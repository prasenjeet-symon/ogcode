package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjeet-symon/ogcode/internal/board"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// boardRosterRow is one roster row on the wire: the board handle flattened to
// the top level plus the title, model and delegation hints of the session it
// belongs to. The drawer labels an agent by what it is doing, and the board row
// carries no title — only the session table does — so the handler joins the
// columns in. Complexity and workload ride along for the hover tooltip: they
// are the spawner's prediction, set on a delegated session and empty on one
// nobody delegated. Embedding keeps the existing wire fields at the top level,
// so a client decoding board.Handle alone is unaffected.
type boardRosterRow struct {
	board.Handle
	Title   string `json:"title,omitempty"`
	Model   string `json:"model,omitempty"`
	// Complexity is easy, medium or hard — the spawner's difficulty prediction.
	Complexity string `json:"complexity,omitempty"`
	// Workload is read-heavy or write-heavy — what the delegated task mostly
	// does.
	Workload string `json:"workload,omitempty"`
	// Waiting marks the agents whose loop is blocked on the human — an
	// unanswered permission request or ask_user batch. The board blinks those,
	// so the page needs the flag in the roster rather than one read per agent.
	Waiting bool `json:"waiting,omitempty"`
}

// awaitingUser reports whether the agent behind sessionID is blocked on the
// human. Only the session's own queues count: a teammate's request is mirrored
// up to its parent for display, but the blink belongs on the agent that is
// actually stuck.
func (s *Server) awaitingUser(sessionID string) bool {
	if s.permissions != nil && len(s.permissions.PendingForSession(sessionID)) > 0 {
		return true
	}
	if s.questions != nil && len(s.questions.PendingForSession(sessionID)) > 0 {
		return true
	}
	return false
}

// handleBoardRoster returns the agent tree the UI drawer shows: every handle on
// the board, with the spawned_by edges that nest them.
func (s *Server) handleBoardRoster(w http.ResponseWriter, r *http.Request) {
	if s.board == nil {
		http.Error(w, "the board is not available", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), boardOpTimeout)
	defer cancel()

	agents, err := s.board.Roster(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The join never fails the roster: a deleted session row must not cost the
	// drawer its other agents, so a missing row just shows without a title.
	rows := make([]boardRosterRow, 0, len(agents))
	for _, h := range agents {
		row := boardRosterRow{Handle: h}
		if h.SessionID != "" {
			row.Waiting = s.awaitingUser(h.SessionID)
		}
		if s.store != nil && h.SessionID != "" {
			if sess, err := s.store.Get(session.SessionID(h.SessionID)); err == nil && sess != nil {
				row.Title = sess.Title
				row.Model = sess.Model
				row.Complexity = sess.Complexity
				row.Workload = sess.Workload
			}
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": rows})
}

// handleBoardStopTeammate stops one teammate from the board drawer.
func (s *Server) handleBoardStopTeammate(w http.ResponseWriter, r *http.Request) {
	if s.board == nil {
		http.Error(w, "the board is not available", http.StatusServiceUnavailable)
		return
	}
	handle := chi.URLParam(r, "handle")
	ctx, cancel := context.WithTimeout(r.Context(), boardOpTimeout)
	defer cancel()

	h, err := s.board.Get(ctx, handle)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if h.Handle == "" {
		http.Error(w, "no such handle: "+handle, http.StatusNotFound)
		return
	}
	if h.Kind != board.KindTeammate && h.Kind != board.KindTask {
		http.Error(w, handle+" is a session, not a teammate", http.StatusBadRequest)
		return
	}
	res, err := s.stopTeammate(ctx, handle)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
