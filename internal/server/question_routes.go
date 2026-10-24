package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/board"
	"github.com/prasenjeet-symon/ogcode/internal/question"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// handleListQuestions returns all pending (unanswered) ask_user batches for a
// session — its own, plus any raised by its teammates at any depth, the way
// teammate permission prompts are listed. The UI calls this when switching to a
// session to restore a dialog that was dropped from view while another session
// was open — the agent loop is still blocked on the reply. A teammate's batch is
// included: it is surfaced in the session the human is watching, so opening the
// teammate's own page is not required to answer it.
func (s *Server) handleListQuestions(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	var reqs []question.Request
	if s.questions != nil {
		reqs = s.questions.PendingForSession(sessionID)
		reqs = append(reqs, s.pendingTeammateQuestions(sessionID)...)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reqs)
}

// pendingTeammateQuestions returns the ask_user batches raised by every
// teammate of this session, at any nesting depth — the dialog each of their
// loops is blocked on. Each carries the teammate's handle so the dialog can say
// who is asking; the request's own SessionID stays the teammate's, because that
// is the loop the answers must unblock.
//
// Known parity hole, identical on the permission mirror
// (pendingTeammatePermissions): when a reply lands, only the reply's path
// session, its owner and the owner's root refetch this subtree. Pages beyond
// those three that already GET-fetched it keep their stale copy until the
// reply 404s there or the page reloads — a reply is delivered by questionID
// and notifies the path, owner and root only, so it is parity, not a bug on
// either side.
func (s *Server) pendingTeammateQuestions(sessionID string) []question.Request {
	root, children, ok := s.teammateChildrenOf(sessionID)
	if !ok {
		return nil
	}
	var out []question.Request
	queue := []board.Handle{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range children[cur.Handle] {
			for _, req := range s.questions.PendingForSession(child.SessionID) {
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

// teammateChildrenOf primes the walk over the agent tree from the board handle
// this session holds: the map of each roster agent's teammate children and the
// root handle to start the BFS from. Both pendingTeammatePermissions and
// pendingTeammateQuestions scan the same tree, so the walk's shape — and the
// guard clauses around it — live here once. ok names whether a board is wired
// and holds this session's handle; callers bail out silently when it is not.
// Both mirrors carry the same documented staleness hole on pages beyond the
// reply's three refreshed sessions — see their comments before extending one.
func (s *Server) teammateChildrenOf(sessionID string) (root board.Handle, children map[string][]board.Handle, ok bool) {
	if s.board == nil {
		return board.Handle{}, nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
	defer cancel()
	h, err := s.board.BySession(ctx, sessionID)
	if err != nil || h.Handle == "" {
		return board.Handle{}, nil, false
	}
	roster, err := s.board.Roster(ctx)
	if err != nil {
		return board.Handle{}, nil, false
	}
	children = make(map[string][]board.Handle, len(roster))
	for _, a := range roster {
		if a.Kind == board.KindTeammate && a.SpawnedBy != "" && a.SessionID != "" {
			children[a.SpawnedBy] = append(children[a.SpawnedBy], a)
		}
	}
	return h, children, true
}

// handleReplyQuestion handles the user's answers to an ask_user batch. A
// teammate's dialog is answered from its parent's page too (the batch mirrors
// there), so every queue holding it must be told it resolved: the session in
// the path, the session whose loop was blocked, and the user-facing root the
// batch mirrored to.
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
	if s.questions == nil {
		http.Error(w, "question request not found", http.StatusNotFound)
		return
	}

	// The request names the session whose loop is blocked, which is not this
	// path when a teammate's dialog is answered from its parent's composer.
	// Read it before Reply, which drops the pending entry.
	owner := ""
	if pr := s.questions.Get(question.QuestionID(questionID)); pr != nil {
		owner = pr.Request.SessionID
	}

	// Deliver the answers to the waiting agent loop. Reply returns false when
	// the batch is unknown (already answered, cancelled, or expired) — surface
	// that as 404 so the client can drop its stale dialog.
	if !s.questions.Reply(question.QuestionID(questionID), question.Reply{Answers: input.Answers}) {
		http.Error(w, "question request not found", http.StatusNotFound)
		return
	}

	// Every queue holding this dialog must be cleared, whichever page answered
	// it: the session in the path, the session whose loop was blocked, and the
	// user-facing root the loop mirrored the batch to for a teammate.
	notified := make(map[session.SessionID]bool, 3)
	notify := func(id session.SessionID) {
		if id == "" || notified[id] {
			return
		}
		notified[id] = true
		s.bus.Publish("question.replied", map[string]string{
			"sessionId":  string(id),
			"questionId": questionID,
			"response":   "answered",
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
