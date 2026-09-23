package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/question"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// handleListQuestions returns all pending (unanswered) ask_user batches for a
// session. The UI calls this when switching to a session to restore a dialog
// that was dropped from view while another session was open — the agent loop is
// still blocked on the reply.
func (s *Server) handleListQuestions(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	var reqs []question.Request
	if s.questions != nil {
		reqs = s.questions.PendingForSession(sessionID)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reqs)
}

// handleReplyQuestion handles the user's answers to an ask_user batch.
func (s *Server) handleReplyQuestion(w http.ResponseWriter, r *http.Request) {
	sessionID := session.SessionID(chi.URLParam(r, "sessionID"))
	questionID := chi.URLParam(r, "questionID")

	var input struct {
		Answers []question.Answer `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Deliver the answers to the waiting agent loop. Reply returns false when
	// the batch is unknown (already answered, cancelled, or expired) — surface
	// that as 404 so the client can drop its stale dialog.
	if s.questions == nil || !s.questions.Reply(question.QuestionID(questionID), question.Reply{Answers: input.Answers}) {
		http.Error(w, "question request not found", http.StatusNotFound)
		return
	}

	s.bus.Publish("question.replied", map[string]string{
		"sessionId":  string(sessionID),
		"questionId": questionID,
		"response":   "answered",
	})

	w.WriteHeader(http.StatusNoContent)
}
