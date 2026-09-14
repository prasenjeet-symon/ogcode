// Package worker is the ogcode side of the remote-agent-workers control plane.
// It is a pure ConnectRPC *client*: it dials the master (the separate
// ogcode-control-plane service), authenticates with the shared pairing secret,
// and opens ONE long-lived bidi stream. Master->worker commands arrive down that
// stream and worker->master events/results go back up it — the worker never
// listens for inbound connections.
//
// It hosts agent sessions by starting one full standalone ogcode server per
// worktree directory (server.NewWithOptions + Serve, see servers.go) and
// driving it through its exported HostSession/Guidance/ReplyPermission API —
// the same wiring a local `ogcode` run has, per directory. Nothing in this
// package alters ogcode's existing behavior — it only adds a new run-mode.
package worker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1/controlplanev1connect"
	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// heartbeatInterval is how often the worker pings the master. It must be well
// under the master's missed-heartbeat timeout (default 45s). It defaults to 15s
// and is a field so tests can shorten it.
const defaultHeartbeatInterval = 15 * time.Second

// reconnect backoff bounds for the session resume loop. A failed register or a
// dropped stream is retried, sleeping exponentially from minBackoff up to
// maxBackoff. A re-register after a token rejection starts at minBackoff so a
// repaired pairing reconnects promptly.
const (
	minBackoff = 1 * time.Second
	maxBackoff = 30 * time.Second
)

// Options configures a Worker.
type Options struct {
	// MasterURL is the control-plane endpoint, e.g. "https://master:8443" (TLS)
	// or "http://localhost:8443" (h2c, dev only).
	MasterURL string
	// PairingSecret must match the master's configured secret.
	PairingSecret string
	// WorkerName is a human-friendly label (defaults to the hostname).
	WorkerName string
	// Workspaces are the roots this worker offers; worktrees under them are
	// discovered automatically.
	Workspaces []string
	// RepoRoot is where managed repo clones live (default ~/.ogcode/repos).
	// For the clones' user worktrees to be discoverable and hostable, this
	// must also be offered via --workspace — workspace containment refuses
	// directories outside the configured roots.
	RepoRoot string
	// CACertPath, when set, is a PEM file of extra CA(s) to trust for the master's
	// TLS certificate (for a self-signed or private-CA master). Public certs
	// (e.g. Let's Encrypt) need nothing here.
	CACertPath string
	// Insecure skips TLS verification of the master. Development only.
	Insecure bool
	Logger   *slog.Logger
}

// Worker is the running worker daemon.
type Worker struct {
	opts   Options
	logger *slog.Logger
	client controlplanev1connect.ControlPlaneServiceClient

	workerID string

	tokenMu     sync.RWMutex
	token       string
	tokenExpiry time.Time

	stream *connect.BidiStreamForClient[cpv1.WorkerToMaster, cpv1.MasterToWorker]
	sendMu sync.Mutex

	// srvs hosts one standalone ogcode server per worktree directory.
	srvs *serverManager
	// tunnelled records the directories whose tunnel + event relay are already
	// open (one per server, for the server's lifetime).
	tunnelled *sync.Map

	sessions *sessionRegistry

	// repos maps repoURL → cloneDir for repos this worker manages (one clone
	// per repo; user worktrees hang off it). Not persisted — re-discovered
	// from disk on first use. Guarded by repoLock (repos.go).
	repos       map[string]string
	reposSeeded bool

	ctx context.Context

	// reauthenticate is set when any stream (worker stream or tunnel) rejects
	// our token. It forces the next session to drop the persisted credential and
	// re-register instead of re-using the stale token.
	reauthenticate atomic.Bool
	// heartbeatInterval controls how often the worker pings the master; default
	// 15s, shortened by tests.
	heartbeatInterval time.Duration
}

// New constructs a Worker.
func New(opts Options) *Worker {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.WorkerName == "" {
		if h, err := os.Hostname(); err == nil {
			opts.WorkerName = h
		} else {
			opts.WorkerName = "ogcode-worker"
		}
	}
	return &Worker{
		opts:              opts,
		logger:            opts.Logger,
		srvs:              newServerManager(nil, opts.Logger),
		sessions:          newSessionRegistry(),
		tunnelled:         &sync.Map{},
		heartbeatInterval: defaultHeartbeatInterval,
	}
}

// Run dials the master, authenticates (reusing a persisted token when valid, or
// re-pairing), and serves the command stream until ctx is cancelled. If the
// stream drops or a heartbeat is rejected, the worker loops and reconnects with
// capped backoff — a restarted master re-recognizes the worker's token, so
// reconnecting does not re-pair. It cleans up all hosted sessions and servers
// on exit.
func (w *Worker) Run(ctx context.Context) error {
	w.ctx = ctx
	w.srvs = newServerManager(ctx, w.logger)
	client, err := w.buildClient()
	if err != nil {
		return err
	}
	w.client = client
	defer w.shutdown()

	// Resume a persisted, unexpired token if we have one: the master's registry
	// persists tokens too, so it re-recognizes us and we skip the pairing round
	// trip. A missing/expired credential or a rejected token forces a register.
	w.resumeStoredToken()

	backoff := minBackoff
	for {
		if ctx.Err() != nil {
			return nil
		}
		if w.tokenMissingOrExpired() || w.reauthenticate.Load() {
			if err := w.register(ctx); err != nil {
				w.logger.Warn("register failed; retrying", "err", err)
				if !sleepBackoff(ctx, backoff) {
					return nil
				}
				backoff = min(2*backoff, maxBackoff)
				continue
			}
			// We hold a fresh token now; a register succeeding also means the
			// credential was accepted, so clear any reauth flag for later.
			w.logger.Info("registered with master", "workerID", w.workerID, "master", w.opts.MasterURL)
			backoff = minBackoff
		}

		// A rejected token during a session (stream or heartbeat) re-pairs next
		// loop. Reset the flag for the resume decision only after registering.
		w.reauthenticate.Store(false)

		ended, streamErr := w.runSession(ctx)
		if ctx.Err() != nil {
			return nil // clean shutdown requested during the session
		}
		if streamErr != nil {
			w.logger.Warn("session ended; reconnecting", "err", streamErr)
		} else if ended {
			return nil
		}
		if !sleepBackoff(ctx, backoff) {
			return nil
		}
		backoff = min(2*backoff, maxBackoff)
	}
}

// runSession serves ONE connected session: opens the worker stream, sends the
// hello, and runs the heartbeat and tunnel until the stream fails or the parent
// context is cancelled. It returns (true, nil) when the parent context ended
// (clean shutdown) and (false, err) when the stream died for another reason.
func (w *Worker) runSession(parent context.Context) (bool, error) {
	sessionCtx, cancel := context.WithCancel(parent)
	defer cancel()

	w.stream = w.client.WorkerStream(sessionCtx)
	if err := w.send(&cpv1.WorkerToMaster{
		Message: &cpv1.WorkerToMaster_Hello{Hello: &cpv1.Hello{WorkerToken: w.getToken()}},
	}); err != nil {
		return parent.Err() != nil, fmt.Errorf("send hello: %w", err)
	}

	reauth := func() {
		w.reauthenticate.Store(true)
		// Drop the credential so we don't keep presenting a rejected token.
		w.setToken("", time.Time{})
		// Cancel the session: it tears down the stream, tunnel and heartbeat, so
		// the receive loop returns and the run loop re-registers with a fresh token.
		cancel()
	}

	go w.heartbeatLoop(sessionCtx, reauth)

	// Open tunnels (+ per-dir servers) for every discovered workspace right
	// away, not lazily on the first StartAgent: the console shows a freshly
	// connected worktree and its UI link before any session runs there.
	// Idempotent (see tunnelAndRelayOnce); best-effort — failures are logged.
	go w.ensureEagerTunnels()

	// Receive loop: commands from the master.
	for {
		cmd, err := w.stream.Receive()
		if err != nil {
			if parent.Err() != nil {
				return true, nil // clean shutdown
			}
			if connect.CodeOf(err) == connect.CodeUnauthenticated {
				w.logger.Warn("session rejected by master; re-pairing", "err", err)
				reauth()
				return false, nil // signal a reconnect with a fresh register
			}
			return false, fmt.Errorf("worker stream closed: %w", err)
		}
		go w.handleCommand(cmd)
	}
}

func (w *Worker) buildClient() (controlplanev1connect.ControlPlaneServiceClient, error) {
	var hc *http.Client
	if strings.HasPrefix(w.opts.MasterURL, "https://") {
		// h2 over TLS via ALPN.
		tlsCfg, err := w.tlsClientConfig()
		if err != nil {
			return nil, err
		}
		hc = &http.Client{Transport: &http2.Transport{TLSClientConfig: tlsCfg}}
	} else {
		// h2c (cleartext HTTP/2) for local development.
		hc = &http.Client{Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		}}
	}
	return controlplanev1connect.NewControlPlaneServiceClient(hc, w.opts.MasterURL), nil
}

// tlsClientConfig builds the TLS config for an https master, honoring a custom
// CA bundle (self-signed / private CA) or insecure skip-verify.
func (w *Worker) tlsClientConfig() (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if w.opts.Insecure {
		cfg.InsecureSkipVerify = true // dev only
		return cfg, nil
	}
	if w.opts.CACertPath != "" {
		pem, err := os.ReadFile(w.opts.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("read master CA %s: %w", w.opts.CACertPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("master CA %s: no valid certificates", w.opts.CACertPath)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

func (w *Worker) register(ctx context.Context) error {
	// Present a stable, persisted worker id so the master registers us under the
	// same id across restarts (the panel subdomain <id>.<host> stays valid). The
	// master mints a fresh id only when we present none.
	stableID, err := loadOrCreateWorkerID()
	if err != nil {
		w.logger.Warn("could not persist stable worker id; master will mint one", "err", err)
	}
	resp, err := w.client.Register(ctx, connect.NewRequest(&cpv1.RegisterRequest{
		PairingSecret: w.opts.PairingSecret,
		WorkerName:    w.opts.WorkerName,
		WorkerId:      stableID,
		Workspaces:    discoverWorkspaces(w.opts.Workspaces),
		Capabilities:  []string{"git"},
	}))
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	w.workerID = resp.Msg.GetWorkerId()
	w.setToken(resp.Msg.GetWorkerToken(), time.Unix(resp.Msg.GetTokenExpiresUnix(), 0))
	return nil
}

// resumeStoredToken loads a persisted, unexpired token and adopts it so the
// first connection can skip the pairing round trip. Best-effort; a missing or
// expired credential leaves the worker to register normally.
func (w *Worker) resumeStoredToken() {
	token, exp, ok := loadCred()
	if !ok {
		return
	}
	if exp.Before(time.Now()) {
		w.logger.Debug("stored worker token expired; will re-pair")
		return
	}
	w.setToken(token, exp)
	w.logger.Info("resuming stored worker token")
}

// tokenMissingOrExpired reports whether the worker currently holds no usable
// auth token and so must (re-)register.
func (w *Worker) tokenMissingOrExpired() bool {
	w.tokenMu.RLock()
	defer w.tokenMu.RUnlock()
	return w.token == "" || w.tokenExpiry.Before(time.Now())
}

// sleepBackoff waits up to d before a reconnect attempt. It returns false (and
// returns immediately) when ctx is cancelled, so a clean shutdown never waits
// out the backoff.
func sleepBackoff(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (w *Worker) heartbeatLoop(ctx context.Context, onReauth func()) {
	t := time.NewTicker(w.heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			resp, err := w.client.Heartbeat(ctx, connect.NewRequest(&cpv1.HeartbeatRequest{
				WorkerToken: w.getToken(),
			}))
			if err != nil {
				if connect.CodeOf(err) == connect.CodeUnauthenticated {
					// The token no longer validates (e.g. the master was wiped or we
					// were de-paired). Re-pair now instead of retrying with a dead token
					// forever — the loop returns and the run loop triggers a register.
					w.logger.Warn("heartbeat rejected; will re-pair", "err", err)
					onReauth()
					return
				}
				w.logger.Warn("heartbeat failed", "err", err)
				continue
			}
			if rotated := resp.Msg.GetWorkerToken(); rotated != "" {
				w.setToken(rotated, time.Unix(resp.Msg.GetTokenExpiresUnix(), 0))
				w.logger.Debug("worker token rotated")
			}
		}
	}
}

// handleCommand dispatches one master->worker command. Each runs in its own
// goroutine (see Run) so a slow StartAgent never stalls the receive loop.
func (w *Worker) handleCommand(cmd *cpv1.MasterToWorker) {
	switch c := cmd.GetCommand().(type) {
	case *cpv1.MasterToWorker_Ping:
		_ = w.send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_Pong{
			Pong: &cpv1.Pong{Nonce: c.Ping.GetNonce()},
		}})
	case *cpv1.MasterToWorker_ListWorkspaces:
		w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{
			Ok:         true,
			Workspaces: discoverWorkspaces(w.opts.Workspaces),
		})
	case *cpv1.MasterToWorker_StartAgent:
		if err := w.startAgent(c.StartAgent); err != nil {
			w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: false, Error: err.Error()})
			return
		}
		w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: true})
	case *cpv1.MasterToWorker_AddUserWorktree:
		// Eager provisioning at assignment time: clone once, then create the
		// user's worktree so it exists before the first session.
		au := c.AddUserWorktree
		repoDir, err := w.EnsureRepo(w.ctx, au.GetRepoUrl())
		if err == nil {
			_, _, err = w.EnsureUserWorktree(repoDir, au.GetUserName(), au.GetBaseBranch())
		}
		if err != nil {
			w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: false, Error: err.Error()})
			return
		}
		w.logger.Info("provisioned user worktree", "repo", au.GetRepoUrl(), "user", au.GetUserName())
		w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: true})
	case *cpv1.MasterToWorker_ListRepos:
		res := &cpv1.CommandResult{Ok: true}
		for url, dir := range w.managedRepos() {
			res.Repos = append(res.Repos, &cpv1.RepoInfo{
				Name:     filepath.Base(dir),
				Url:      url,
				WorkerId: w.workerID,
			})
		}
		w.replyResult(cmd.GetRequestId(), res)
	case *cpv1.MasterToWorker_Stats:
		res := &cpv1.CommandResult{Ok: true}
		if free, err := freeBytes(w.repoRoot()); err == nil {
			res.FreeBytes = free
		} else {
			res.Ok = false
			res.Error = err.Error()
		}
		w.replyResult(cmd.GetRequestId(), res)
	case *cpv1.MasterToWorker_CloneRepo:
		// Eager clone at repository-registration time, before any user is
		// assigned: warm the single clone so assignment only provisions a
		// worktree. Idempotent (EnsureRepo reuses an existing usable clone).
		cr := c.CloneRepo
		if _, err := w.EnsureRepo(w.ctx, cr.GetRepoUrl()); err != nil {
			w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: false, Error: err.Error()})
			return
		}
		w.logger.Info("cloned repo (master request)", "url", cr.GetRepoUrl())
		w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: true})
	case *cpv1.MasterToWorker_RemoveUserWorktree:
		// Lifecycle: drop a user's worktree, keep the branch (the work stays
		// mergeable). A repo dir is resolved from the URL first so a typo'd
		// or unmanaged URL fails before anything is removed.
		rw := c.RemoveUserWorktree
		repoDir, err := w.resolveManagedRepoDir(rw.GetRepoUrl())
		if err == nil {
			err = w.RemoveUserWorktree(repoDir, rw.GetUserName())
		}
		if err != nil {
			w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: false, Error: err.Error()})
			return
		}
		w.logger.Info("removed user worktree (master request)", "repo", rw.GetRepoUrl(), "user", rw.GetUserName())
		w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: true})
	case *cpv1.MasterToWorker_MergeUserBranch:
		// Lifecycle: merge the user's branch back into the base branch in a
		// temporary worktree; summary carries the outcome for the console.
		mb := c.MergeUserBranch
		repoDir, err := w.resolveManagedRepoDir(mb.GetRepoUrl())
		var summary string
		if err == nil {
			summary, err = w.MergeUserBranch(repoDir, mb.GetUserName(), mb.GetBaseBranch(), mb.GetPush())
		}
		if err != nil {
			w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: false, Error: err.Error()})
			return
		}
		w.logger.Info("merged user branch (master request)", "repo", mb.GetRepoUrl(), "user", mb.GetUserName(), "summary", summary)
		w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: true, Summary: summary})
	case *cpv1.MasterToWorker_DeprovisionRepo:
		// Lifecycle: retire a managed repo — remove remaining user worktrees
		// (branches kept) and optionally delete the clone itself.
		dp := c.DeprovisionRepo
		summary, err := w.DeprovisionRepo(dp.GetRepoUrl(), dp.GetRemoveClone())
		if err != nil {
			w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: false, Error: err.Error()})
			return
		}
		w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: true, Summary: summary})
	case *cpv1.MasterToWorker_StopAgent:
		ok := w.sessions.cancel(c.StopAgent.GetSessionId())
		w.replyResult(cmd.GetRequestId(), okResult(ok, "session not hosted here"))
	case *cpv1.MasterToWorker_AbortSession:
		ok := w.sessions.cancel(c.AbortSession.GetSessionId())
		w.replyResult(cmd.GetRequestId(), okResult(ok, "session not hosted here"))
	case *cpv1.MasterToWorker_Guidance:
		g := c.Guidance
		sh := w.hostServerFor(g.GetSessionId())
		if sh == nil {
			// Distinguish "not hosted here" from "hosted but the server has no
			// running loop" so the master can surface the right failure.
			if w.sessions.has(g.GetSessionId()) {
				w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: false, Error: "session is hosted but not running a gated loop"})
			} else {
				w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: false, Error: "session not hosted here"})
			}
			return
		}
		if err := sh.srv.Guidance(session.SessionID(g.GetSessionId()), g.GetText(), true); err != nil {
			w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: false, Error: err.Error()})
			return
		}
		w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: true})
	case *cpv1.MasterToWorker_ApproveTool:
		at := c.ApproveTool
		if err := w.replyToPermission(at.GetSessionId(), at.GetPermissionId(), "once"); err != nil {
			w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: false, Error: err.Error()})
			return
		}
		w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: true})
	case *cpv1.MasterToWorker_RejectTool:
		rt := c.RejectTool
		if err := w.replyToPermission(rt.GetSessionId(), rt.GetPermissionId(), "reject"); err != nil {
			w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: false, Error: err.Error()})
			return
		}
		w.replyResult(cmd.GetRequestId(), &cpv1.CommandResult{Ok: true})
	default:
		w.logger.Warn("unknown command from master")
	}
}

// hostServerFor returns the worktree server hosting a session, or nil when the
// session isn't hosted here.
func (w *Worker) hostServerFor(sessionID string) *serverHost {
	dir := w.sessions.dirOf(sessionID)
	if dir == "" {
		return nil
	}
	return w.srvs.get(dir)
}

// replyToPermission delivers the operator's decision to the worktree server's
// permission manager — the same seam the server's HTTP permission reply uses,
// driven over the stream instead of over HTTP.
func (w *Worker) replyToPermission(sessionID, permID, response string) error {
	sh := w.hostServerFor(sessionID)
	if sh == nil {
		return errors.New("session not hosted here")
	}
	if !sh.srv.ReplyPermission(session.SessionID(sessionID), permID, response) {
		return errors.New("permission request not found")
	}
	return nil
}

func (w *Worker) startAgent(c *cpv1.StartAgent) error {
	dir := c.GetWorkspace()
	if c.GetRepoUrl() != "" && c.GetUserName() != "" {
		// Logical targeting (multi-user repos): the master sends the
		// (repo_url, user_name) pair and the worker resolves it to the user's
		// worktree off the single clone. Clone-once + worktree-ensure are
		// idempotent, so this doubles as the backstop when AddUserWorktree
		// already provisioned the assignment.
		repoDir, err := w.EnsureRepo(w.ctx, c.GetRepoUrl())
		if err != nil {
			return err
		}
		_, dir, err = w.EnsureUserWorktree(repoDir, c.GetUserName(), "")
		if err != nil {
			return err
		}
	} else if dir == "" {
		return errors.New("workspace required")
	}
	// The workspace set chosen at registration is authoritative: a StartAgent
	// may direct the worker at a directory the worker itself offered (a root,
	// a worktree under one, or — for multi-user repos — a clone under the repo
	// root, which the operator must also configure as a workspace) and nowhere
	// else — never an arbitrary path the master or a form produced.
	if !w.workspaceAllowed(dir) {
		return fmt.Errorf("workspace %q is not one this worker offers (its roots are fixed at startup with --workspace)", dir)
	}
	if err := bootstrapWorkspace(dir, c.GetRef()); err != nil {
		return err
	}

	// One full ogcode server per worktree: its own DB, bus, stores, providers,
	// permission manager, loop runner and loopback listener — the same wiring a
	// local `ogcode` run has, isolated per directory.
	sh, err := w.srvs.ensure(dir)
	if err != nil {
		return err
	}

	// First session in this directory: open the tunnel for the panel and start
	// the event relay to the master. Both live for the server's lifetime, so a
	// subsequent StartAgent in the same dir skips straight to HostSession.
	// (The eager pass at connect time usually has this done already; this covers
	// workspaces created or cloned after the worker connected.)
	w.tunnelAndRelayOnce(dir, sh)

	agentName := c.GetAgentName()
	stop, existed, err := sh.srv.HostSession(session.SessionID(c.GetSessionId()), dir, c.GetPrompt(), agentName,
		int(c.GetViewportWidth()), int(c.GetViewportHeight()))
	if err != nil {
		return err
	}
	// existed means the master re-delivered a StartAgent for a session already
	// hosted (or its rows persisted from a previous run): the loop registration
	// was refreshed and no rows were duplicated.
	_ = existed

	w.sessions.add(&hostedSession{id: c.GetSessionId(), dir: dir, stop: stop})
	w.logger.Info("hosting session", "sessionID", c.GetSessionId(), "dir", dir, "agent", agentName, "existed", existed)
	return nil
}

// workspaceAllowed reports whether dir is a workspace this worker chose to
// host: exactly one of its configured roots, or a descendant of one (a git
// worktree created under a root after registration still qualifies). Roots are
// expanded and made absolute the same way discoverWorkspaces does, so the
// check matches what the worker advertises at registration.
func (w *Worker) workspaceAllowed(dir string) bool {
	clean := filepath.Clean(dir)
	for _, root := range w.opts.Workspaces {
		abs, err := filepath.Abs(expandHome(root))
		if err != nil {
			continue
		}
		if clean == abs || strings.HasPrefix(clean+string(filepath.Separator), abs+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// tunnelAndRelayOnce wires ONE worktree server to the master exactly once per
// directory per worker lifetime: the event relay (server bus → stream) and the
// tunnel (panel subdomain → local server). The LoadOrStore dedupe means a
// second caller — StartAgent after the eager pass, or two racing StartAgents —
// sees the wiring already done and returns immediately. Both the relay and the
// tunnel live on w.ctx, so they survive session-stream reconnects (openTunnel
// re-handshakes on its own).
func (w *Worker) tunnelAndRelayOnce(dir string, sh *serverHost) {
	if _, already := w.tunnelled.LoadOrStore(dir, struct{}{}); already {
		return
	}
	ch, stop := sh.srv.SubscribeEvents()
	go w.relayEvents(ch, stop)
	go w.openTunnel(w.ctx, dir, sh)
}

// ensureEagerTunnels brings up a per-dir ogcode server and its tunnel for every
// discovered workspace at connect time, so each worktree is visible and
// reachable in the console (with a working UI link) before any session starts.
// Best-effort: absent roots are skipped, failures are logged and never fatal —
// a directory that fails here still gets a server + tunnel when StartAgent
// arrives for it. Runs on w.ctx for the worker's lifetime.
func (w *Worker) ensureEagerTunnels() {
	for _, ws := range discoverWorkspaces(w.opts.Workspaces) {
		if w.ctx.Err() != nil {
			return
		}
		if !ws.GetPresent() {
			w.logger.Debug("eager tunnel: workspace absent, skipping", "dir", ws.GetPath())
			continue
		}
		sh, err := w.srvs.ensure(ws.GetPath())
		if err != nil {
			w.logger.Warn("eager tunnel: could not start worktree server", "dir", ws.GetPath(), "err", err)
			continue
		}
		w.tunnelAndRelayOnce(ws.GetPath(), sh)
		w.logger.Info("eager tunnel: worktree UI reachable", "dir", ws.GetPath(), "route", routeLabel(ws.GetPath()), "port", sh.port)
	}
}

// relayEvents forwards every bus event from a worktree server's bus up the
// stream as a SessionEvent, tagged with the session id parsed from the event
// payload. A loop.done event also unregisters the session here, so the
// registry mirrors what the server's own bookkeeping already dropped. The
// master re-stamps a fresh master seq on republish, so the worker's seq is
// deliberately not carried.
func (w *Worker) relayEvents(ch <-chan bus.Event, stop func()) {
	defer stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			if id := parseSessionID(evt.Properties); id != "" && evt.Type == "loop.done" {
				w.sessions.remove(id)
			}
			_ = w.send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_SessionEvent{
				SessionEvent: &cpv1.SessionEvent{
					SessionId:  parseSessionID(evt.Properties),
					Type:       evt.Type,
					Properties: evt.Properties,
				},
			}})
		}
	}
}

// send serializes writes to the bidi stream (Connect forbids concurrent Send).
func (w *Worker) send(msg *cpv1.WorkerToMaster) error {
	w.sendMu.Lock()
	defer w.sendMu.Unlock()
	if w.stream == nil {
		return errors.New("stream not open")
	}
	return w.stream.Send(msg)
}

func (w *Worker) replyResult(reqID string, res *cpv1.CommandResult) {
	res.RequestId = reqID
	if err := w.send(&cpv1.WorkerToMaster{Message: &cpv1.WorkerToMaster_CommandResult{CommandResult: res}}); err != nil {
		w.logger.Warn("failed to send command result", "requestID", reqID, "err", err)
	}
}

func (w *Worker) setToken(v string, exp time.Time) {
	w.tokenMu.Lock()
	defer w.tokenMu.Unlock()
	w.token = v
	w.tokenExpiry = exp
	// Persist so a restart reconnects without re-pairing. Clearing the token
	// (on a rejection) also clears the stored credential.
	if v == "" {
		_ = persistCred("", time.Time{})
	} else {
		if err := persistCred(v, exp); err != nil {
			w.logger.Warn("could not persist worker token", "err", err)
		}
	}
}

func (w *Worker) getToken() string {
	w.tokenMu.RLock()
	defer w.tokenMu.RUnlock()
	return w.token
}

func (w *Worker) shutdown() {
	w.sessions.cancelAll()
	w.srvs.stopAll()
}

func okResult(ok bool, failMsg string) *cpv1.CommandResult {
	if ok {
		return &cpv1.CommandResult{Ok: true}
	}
	return &cpv1.CommandResult{Ok: false, Error: failMsg}
}

func parseSessionID(props []byte) string {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(props, &p)
	return p.SessionID
}
