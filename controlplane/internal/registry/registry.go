// Package registry holds the master's live view of connected workers and the
// routing table from global session id to the worker hosting it. It is the only
// state the master keeps about sessions — everything else (the session DB,
// pending permissions, the agent loop) lives on the worker.
package registry

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
)

// Status is a worker's liveness as seen by the master.
type Status string

const (
	// StatusOnline: a WorkerStream is attached and heartbeats are current.
	StatusOnline Status = "online"
	// StatusOffline: registered but no stream attached (e.g. between reconnects).
	StatusOffline Status = "offline"
	// StatusDead: missed the heartbeat timeout; its sessions have been failed.
	StatusDead Status = "dead"
)

// Workspace is a hostable directory reported by a worker.
type Workspace struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Branch  string `json:"branch"`
	Present bool   `json:"present"`
}

// Conn is the master's send side of an attached WorkerStream. The master's
// stream handler implements it; the registry only needs to push commands down.
type Conn interface {
	Send(*cpv1.MasterToWorker) error
}

// WorkerInfo is a read-only snapshot of a worker (no live connection handle).
type WorkerInfo struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Capabilities []string    `json:"capabilities"`
	Workspaces   []Workspace `json:"workspaces"`
	LastSeen     time.Time   `json:"lastSeen"`
	Status       Status      `json:"status"`
}

type worker struct {
	info  WorkerInfo
	token pairing.Token
	conn  Conn // nil when no stream is attached
}

// Registry is the concurrency-safe worker + session-routing store. The maps are
// the read path; a bbolt store, when configured, is the write-behind: every
// mutation is written through inside the same mutex that changed the map.
type Registry struct {
	mu         sync.RWMutex
	workers    map[string]*worker // workerID -> worker
	tokenIndex map[string]string  // token value -> workerID
	sessions   map[string]string  // sessionID -> workerID
	store      *Store             // nil = in-memory only (tests)
	logger     *slog.Logger       // nil = slog.Default()
}

// New returns an empty, in-memory-only Registry.
func New() *Registry {
	return &Registry{
		workers:    map[string]*worker{},
		tokenIndex: map[string]string{},
		sessions:   map[string]string{},
	}
}

// NewWithStore returns an empty Registry that writes every mutation through to
// st. Write-back is best-effort: a store failure is logged, never returned, so
// a hiccup on the durable side never tears down the live routing table.
func NewWithStore(st *Store, logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	r := New()
	r.store = st
	r.logger = logger
	return r
}

// Restore replaces the in-memory maps with a persisted snapshot, applied as a
// live-state reset so a restart never looks like a mass worker death. Restored
// workers come back Offline with LastSeen = boot; calling ReapStale with now =
// boot immediately afterwards reaps nothing. conn restores nil (there is no
// live stream to attach).
func (r *Registry) Restore(snap *Snapshot, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, rec := range snap.Workers {
		rec.Info.Status = StatusOffline
		rec.Info.LastSeen = now // boot time, not the persisted (past) timestamp
		r.workers[id] = &worker{info: rec.Info, token: rec.Token}
	}
	r.tokenIndex = snap.Tokens
	r.sessions = snap.Sessions
}

// Store returns the configured bbolt store, or nil for an in-memory registry.
func (r *Registry) Store() *Store { return r.store }

// Add registers (or replaces) a worker at Register time with its first token.
// The worker starts Offline until a WorkerStream attaches via Attach. A
// replacement re-keys the token index and deletes the replaced token's entry;
// it never touches `sessions`, so a re-pairing worker keeps its session
// routes. Both effects are written through to the store atomically.
func (r *Registry) Add(id, name string, caps []string, ws []Workspace, token pairing.Token, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var oldToken string
	if existing, ok := r.workers[id]; ok {
		oldToken = existing.token.Value
		delete(r.tokenIndex, existing.token.Value)
	}
	r.workers[id] = &worker{
		info: WorkerInfo{
			ID:           id,
			Name:         name,
			Capabilities: caps,
			Workspaces:   ws,
			LastSeen:     now,
			Status:       StatusOffline,
		},
		token: token,
	}
	r.tokenIndex[token.Value] = id
	if r.store != nil {
		r.storeError(r.store.ReplaceWorker(id, workerRecord{
			Info:  r.workers[id].info,
			Token: token,
		}, oldToken))
	}
}

// WorkerIDForToken resolves a token to its worker id (map lookup; tokens are
// high-entropy random values). Reports false for unknown/rotated-away tokens.
func (r *Registry) WorkerIDForToken(token string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.tokenIndex[token]
	return id, ok
}

// UpdateToken rotates a worker's token, keeping the token index consistent. The
// old token's index entry is deleted and the new one written, both through to
// the store.
func (r *Registry) UpdateToken(id string, token pairing.Token) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workers[id]
	if !ok {
		return false
	}
	oldToken := w.token.Value
	delete(r.tokenIndex, w.token.Value)
	w.token = token
	r.tokenIndex[token.Value] = id
	if r.store != nil {
		r.storeError(r.store.UpdateToken(id, oldToken, token))
	}
	return true
}

// Token returns a worker's current token.
func (r *Registry) Token(id string) (pairing.Token, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.workers[id]
	if !ok {
		return pairing.Token{}, false
	}
	return w.token, true
}

// Attach binds a live WorkerStream connection to a worker and marks it Online.
// A previously-attached conn is replaced (last stream wins).
func (r *Registry) Attach(id string, conn Conn, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workers[id]
	if !ok {
		return fmt.Errorf("attach: unknown worker %q", id)
	}
	w.conn = conn
	w.info.Status = StatusOnline
	w.info.LastSeen = now
	return nil
}

// Detach clears a worker's connection and marks it Offline (not Dead — a clean
// disconnect may be a reconnect in flight; the heartbeat reaper decides death).
func (r *Registry) Detach(id string, conn Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workers[id]
	if !ok {
		return
	}
	// Only detach if this is still the current conn (avoid a late Detach from an
	// old stream clobbering a fresh reconnect).
	if w.conn != conn {
		return
	}
	w.conn = nil
	if w.info.Status == StatusOnline {
		w.info.Status = StatusOffline
	}
}

// Touch updates LastSeen (and restores Online if the conn is live) on heartbeat.
func (r *Registry) Touch(id string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.workers[id]
	if !ok {
		return false
	}
	w.info.LastSeen = now
	if w.conn != nil {
		w.info.Status = StatusOnline
	}
	return true
}

// SetWorkspaces replaces a worker's advertised workspaces.
func (r *Registry) SetWorkspaces(id string, ws []Workspace) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.workers[id]; ok {
		w.info.Workspaces = ws
	}
}

// SendCommand pushes a command down a worker's attached stream. It errors if the
// worker is unknown or has no live stream.
func (r *Registry) SendCommand(id string, cmd *cpv1.MasterToWorker) error {
	r.mu.RLock()
	w, ok := r.workers[id]
	var conn Conn
	if ok {
		conn = w.conn
	}
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("send: unknown worker %q", id)
	}
	if conn == nil {
		return fmt.Errorf("send: worker %q is offline", id)
	}
	return conn.Send(cmd)
}

// Get returns a snapshot of one worker.
func (r *Registry) Get(id string) (WorkerInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.workers[id]
	if !ok {
		return WorkerInfo{}, false
	}
	return w.info, true
}

// List returns snapshots of all workers.
func (r *Registry) List() []WorkerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]WorkerInfo, 0, len(r.workers))
	for _, w := range r.workers {
		out = append(out, w.info)
	}
	return out
}

// --- Session routing --------------------------------------------------------

// RouteSession records that sessionID is hosted by workerID.
func (r *Registry) RouteSession(sessionID, workerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[sessionID] = workerID
	if r.store != nil {
		r.storeError(r.store.RouteSession(sessionID, workerID))
	}
}

// UnrouteSession forgets a session's routing (on stop/finish).
func (r *Registry) UnrouteSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, sessionID)
	if r.store != nil {
		r.storeError(r.store.UnrouteSession(sessionID))
	}
}

// storeError logs a write-back failure. Write-through is best-effort by
// design: the in-memory maps are the source of truth for the running process,
// and a durable-store hiccup must not break live routing. A failed write only
// loses durability across a restart, which a later write or restore heals.
func (r *Registry) storeError(err error) {
	if err != nil {
		r.logger.Error("registry write-through to bbolt failed", "err", err)
	}
}

// WorkerForSession resolves which worker hosts a session.
func (r *Registry) WorkerForSession(sessionID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.sessions[sessionID]
	return id, ok
}

// AllSessions returns a snapshot of the session routing table (sessionID ->
// workerID), copied so callers can range over it without holding the lock.
func (r *Registry) AllSessions() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.sessions))
	for sid, wid := range r.sessions {
		out[sid] = wid
	}
	return out
}

// SessionsForWorker lists the sessions routed to a worker.
func (r *Registry) SessionsForWorker(workerID string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for sid, wid := range r.sessions {
		if wid == workerID {
			out = append(out, sid)
		}
	}
	return out
}

// ReapStale marks workers whose LastSeen is older than timeout as Dead and
// returns the dead workers' ids together with the sessions that were routed to
// them (so the caller can fail those sessions with a clear finish reason). A
// worker is only reaped once: already-Dead workers are skipped.
func (r *Registry) ReapStale(timeout time.Duration, now time.Time) (deadWorkers []string, orphanedSessions []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, w := range r.workers {
		if w.info.Status == StatusDead {
			continue
		}
		if now.Sub(w.info.LastSeen) <= timeout {
			continue
		}
		w.info.Status = StatusDead
		w.conn = nil
		deadWorkers = append(deadWorkers, id)
		for sid, wid := range r.sessions {
			if wid == id {
				orphanedSessions = append(orphanedSessions, sid)
			}
		}
	}
	return deadWorkers, orphanedSessions
}
