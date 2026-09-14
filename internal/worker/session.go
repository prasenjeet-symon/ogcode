package worker

import (
	"sync"
)

// hostedSession tracks one running agent session so the worker can stop it on
// StopAgent / AbortSession or on shutdown. The loop itself, its gating and its
// guidance channel live in the worktree's server (see servers.go); stop is the
// server's HostSession cancel func for this session.
type hostedSession struct {
	id   string
	dir  string
	stop func()
}

// sessionRegistry is the worker's per-session bookkeeping — which sessions are
// hosted here and in which directory (the join key to the right worktree
// server, where all loop control lives).
type sessionRegistry struct {
	mu       sync.Mutex
	sessions map[string]*hostedSession
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{sessions: map[string]*hostedSession{}}
}

func (r *sessionRegistry) add(s *hostedSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.id] = s
}

func (r *sessionRegistry) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
}

func (r *sessionRegistry) cancel(id string) bool {
	r.mu.Lock()
	s, ok := r.sessions[id]
	r.mu.Unlock()
	if !ok {
		return false
	}
	s.stop()
	return true
}

// has reports whether a session is hosted here.
func (r *sessionRegistry) has(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.sessions[id]
	return ok
}

// dirOf returns the workspace directory hosting a session, or "" when the
// session isn't hosted here.
func (r *sessionRegistry) dirOf(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.sessions[id]; ok {
		return s.dir
	}
	return ""
}

// sessionInDir returns one session hosted in dir, or ("", false) when none
// is. Several sessions can share a worktree directory; any one of them is
// reason enough to refuse removing it.
func (r *sessionRegistry) sessionInDir(dir string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, s := range r.sessions {
		if s.dir == dir {
			return id, true
		}
	}
	return "", false
}

func (r *sessionRegistry) cancelAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.sessions {
		s.stop()
	}
}
