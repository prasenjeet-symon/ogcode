package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/board"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// handleListPermissions returns all pending (unanswered) permission requests for
// a session. The UI calls this when switching to a session to restore any
// approval prompts that were dropped from view while another session was open —
// the agent loop is still blocked on each one. A teammate's requests are
// included: they are surfaced in the session the human is watching, so opening
// the teammate's own page is not required to answer one.
func (s *Server) handleListPermissions(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	var reqs []permission.Request
	if s.permissions != nil {
		reqs = s.permissions.PendingForSession(sessionID)
		reqs = append(reqs, s.pendingTeammatePermissions(sessionID)...)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reqs)
}

// pendingTeammatePermissions returns the requests raised by every teammate of
// this session, at any nesting depth. Each carries the teammate's handle so the
// prompt can say whose approval is pending; the request's own SessionID stays
// the teammate's, because that is the loop blocked on the reply.
//
// Known parity hole, identical on the question mirror
// (pendingTeammateQuestions): when a reply lands, only the reply's path
// session, its owner and the owner's root refetch this subtree. Pages beyond
// those three that already GET-fetched it keep their stale copy until the
// reply 404s there or the page reloads — a reply is delivered by permissionID
// and clears the path, owner and root queues only.
func (s *Server) pendingTeammatePermissions(sessionID string) []permission.Request {
	if s.permissions == nil {
		return nil
	}
	root, children, ok := s.teammateChildrenOf(sessionID)
	if !ok {
		return nil
	}
	var out []permission.Request
	queue := []board.Handle{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range children[cur.Handle] {
			for _, req := range s.permissions.PendingForSession(child.SessionID) {
				if req.Teammate == "" {
					req.Teammate = child.Handle
				}
				out = append(out, req)
			}
			queue = append(queue, child)
		}
	}
	return out
}

// handlePermissionReply handles user responses to permission requests
func (s *Server) handlePermissionReply(w http.ResponseWriter, r *http.Request) {
	sessionID := session.SessionID(chi.URLParam(r, "sessionID"))
	permID := permission.PermissionID(chi.URLParam(r, "permissionID"))

	var input struct {
		Response string `json:"response"` // "once", "always", "reject"
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if s.permissions == nil {
		http.Error(w, "permission request not found", http.StatusNotFound)
		return
	}
	// The request names the session whose loop is blocked, which is not this path
	// when a teammate's prompt is answered from its parent's composer. Read it
	// before Reply, which drops the pending entry.
	owner := ""
	if pr := s.permissions.Get(permID); pr != nil {
		owner = pr.Request.SessionID
	}

	// Deliver the reply to the waiting agent loop. Reply returns false when the
	// request is unknown (already answered, cancelled, or expired) — surface that
	// as 404 so the client can drop its stale prompt.
	if !s.permissions.Reply(permID, input.Response) {
		http.Error(w, "permission request not found", http.StatusNotFound)
		return
	}

	// Every queue holding this prompt must be cleared, whichever page answered it:
	// the session in the path, the session whose loop was blocked, and the
	// user-facing root the loop mirrored the prompt to for a teammate.
	notified := make(map[session.SessionID]bool, 3)
	notify := func(id session.SessionID) {
		if id == "" || notified[id] {
			return
		}
		notified[id] = true
		s.bus.Publish("permission.replied", map[string]string{
			"sessionId":    string(id),
			"permissionId": string(permID),
			"response":     input.Response,
		})
	}
	notify(sessionID)
	if owner != "" {
		notify(session.SessionID(owner))
		if parent, _ := s.parentSessionOf(session.SessionID(owner)); parent != "" {
			notify(parent)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
