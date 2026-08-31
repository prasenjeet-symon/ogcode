package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// handleListPermissions returns all pending (unanswered) permission requests for
// a session. The UI calls this when switching to a session to restore any
// approval prompts that were dropped from view while another session was open —
// the agent loop is still blocked on each one.
func (s *Server) handleListPermissions(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	var reqs []permission.Request
	if s.permissions != nil {
		reqs = s.permissions.PendingForSession(sessionID)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reqs)
}

// handlePermissionReply handles user responses to permission requests
func (s *Server) handlePermissionReply(w http.ResponseWriter, r *http.Request) {
	sessionID := session.SessionID(chi.URLParam(r, "sessionID"))
	permID := chi.URLParam(r, "permissionID")

	var input struct {
		Response string `json:"response"` // "once", "always", "reject"
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Deliver the reply to the waiting agent loop. Reply returns false when the
	// request is unknown (already answered, cancelled, or expired) — surface that
	// as 404 so the client can drop its stale prompt.
	if s.permissions == nil || !s.permissions.Reply(permission.PermissionID(permID), input.Response) {
		http.Error(w, "permission request not found", http.StatusNotFound)
		return
	}

	s.bus.Publish("permission.replied", map[string]string{
		"sessionId":    string(sessionID),
		"permissionId": permID,
		"response":     input.Response,
	})

	w.WriteHeader(http.StatusNoContent)
}
