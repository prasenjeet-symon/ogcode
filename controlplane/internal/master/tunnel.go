package master

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"sort"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/hashicorp/yamux"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/tunnel"
)

// tunnelEntry is one live yamux session together with the key parts it was
// registered under, so the console can enumerate a worker's routes.
type tunnelEntry struct {
	workerID string
	route    string // empty = legacy single-server tunnel under the bare id
	sess     *yamux.Session
}

// tunnelRegistry holds one live yamux client session per worker tunnel, keyed
// by the exact host label the session answers on ("<workerID>-<route>" or the
// bare worker id), so the UI proxy can open streams to the right worker.
type tunnelRegistry struct {
	mu       sync.Mutex
	sessions map[string]*tunnelEntry
}

func newTunnelRegistry() *tunnelRegistry {
	return &tunnelRegistry{sessions: map[string]*tunnelEntry{}}
}

func (t *tunnelRegistry) set(workerID, route string, s *yamux.Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := tunnelKey(workerID, route)
	if old := t.sessions[key]; old != nil {
		old.sess.Close()
	}
	t.sessions[key] = &tunnelEntry{workerID: workerID, route: route, sess: s}
}

// get looks a tunnel up by the exact host label of the request. No parsing: the
// label either is a registered key or is not.
func (t *tunnelRegistry) get(label string) (*yamux.Session, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.sessions[label]
	if !ok {
		return nil, false
	}
	return e.sess, true
}

// owner resolves a host label to the worker id and route label the tunnel was
// registered under, so the workspace allowlist can map a request to the
// workspace it serves. ok=false when the label is not (or no longer)
// registered.
func (t *tunnelRegistry) owner(label string) (workerID, route string, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.sessions[label]
	if !ok {
		return "", "", false
	}
	return e.workerID, e.route, true
}

// del removes a session only if it is still the current one (a late teardown
// from a replaced tunnel must not clobber a fresh reconnect).
func (t *tunnelRegistry) del(workerID, route string, s *yamux.Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := tunnelKey(workerID, route)
	if e := t.sessions[key]; e != nil && e.sess == s {
		delete(t.sessions, key)
	}
}

// routes returns the route labels a worker currently has tunnels for, sorted.
// The legacy empty route is excluded — the console links to worktree UIs only.
func (t *tunnelRegistry) routes(workerID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var routes []string
	for _, e := range t.sessions {
		if e.workerID == workerID && e.route != "" {
			routes = append(routes, e.route)
		}
	}
	sort.Strings(routes)
	return routes
}

// Tunnel authenticates a worker-opened byte pipe and runs the master end of the
// yamux multiplexer over it. It blocks until the worker disconnects.
func (s *Server) Tunnel(ctx context.Context, stream *connect.BidiStream[cpv1.TunnelChunk, cpv1.TunnelChunk]) error {
	// The worker's FIRST frame is its token. Reading it here does double duty:
	// it authenticates the tunnel, and it forces Connect to deliver the request
	// to this handler (a bidi request isn't dispatched until the client sends).
	// It is consumed BEFORE yamux takes over, so it never enters the byte stream.
	first, err := stream.Receive()
	if err != nil {
		return connect.NewError(connect.CodeAborted, fmt.Errorf("tunnel handshake: %w", err))
	}
	token := string(first.GetData())
	id, ok := s.reg.WorkerIDForToken(token)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("unknown worker token"))
	}
	if tok, _ := s.reg.Token(id); tok.Expired(time.Now()) {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("worker token expired"))
	}

	// A per-worktree tunnel carries a route label (the worktree directory's
	// DNS-safe name); the master keys it as "<workerID>-<route>" so one worker
	// can serve several worktree UIs at distinct subdomains. An empty route is
	// the legacy single-server form, keyed under the bare worker id.

	sess, err := tunnel.ClientSession(stream, nil)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	s.tunnels.set(id, first.GetRoute(), sess)
	defer func() {
		s.tunnels.del(id, first.GetRoute(), sess)
		sess.Close()
	}()
	s.logger.Info("worker tunnel attached", "workerID", id, "route", first.GetRoute())

	select {
	case <-ctx.Done():
	case <-sess.CloseChan():
	}
	s.logger.Info("worker tunnel closed", "workerID", id, "route", first.GetRoute())
	return nil
}

// tunnelKey computes the tunnel registry key for a worker connection: the bare
// worker id when no route label was presented (legacy single-server tunnel),
// or "<workerID>-<route>" for a per-worktree tunnel. The key IS the host's
// first dotted label by construction — lookups are exact whole-label matches
// against it (hostLabel never splits on hyphens), so worker ids containing
// hyphens are unambiguous. A request only routes when the label was actually
// registered by an authenticated worker.
func tunnelKey(workerID, route string) string {
	if route == "" {
		return workerID
	}
	return workerID + "-" + route
}

// UIProxyHandler wraps base so that a request whose host's first label names a
// worker with an open tunnel is reverse-proxied to that worker's local ogcode UI
// (e.g. http://<workerID>.panel.example/). Everything else falls through to base
// (the ConnectRPC handlers, health check, etc.).
func (s *Server) UIProxyHandler(base http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.tunnels.get(hostLabel(r.Host))
		if !ok {
			// Apex host (not a worker subdomain). The base-host console and its
			// operator login/logout are served here; everything else (the
			// ConnectRPC endpoints, health check) falls through to base. The RPC
			// endpoints are authenticated by the pairing secret / worker token,
			// not the operator login, so they bypass the gate.
			if isApexConsole(r) {
				s.handleApexConsole(w, r)
				return
			}
			base.ServeHTTP(w, r)
			return
		}

		// Worker-subdomain request → the browser-facing UI, operator-gated.
		if s.gate != nil && s.gate.Enabled() {
			switch r.URL.Path {
			case operatorLoginPath:
				s.handleOperatorLogin(w, r)
				return
			case operatorLogoutPath:
				s.handleOperatorLogout(w, r)
				return
			}
			if !s.gate.Authenticated(r) {
				if wantsHTML(r) {
					renderLogin(w, "")
				} else {
					http.Error(w, "operator login required", http.StatusUnauthorized)
				}
				return
			}
			// Per-user workspace allowlist (enforced only for accounts carrying
			// one — an empty allowlist is unrestricted). Fail-closed: a store
			// error or an unresolvable tunnel owner denies, never falls through.
			if !s.workspaceAllowed(r, hostLabel(r.Host)) {
				if wantsHTML(r) {
					renderForbidden(w, hostLabel(r.Host))
				} else {
					http.Error(w, "workspace not allowed for this account", http.StatusForbidden)
				}
				return
			}
		}
		s.serveTunneledUI(w, r, sess)
	})
}

func (s *Server) serveTunneledUI(w http.ResponseWriter, r *http.Request, sess *yamux.Session) {
	rp := &httputil.ReverseProxy{
		// Flush immediately so the worker's SSE event stream (text/event-stream)
		// reaches the browser live rather than buffering.
		FlushInterval: -1,
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			// Host is a placeholder; the Transport dials the yamux stream, not DNS.
			req.URL.Host = "ogcode-worker"
		},
		Transport: &http.Transport{
			// Each browser connection maps to a fresh yamux stream spliced to the
			// worker's local ogcode; no pooling to avoid reusing a torn stream.
			DisableKeepAlives: true,
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return sess.Open()
			},
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "worker tunnel error: "+err.Error(), http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// hostLabel returns the first dotted label of an HTTP Host (minus any port):
// "abc123.panel.example:8443" -> "abc123".
func hostLabel(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if i := strings.IndexByte(host, '.'); i >= 0 {
		host = host[:i]
	}
	return host
}
