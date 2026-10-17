package server

import (
	"encoding/json"
	"net/http"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// appearancePayload is the wire shape for the machine-wide UI preferences.
type appearancePayload struct {
	Mode string `json:"mode"`
}

// handleGetAppearance reports the stored theme mode. The setting is global (one
// machine-wide choice), so it is read from the global config DB.
func (s *Server) handleGetAppearance(w http.ResponseWriter, r *http.Request) {
	mode, err := session.GetThemeMode(s.globalDB)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read appearance"})
		return
	}
	writeJSON(w, http.StatusOK, appearancePayload{Mode: mode})
}

// handleSetAppearance stores the theme mode, normalising it to a known value.
func (s *Server) handleSetAppearance(w http.ResponseWriter, r *http.Request) {
	var req appearancePayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	mode := session.NormalizeThemeMode(req.Mode)
	if err := session.SetThemeMode(s.globalDB, mode); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save appearance"})
		return
	}
	writeJSON(w, http.StatusOK, appearancePayload{Mode: mode})
}
