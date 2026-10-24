// Package board is the per-project roster of agents the build agent shares with
// the teammates it spawns.
//
// A row is one agent: its handle, the session it runs in, whether it is a
// session or a teammate, the handle that spawned it, and its status. The
// spawned_by edges make the rows a tree — the project's running agents and who
// spawned whom — which is the whole of what the board is for. There is no post
// log: agents coordinate by editing files, and a child reports to its parent by
// finishing.
//
// The package deliberately depends on nothing but database/sql and the project
// database: the agent loop, the server and the tools all import it, so an
// import in the other direction would be a cycle. The server wires the
// behaviour that needs those packages in through function fields.
package board

import (
	"time"
)

const (
	// UserHandle is the project owner's handle. The user posts nothing to the
	// board, but the reserved name keeps an agent from taking it.
	UserHandle = "user"
	// SystemHandle names board-generated rows. Reserved for the same reason.
	SystemHandle = "system"
)

// ReservedHandles may not be registered or spawned: they name the project
// owner, the board itself, and board-generated entries.
var ReservedHandles = map[string]bool{
	UserHandle:   true,
	SystemHandle: true,
	"board":      true,
}

// Handle kinds: a handle is either a session (the build agent, a resumed
// session), a teammate spawned by one, or a background task child a session's
// task tool delegated to.
const (
	KindSession  = "session"
	KindTeammate = "teammate"
	KindTask     = "task"
)

// Handle statuses. "stale" is set at startup for a handle whose session is no
// longer alive; "stopped" is a teammate that was stopped deliberately. done,
// stopped and stale are terminal: nothing will move the row again, and the
// spawner is owed one report about it.
const (
	StatusIdle    = "idle"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusStopped = "stopped"
	StatusStale   = "stale"
)

// Handle is one agent on a project's board.
type Handle struct {
	ID        string `json:"id"`
	Directory string `json:"directory,omitempty"`
	Handle    string `json:"handle"`
	SessionID string `json:"sessionId,omitempty"`
	Role      string `json:"role,omitempty"`
	Kind      string `json:"kind,omitempty"`
	// SpawnedBy is the parent's handle; empty for a session that joined on its
	// own. It is the tree edge.
	SpawnedBy string    `json:"spawnedBy,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// notifiedAt is when the spawner was last told this row's status, which is
	// what keeps a completion from being reported twice. It is bookkeeping, not
	// state anyone reads, so it stays off the wire.
	notifiedAt time.Time
}

// Terminal reports whether nothing will move this handle's status again.
func (h Handle) Terminal() bool {
	switch h.Status {
	case StatusDone, StatusStopped, StatusStale:
		return true
	}
	return false
}

// Unreported reports whether this handle's status is news to its spawner: it is
// terminal, and it changed after the spawner was last told.
func (h Handle) Unreported() bool {
	return h.Terminal() && h.UpdatedAt.After(h.notifiedAt)
}
