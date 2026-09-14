// Package master implements the control-plane server: the ConnectRPC service
// workers dial, the command-correlation layer that turns the worker-opened bidi
// stream into request/response calls, and the higher-level session orchestration
// the operator panel drives.
package master

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1/controlplanev1connect"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/bus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// ErrCommandTimeout is returned when a worker does not answer a command in time.
var ErrCommandTimeout = errors.New("worker command timed out")

// DefaultCommandTimeout bounds how long the master waits for a CommandResult.
const DefaultCommandTimeout = 30 * time.Second

// Server implements controlplanev1connect.ControlPlaneServiceHandler.
type Server struct {
	reg     *registry.Registry
	auth    *pairing.Authenticator
	bus     *bus.Bus
	logger  *slog.Logger
	newID   func() string
	cmdTO   time.Duration
	pending sync.Map // request_id (string) -> chan *cpv1.CommandResult
	tunnels *tunnelRegistry
	gate    *auth.Gate // operator login for the UI proxy; nil/disabled runs open
	repos   *repoStore // clone placement per repo (in-memory, re-derived)

	// sessionUsers maps sessionID -> the operator account that started it,
	// when that account is a non-admin, repo-scoped user. Live state only
	// (sessions are routed in-memory too); a session absent from the map was
	// started by an admin or before user scoping existed.
	sessionUsers sync.Map // sessionID (string) -> userID (string)
}

// Options configures a Server. Zero values fall back to sane defaults.
type Options struct {
	Registry       *registry.Registry
	Auth           *pairing.Authenticator
	Bus            *bus.Bus
	Logger         *slog.Logger
	CommandTimeout time.Duration
	// NewID overrides id generation (tests inject a deterministic generator).
	NewID func() string
	// Gate is the operator login for the browser-facing UI proxy. When nil or
	// disabled (no password), the proxy runs unauthenticated.
	Gate *auth.Gate
}

// New constructs a Server.
func New(opts Options) *Server {
	s := &Server{
		reg:     opts.Registry,
		auth:    opts.Auth,
		bus:     opts.Bus,
		logger:  opts.Logger,
		newID:   opts.NewID,
		cmdTO:   opts.CommandTimeout,
		tunnels: newTunnelRegistry(),
		gate:    opts.Gate,
		repos:   newRepoStore(),
	}
	if s.reg == nil {
		s.reg = registry.New()
	}
	if s.bus == nil {
		s.bus = bus.New(0)
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.newID == nil {
		s.newID = randomID
	}
	if s.cmdTO <= 0 {
		s.cmdTO = DefaultCommandTimeout
	}
	return s
}

// Registry exposes the worker/session store (used by the panel HTTP layer).
func (s *Server) Registry() *registry.Registry { return s.reg }

// Bus exposes the master event bus (the panel's SSE handler subscribes here).
func (s *Server) Bus() *bus.Bus { return s.bus }

// Handler returns the mounted ConnectRPC handler and its path prefix.
func (s *Server) Handler(opts ...connect.HandlerOption) (string, http.Handler) {
	return controlplanev1connect.NewControlPlaneServiceHandler(s, opts...)
}

// --- ConnectRPC handlers ----------------------------------------------------

// Register authenticates a worker and issues its first token.
func (s *Server) Register(
	ctx context.Context,
	req *connect.Request[cpv1.RegisterRequest],
) (*connect.Response[cpv1.RegisterResponse], error) {
	if !s.auth.CheckSecret(req.Msg.GetPairingSecret()) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid pairing secret"))
	}
	now := time.Now()
	token, err := s.auth.Mint(now)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Adopt a stable worker-presented id when it is well-formed so the worker's
	// identity (and its panel subdomain) survives reconnects; otherwise mint a
	// fresh one. A malformed presented id is rejected rather than silently
	// replaced, so a worker that thinks it is "worker-a" never ends up registered
	// as a different id it does not know about.
	id := req.Msg.GetWorkerId()
	if id == "" {
		id = s.newID()
	} else if !validWorkerID(id) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid worker_id %q: must be 1-63 lowercase alphanumeric or hyphen, not starting/ending with hyphen", id))
	}
	s.reg.Add(id, req.Msg.GetWorkerName(), req.Msg.GetCapabilities(), fromProtoWorkspaces(req.Msg.GetWorkspaces()), token, now)
	s.logger.Info("worker registered", "workerID", id, "name", req.Msg.GetWorkerName())
	return connect.NewResponse(&cpv1.RegisterResponse{
		WorkerId:         id,
		WorkerToken:      token.Value,
		TokenExpiresUnix: token.ExpiresAt.Unix(),
	}), nil
}

// Heartbeat refreshes liveness and rotates the token near expiry.
func (s *Server) Heartbeat(
	ctx context.Context,
	req *connect.Request[cpv1.HeartbeatRequest],
) (*connect.Response[cpv1.HeartbeatResponse], error) {
	id, ok := s.reg.WorkerIDForToken(req.Msg.GetWorkerToken())
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("unknown worker token"))
	}
	now := time.Now()
	tok, _ := s.reg.Token(id)
	if tok.Expired(now) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("worker token expired; re-register"))
	}
	s.reg.Touch(id, now)

	resp := &cpv1.HeartbeatResponse{Ok: true}
	if s.auth.ShouldRotate(tok, now) {
		newTok, err := s.auth.Mint(now)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		s.reg.UpdateToken(id, newTok)
		resp.WorkerToken = newTok.Value
		resp.TokenExpiresUnix = newTok.ExpiresAt.Unix()
	}
	return connect.NewResponse(resp), nil
}

// WorkerStream is the worker-opened bidi control channel. See the proto doc for
// the framing contract: first frame must be Hello, then commands flow down and
// results/events flow up.
func (s *Server) WorkerStream(
	ctx context.Context,
	stream *connect.BidiStream[cpv1.WorkerToMaster, cpv1.MasterToWorker],
) error {
	first, err := stream.Receive()
	if err != nil {
		return connect.NewError(connect.CodeAborted, fmt.Errorf("receive hello: %w", err))
	}
	hello := first.GetHello()
	if hello == nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("first frame must be Hello"))
	}
	id, ok := s.reg.WorkerIDForToken(hello.GetWorkerToken())
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("unknown worker token"))
	}
	tok, _ := s.reg.Token(id)
	if tok.Expired(time.Now()) {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("worker token expired"))
	}

	conn := &streamConn{stream: stream}
	if err := s.reg.Attach(id, conn, time.Now()); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	defer s.reg.Detach(id, conn)
	s.logger.Info("worker stream attached", "workerID", id)

	for {
		msg, err := stream.Receive()
		if err != nil {
			// Clean EOF or transport error: the worker went away.
			s.logger.Info("worker stream closed", "workerID", id, "err", err)
			return nil
		}
		s.dispatchFromWorker(id, msg)
	}
}

// dispatchFromWorker routes one worker->master frame.
func (s *Server) dispatchFromWorker(workerID string, msg *cpv1.WorkerToMaster) {
	switch m := msg.GetMessage().(type) {
	case *cpv1.WorkerToMaster_CommandResult:
		s.deliverResult(m.CommandResult)
	case *cpv1.WorkerToMaster_SessionEvent:
		// Re-publish onto the master bus with a FRESH master seq (the worker's
		// original numbering is intentionally discarded).
		ev := m.SessionEvent
		s.bus.PublishRaw(ev.GetType(), ev.GetProperties())
	case *cpv1.WorkerToMaster_ResourceFrame:
		// Control frame: forwarded to the panel out-of-band (Phase 4). It must
		// never touch the bus, or it would burn a seq and trigger client resyncs.
		s.handleResourceFrame(m.ResourceFrame)
	case *cpv1.WorkerToMaster_Pong:
		s.deliverPong(workerID, m.Pong)
	case *cpv1.WorkerToMaster_Hello:
		// A second Hello is a protocol error; ignore rather than tear down.
		s.logger.Warn("unexpected duplicate Hello", "workerID", workerID)
	default:
		s.logger.Warn("unknown worker frame", "workerID", workerID)
	}
}

func (s *Server) deliverResult(res *cpv1.CommandResult) {
	if res == nil {
		return
	}
	if ch, ok := s.pending.Load(res.GetRequestId()); ok {
		select {
		case ch.(chan *cpv1.CommandResult) <- res:
		default:
		}
	}
}

// handleResourceFrame is a hook for the SSE control-frame path (Phase 4). For
// now it is a no-op so the frame is consumed without touching the bus.
func (s *Server) handleResourceFrame(_ *cpv1.ResourceFrame) {}

// deliverPong is a hook for stream health checks (Phase 4+).
func (s *Server) deliverPong(_ string, _ *cpv1.Pong) {}

// --- Higher-level orchestration ---------------------------------------------

// Call sends a command to a worker and waits for its correlated CommandResult.
func (s *Server) Call(ctx context.Context, workerID string, cmd *cpv1.MasterToWorker) (*cpv1.CommandResult, error) {
	reqID := s.newID()
	cmd.RequestId = reqID
	ch := make(chan *cpv1.CommandResult, 1)
	s.pending.Store(reqID, ch)
	defer s.pending.Delete(reqID)

	if err := s.reg.SendCommand(workerID, cmd); err != nil {
		return nil, err
	}
	select {
	case res := <-ch:
		return res, nil
	case <-time.After(s.cmdTO):
		return nil, ErrCommandTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ListWorkspaces asks a worker for its current workspaces and caches them.
func (s *Server) ListWorkspaces(ctx context.Context, workerID string) ([]registry.Workspace, error) {
	res, err := s.Call(ctx, workerID, &cpv1.MasterToWorker{
		Command: &cpv1.MasterToWorker_ListWorkspaces{ListWorkspaces: &cpv1.ListWorkspaces{}},
	})
	if err != nil {
		return nil, err
	}
	if !res.GetOk() {
		return nil, errors.New(res.GetError())
	}
	ws := fromProtoWorkspaces(res.GetWorkspaces())
	s.reg.SetWorkspaces(workerID, ws)
	return ws, nil
}

// StartRemoteAgent mints a global session id, tells the worker to host it, and
// (on success) routes the session to that worker. The returned id is the global
// session id used everywhere afterwards. When the spec targets a user
// worktree (RepoURL + UserName set), the worker resolves the pair to the
// worktree itself and the workspace field stays empty.
func (s *Server) StartRemoteAgent(ctx context.Context, workerID string, spec StartSpec) (string, error) {
	sessionID := "ses_" + s.newID()
	res, err := s.Call(ctx, workerID, &cpv1.MasterToWorker{
		Command: &cpv1.MasterToWorker_StartAgent{StartAgent: &cpv1.StartAgent{
			SessionId:      sessionID,
			Workspace:      spec.Workspace,
			AgentName:      spec.AgentName,
			Prompt:         spec.Prompt,
			Ref:            spec.Ref,
			RepoUrl:        spec.RepoURL,
			UserName:       spec.UserName,
			ViewportWidth:  int32(spec.ViewportWidth),
			ViewportHeight: int32(spec.ViewportHeight),
		}},
	})
	if err != nil {
		return "", err
	}
	if !res.GetOk() {
		return "", errors.New(res.GetError())
	}
	s.reg.RouteSession(sessionID, workerID)
	if spec.UserName != "" {
		s.sessionUsers.Store(sessionID, spec.UserName)
	}
	return sessionID, nil
}

// StartSpec parameterizes StartRemoteAgent. Workspace is the absolute host
// path form; RepoURL+UserName is the logical (repo, user) form — the worker
// resolves exactly one of the two.
type StartSpec struct {
	Workspace      string
	RepoURL        string
	UserName       string
	AgentName      string
	Prompt         string
	Ref            string
	ViewportWidth  int
	ViewportHeight int
}

// StartUserSession routes a session to the worker holding repoURL's clone and
// targets it at userName's worktree — the multi-user flow the console's
// per-user sessions drive. The placement must already be recorded (AssignUser
// ran earlier); a repo never assigned has no clone to host it.
func (s *Server) StartUserSession(ctx context.Context, repoURL, userName string, spec StartSpec) (string, error) {
	rec, ok := s.repos.get(repoSlugFromURL(repoURL))
	if !ok {
		return "", fmt.Errorf("repo %s has no placement — assign the user first", repoURL)
	}
	spec.RepoURL = rec.URL
	spec.UserName = userName
	return s.StartRemoteAgent(ctx, rec.WorkerID, spec)
}

// SessionUser returns the operator account that started sessionID, when that
// session was started by a non-admin, repo-scoped user.
func (s *Server) SessionUser(sessionID string) (string, bool) {
	v, ok := s.sessionUsers.Load(sessionID)
	if !ok {
		return "", false
	}
	user, _ := v.(string)
	return user, user != ""
}

// RecordSessionUser records the operator account that started sessionID — the
// test seam for the bookkeeping StartRemoteAgent performs for user sessions;
// production callers use StartRemoteAgent itself.
func (s *Server) RecordSessionUser(sessionID, userID string) {
	if userID != "" {
		s.sessionUsers.Store(sessionID, userID)
	}
}

// ReapLoop marks workers dead after the timeout and fails their routed sessions.
// It runs until ctx is cancelled.
func (s *Server) ReapLoop(ctx context.Context, timeout time.Duration, tick time.Duration) {
	if tick <= 0 {
		tick = timeout / 3
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			dead, orphans := s.reg.ReapStale(timeout, now)
			for _, id := range dead {
				s.logger.Warn("worker marked dead (missed heartbeat)", "workerID", id)
			}
			for _, sid := range orphans {
				s.failSession(sid, "worker offline")
			}
		}
	}
}

// failSession publishes a synthetic finish event for a stranded session and
// forgets its routing. The panel renders it as a failed session.
func (s *Server) failSession(sessionID, reason string) {
	s.reg.UnrouteSession(sessionID)
	s.bus.Publish("session.failed", map[string]string{
		"sessionId":    sessionID,
		"finishReason": reason,
	})
}

// --- helpers ----------------------------------------------------------------

// streamConn adapts a Connect bidi stream to registry.Conn, serializing Send
// (Connect forbids concurrent Send on one stream).
type streamConn struct {
	mu     sync.Mutex
	stream *connect.BidiStream[cpv1.WorkerToMaster, cpv1.MasterToWorker]
}

func (c *streamConn) Send(cmd *cpv1.MasterToWorker) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stream.Send(cmd)
}

func fromProtoWorkspaces(in []*cpv1.Workspace) []registry.Workspace {
	out := make([]registry.Workspace, 0, len(in))
	for _, w := range in {
		out = append(out, registry.Workspace{
			Path:    w.GetPath(),
			Name:    w.GetName(),
			Branch:  w.GetBranch(),
			Present: w.GetPresent(),
		})
	}
	return out
}

// randomID returns a short, URL-safe, lowercase random id.
func randomID() string {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand failure is fatal-grade, but returning a time-based fallback
		// keeps the control plane alive rather than panicking a handler.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
}

// validWorkerID reports whether id is safe to use as a DNS subdomain label (the
// worker's panel URL is <id>.<host>). It mirrors the RFC 1123 label rules: 1-63
// chars, lowercase alphanumeric or hyphen, not starting or ending with a hyphen.
func validWorkerID(id string) bool {
	if len(id) == 0 || len(id) > 63 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-':
			if i == 0 || i == len(id)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
