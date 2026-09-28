package server

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/usage"
)

// handleUsageSummary totals the global spend ledger between ?from and ?to (unix
// ms, both optional; the default is all of it). ?project narrows it to one
// workspace directory, its task worktrees included; without it the total spans
// every project. It backs the Usage page in Settings.
func (s *Server) handleUsageSummary(w http.ResponseWriter, r *http.Request) {
	from, err := msParam(r, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, "from: "+err.Error())
		return
	}
	to, err := msParam(r, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, "to: "+err.Error())
		return
	}
	sum, err := usage.Summarize(s.usage.DB(), s.registry, from, to, r.URL.Query().Get("project"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// handleSessionUsage prices one session by the model each step ran on, for the
// token pill's cost.
func (s *Server) handleSessionUsage(w http.ResponseWriter, r *http.Request) {
	id := session.SessionID(chi.URLParam(r, "sessionID"))
	sess, err := s.store.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sess == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	cost, err := usage.ForSession(s.store, s.registry, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cost)
}

// msParam reads an optional unix-millisecond query parameter; absent is 0.
func msParam(r *http.Request, name string) (int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, nil
	}
	return strconv.ParseInt(v, 10, 64)
}
