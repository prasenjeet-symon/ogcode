package worker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/server"
)

// serverManager hosts one standalone ogcode server per worktree directory.
//
// Phase 2 of the remote-agent-workers plan: every worktree gets a full
// server.Server wired by server.NewWithOptions + Serve(ctx), which brings its
// own DB, bus, stores, providers, permission manager, loop runner and loopback
// HTTP listener. The per-dir isolation is automatic; machine-global state
// (~/.ogcode/config.db, embed model, mcp-tokens) is designed for N-process
// sharing and needs nothing here.
type serverManager struct {
	mu      sync.Mutex
	ctx     context.Context
	servers map[string]*serverHost
	logger  *slog.Logger
}

// serverHost is one live worktree server plus its owning context.
type serverHost struct {
	srv    *server.Server
	cancel context.CancelFunc
	port   int
	// done is closed when the Serve goroutine has returned — by then the
	// server has released its listener, closed its DBs and written its last
	// byte into the worktree's .ogcode/, so a caller tearing the directory
	// down afterwards cannot race a still-running server.
	done chan struct{}
}

func newServerManager(ctx context.Context, logger *slog.Logger) *serverManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &serverManager{
		ctx:     ctx,
		servers: map[string]*serverHost{},
		logger:  logger,
	}
}

// ensure returns the server already hosting dir, or spawns one and waits until
// it answers on 127.0.0.1:<port> before returning it.
//
// The spawned server is a child of the worker's own context, so cancelling the
// worker tears every hosted server down with it.
func (m *serverManager) ensure(dir string) (*serverHost, error) {
	// Hold the manager lock across the whole spawn so two concurrent ensure()
	// calls for the same dir cannot each start a server.
	m.mu.Lock()
	defer m.mu.Unlock()

	if h, ok := m.servers[dir]; ok {
		return h, nil
	}

	srv := server.NewWithOptions(0, dir, server.Options{NoBrowser: true, Loopback: true})

	hostCtx, cancel := context.WithCancel(m.ctx)
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		if err := srv.Serve(hostCtx); err != nil {
			m.logger.Error("worktree server exited with error", "dir", dir, "err", err)
		}
	}()

	// Wait for the listener to bind and answer before handing the host out —
	// the tunnel splices accepted streams to 127.0.0.1:<port> immediately and
	// StartAgent dials nothing, but a half-up server would 502 the first UI
	// request and fail the first HostSession's loop.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			cancel()
			<-serveDone
			return nil, fmt.Errorf("timed out waiting for server on %s to bind", dir)
		}
		if port := srv.Port(); port != 0 {
			c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
			if err == nil {
				c.Close()
				h := &serverHost{srv: srv, cancel: cancel, port: port, done: serveDone}
				m.servers[dir] = h
				return h, nil
			}
		}
		select {
		case <-serveDone:
			cancel()
			return nil, fmt.Errorf("server for %s exited before binding", dir)
		case <-hostCtx.Done():
			cancel()
			return nil, fmt.Errorf("server for %s cancelled before binding", dir)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// get returns the host for dir, or nil when none is running.
func (m *serverManager) get(dir string) *serverHost {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.servers[dir]
}

// stopShutdownGrace bounds how long stopAll waits for the hosted servers to
// finish their own graceful shutdown. Serve's own HTTP grace is 3s, so this
// leaves room for the DB and MCP closes that follow it.
const stopShutdownGrace = 10 * time.Second

// stopAll cancels every hosted server's context and waits (bounded) for each
// Serve goroutine to return. Waiting matters: Serve closes the server's DBs and
// finishes writing under the worktree only on its way out, so a caller that
// deletes the directory right after stopAll — a test's TempDir cleanup — would
// otherwise race it.
func (m *serverManager) stopAll() {
	m.mu.Lock()
	hosts := make([]*serverHost, 0, len(m.servers))
	for _, h := range m.servers {
		hosts = append(hosts, h)
	}
	m.servers = map[string]*serverHost{}
	m.mu.Unlock()
	for _, h := range hosts {
		h.cancel()
	}
	deadline := time.Now().Add(stopShutdownGrace)
	for _, h := range hosts {
		select {
		case <-h.done:
		case <-time.After(time.Until(deadline)):
			m.logger.Warn("worktree server still shutting down at the grace deadline", "port", h.port)
			return
		}
	}
}
