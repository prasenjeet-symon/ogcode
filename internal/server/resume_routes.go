package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// resumableSessionTypes are the session kinds a resume is offered for.
//
// These are the interactive ones — a person is present, sees the turn fail, and
// decides whether picking it up is worth it. The headless kinds are deliberately
// out: a task runs in a worktree that is torn down on failure, and an index or
// note run is short and cheap to start over, so resuming either would need
// answers about lifecycle that the button does not have.
var resumableSessionTypes = map[string]bool{
	"":      true, // the default interactive build session
	"build": true,
	"plan":  true,
}

// handleResumeSession restarts the agent loop on a session whose last turn was
// cut short, without asking the user to type anything.
//
// The work of deciding what that means lives in the agent package, because it
// is the same work whether the trigger is this endpoint or a crash recovered at
// startup. This handler is the door: it checks the session is one resume is
// offered for, checks a loop is not already running on it, and then starts the
// loop exactly the way a prompt would.
func (s *Server) handleResumeSession(w http.ResponseWriter, r *http.Request) {
	sessionID := session.SessionID(chi.URLParam(r, "sessionID"))

	sess, err := s.store.Get(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if !resumableSessionTypes[sess.SessionType] {
		writeResumeResult(w, http.StatusBadRequest, resumeResponse{
			Resumed: false,
			Message: "This session type does not support resume.",
		})
		return
	}

	// A loop already running is not a session to resume — it is one to leave
	// alone. Resuming it would start a second loop on the same history and the
	// two would write over each other.
	s.mu.Lock()
	_, alreadyRunning := s.running[sessionID]
	s.mu.Unlock()
	if alreadyRunning {
		writeResumeResult(w, http.StatusConflict, resumeResponse{
			Resumed: false,
			Message: "This session is already running.",
		})
		return
	}

	resumable, err := s.loopRunner.PrepareResume(sessionID)
	if err != nil {
		slog.Error("prepare resume", "session", sessionID, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !resumable {
		writeResumeResult(w, http.StatusOK, resumeResponse{
			Resumed: false,
			Message: "There is nothing to resume — the last turn finished normally.",
		})
		return
	}

	agentName := sess.SessionType
	if agentName == "" {
		agentName = "build"
	}
	s.startSessionLoop(sessionID, agentName, 0, 0)

	writeResumeResult(w, http.StatusAccepted, resumeResponse{Resumed: true})
}

type resumeResponse struct {
	Resumed bool   `json:"resumed"`
	Message string `json:"message,omitempty"`
}

func writeResumeResult(w http.ResponseWriter, status int, body resumeResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// recoverInterruptedSessions reconciles every interactive session whose last
// turn was left unfinished by a process that is no longer running.
//
// It exists because a crash writes nothing. Every other failure path records
// why it stopped on the way out; a killed process does not get the chance, and
// what it leaves behind is a turn with no finish reason and possibly a tool
// call nothing answered — which makes the session's *next* request invalid,
// whether that request comes from a resume or from the user typing again.
//
// It only repairs. Restarting a loop is left to the person, which is the whole
// point of a manual resume: coming back to a server that quietly resumed a
// dozen conversations while nobody was watching is not a recovery, it is a
// surprise.
func (s *Server) recoverInterruptedSessions() {
	sessions, err := s.store.ListAll()
	if err != nil {
		slog.Warn("resume recovery: list sessions", "err", err)
		return
	}
	recovered := 0
	for _, sess := range sessions {
		if !resumableSessionTypes[sess.SessionType] {
			continue
		}
		target, err := s.loopRunner.ReconcileSession(sess.ID)
		if err != nil {
			slog.Warn("resume recovery: reconcile", "session", sess.ID, "err", err)
			continue
		}
		if target != nil {
			recovered++
		}
	}
	if recovered > 0 {
		slog.Info("recovered interrupted sessions; they can be resumed from the UI", "count", recovered)
	}
}

// startSessionLoop starts the agent loop for a session in the background with
// the same context, gating and bookkeeping a prompt would set up.
//
// It is shared by the prompt handler and the resume handler so a resumed loop
// is not a second, subtly different way of running one — a resume that skipped
// permission gating, or that failed to register its cancel func, would be a
// loop the user could neither approve tools in nor stop.
func (s *Server) startSessionLoop(sessionID session.SessionID, agentName string, viewportWidth, viewportHeight int) {
	ctx, cancel := context.WithCancel(context.Background())
	// A LoopControl lets the user inject mid-loop guidance and cancel in-flight
	// tools without killing the loop.
	lc := agent.NewLoopControl()
	ctx = agent.WithLoopControl(ctx, lc)
	// This is an interactive session with a UI that can answer permission
	// prompts, so mark it gated: mutating tools (bash/write/edit) will pause for
	// approval. Headless loops (task/breakdown/note/search) never set this flag.
	ctx = agent.WithPermissionGating(ctx)

	s.mu.Lock()
	if old, ok := s.running[sessionID]; ok {
		old()
		slog.Info("cancelled previous running loop", "session", sessionID)
	}
	s.nextToken++
	token := s.nextToken
	s.running[sessionID] = cancel
	s.runningToken[sessionID] = token
	s.loopControls[sessionID] = lc
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			if s.runningToken[sessionID] == token {
				delete(s.running, sessionID)
				delete(s.runningToken, sessionID)
				delete(s.loopControls, sessionID)
			}
			s.mu.Unlock()
		}()
		if err := s.loopRunner.RunLoop(ctx, sessionID, agentName, viewportWidth, viewportHeight); err != nil {
			slog.Error("agent loop error", "session", sessionID, "err", err)
		}
	}()
}
