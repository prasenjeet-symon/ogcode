package server

import (
	"encoding/json"
	"net/http"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// navStatePayload is the wire shape for the folded/expanded state of the
// left-hand navigation panels. It is stored per project, so each project's
// server answers with its own.
type navStatePayload struct {
	SidebarCollapsed     bool `json:"sidebarCollapsed"`
	SettingsNavCollapsed bool `json:"settingsNavCollapsed"`
}

func navStateFrom(state session.NavState) navStatePayload {
	return navStatePayload{
		SidebarCollapsed:     state.SidebarCollapsed,
		SettingsNavCollapsed: state.SettingsNavCollapsed,
	}
}

// handleGetNavState reports which panels this project has folded. A project that
// has never folded one reports both expanded.
func (s *Server) handleGetNavState(w http.ResponseWriter, r *http.Request) {
	state, err := session.GetNavState(s.db)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read nav state"})
		return
	}
	writeJSON(w, http.StatusOK, navStateFrom(state))
}

// handleSetNavState stores a fold. Both fields are optional: one the client
// leaves out keeps its current value, so a single panel can be reported on its
// own. The full state comes back, which is what the client keeps.
func (s *Server) handleSetNavState(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SidebarCollapsed     *bool `json:"sidebarCollapsed"`
		SettingsNavCollapsed *bool `json:"settingsNavCollapsed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	state, err := session.SetNavState(s.db, req.SidebarCollapsed, req.SettingsNavCollapsed)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save nav state"})
		return
	}
	writeJSON(w, http.StatusOK, navStateFrom(state))
}
