package server

import (
	"context"
	"log/slog"
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

// sessionTeammateUsage is one teammate's priced spend, for the pill's
// per-teammate rows.
type sessionTeammateUsage struct {
	Handle    string       `json:"handle"`
	SessionID string       `json:"sessionId"`
	Status    string       `json:"status"`
	Totals    usage.Totals `json:"totals"`
}

// sessionUsage is one session's spend plus its teammates'.
type sessionUsage struct {
	Models    []usage.ModelUsage     `json:"models"`
	Totals    usage.Totals           `json:"totals"`
	Teammates []sessionTeammateUsage `json:"teammates"`
}

// handleSessionUsage prices one session by the model each step ran on, for the
// token pill's cost. Its teammates are priced alongside it — a teammate is a
// real build session the board ties to this one, so its spend is its own and is
// reported as a separate list rather than added to the parent's totals.
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

	ctx, cancel := context.WithTimeout(r.Context(), boardOpTimeout)
	defer cancel()
	handles, err := s.spawnedTeammates(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	teammates := make([]sessionTeammateUsage, 0, len(handles))
	for _, h := range handles {
		childID := session.SessionID(h.SessionID)
		child, err := s.store.Get(childID)
		if err != nil || child == nil {
			// A deleted teammate keeps its roster row; its spend is gone with
			// it. A transient store error on a child's row degrades exactly
			// the same way — the roster join skips the failing row per-row
			// rather than costing the drawer every other agent — so this
			// pricing drops the child and keeps the rest of the rollup.
			if err != nil {
				slog.Warn("usage rollup: could not read a teammate, skipping it",
					"session", id, "teammate", h.Handle, "err", err)
			}
			continue
		}
		childCost, err := usage.ForSession(s.store, s.registry, childID)
		if err != nil {
			// Failing to price a child is the same per-child fault: its spend
			// is this rollup's separate field, folded in on the client, so it
			// is dropped rather than fatal — the parent's totals never rely
			// on it.
			slog.Warn("usage rollup: could not price a teammate, skipping it",
				"session", id, "teammate", h.Handle, "err", err)
			continue
		}
		teammates = append(teammates, sessionTeammateUsage{
			Handle:    h.Handle,
			SessionID: h.SessionID,
			Status:    h.Status,
			Totals:    childCost.Totals,
		})
	}

	writeJSON(w, http.StatusOK, sessionUsage{
		Models:    cost.Models,
		Totals:    cost.Totals,
		Teammates: teammates,
	})
}

// msParam reads an optional unix-millisecond query parameter; absent is 0.
func msParam(r *http.Request, name string) (int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, nil
	}
	return strconv.ParseInt(v, 10, 64)
}
