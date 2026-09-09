package memfile

import "sync"

// Manager is the barrier between background turn-summary work and recall. A turn
// summary is synthesized, written, and indexed on a detached goroutine, so a
// recall that fires immediately afterwards could otherwise read a stale or
// missing index. Recall calls Wait(project) first; the background job brackets
// itself with Begin/Done, so Wait blocks until the project's in-flight summaries
// have all landed in the index.
//
// The counter is keyed by project so a summary in one workspace never blocks
// recall in another. A sync.Cond (not a per-project WaitGroup) is used because
// WaitGroup forbids Add concurrent with Wait — exactly the pattern here, where a
// new turn can begin while a recall is waiting.
type Manager struct {
	mu       sync.Mutex
	cond     *sync.Cond
	inflight map[string]int
}

// NewManager returns a ready Manager.
func NewManager() *Manager {
	m := &Manager{inflight: make(map[string]int)}
	m.cond = sync.NewCond(&m.mu)
	return m
}

// Begin records that a background summary for project has started. Call it
// synchronously, before launching the goroutine, so a recall that follows the
// turn immediately observes the in-flight work.
func (m *Manager) Begin(project string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.inflight[project]++
	m.mu.Unlock()
}

// Done records that a background summary for project has finished (file written
// and indexed). It wakes any waiter.
func (m *Manager) Done(project string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.inflight[project] > 0 {
		m.inflight[project]--
	}
	if m.inflight[project] == 0 {
		delete(m.inflight, project)
	}
	m.cond.Broadcast()
	m.mu.Unlock()
}

// Wait blocks until no background summary for project is in flight.
func (m *Manager) Wait(project string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	for m.inflight[project] > 0 {
		m.cond.Wait()
	}
	m.mu.Unlock()
}
