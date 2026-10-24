package server

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjeet-symon/ogcode/internal/board"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// boardRosterRow is one roster row on the wire: the board handle flattened to
// the top level plus the title and model of the session it belongs to. The
// drawer labels an agent by what it is doing, and the board row carries no
// title — only the session table does — so the handler joins the two columns
// in. Embedding keeps the existing wire fields at the top level, so a client
// decoding board.Handle alone is unaffected.
type boardRosterRow struct {
	board.Handle
	Title string `json:"title,omitempty"`
	Model string `json:"model,omitempty"`
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
		if s.store != nil && h.SessionID != "" {
			if sess, err := s.store.Get(session.SessionID(h.SessionID)); err == nil && sess != nil {
				row.Title = sess.Title
				row.Model = sess.Model
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
