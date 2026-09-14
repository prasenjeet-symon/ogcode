package server

import (
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// HostSession hosts an agent loop for a session the master minted, writing the
// session, message and part rows this server's store needs to run it.
//
// It is the in-process equivalent of what a worker used to do by hand
// (createSessionRows + RunLoop under LoopControl + permission gating) and of
// what the UI does over HTTP (create session + prompt). One path, not three:
// the loop it starts is the same startSessionLoop the prompt and resume
// handlers use, so hosted sessions are gated, cancellable and guided exactly
// like sessions driven from a browser.
//
// The returned stop func cancels the loop (abort semantics — it does not sweep
// orphaned assistant messages or tool parts; the old worker cancel did not
// either). existed reports whether the session row was already present, which
// makes re-delivery of the same StartAgent command idempotent.
func (s *Server) HostSession(id session.SessionID, dir, prompt, agentName string, viewportWidth, viewportHeight int) (stop func(), existed bool, err error) {
	if agentName == "" {
		agentName = "build"
	}

	existing, err := s.store.Get(id)
	if err != nil {
		return nil, false, err
	}
	// A second delivery of the same StartAgent (master retry, or rows persisted
	// by an earlier run) must not duplicate the user message — stop after
	// re-registering a stop func for the loop it may still be running.
	if existing != nil {
		return func() { s.cancelLoop(id) }, true, nil
	}

	sess := &session.Session{
		ID:          id,
		ProjectID:   dir,
		Directory:   dir,
		Title:       truncateTitle(prompt, 60),
		Model:       "",
		SessionType: agentName,
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := s.store.Create(sess); err != nil {
		return nil, false, err
	}
	s.bus.Publish("session.created", sess)

	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: id,
		Role:      session.RoleUser,
		Agent:     agentName,
		CreatedAt: session.Now(),
	}
	if err := s.store.CreateMessage(userMsg); err != nil {
		return nil, true, err
	}
	textData, _ := json.Marshal(session.TextPartData{Text: prompt})
	userPart := &session.Part{
		ID:        session.NewPartID(),
		MessageID: userMsg.ID,
		SessionID: id,
		Type:      session.PartText,
		Data:      textData,
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := s.store.CreatePart(userPart); err != nil {
		return nil, true, err
	}
	s.bus.Publish("message.updated", userMsg)

	s.startSessionLoop(id, agentName, viewportWidth, viewportHeight)
	return func() { s.cancelLoop(id) }, false, nil
}

// cancelLoop cancels a running loop registered by startSessionLoop, if any.
// It is the plain cancel half of the abort handler — no message sweeping.
func (s *Server) cancelLoop(id session.SessionID) {
	s.mu.Lock()
	cancel, ok := s.running[id]
	s.mu.Unlock()
	if ok {
		cancel()
	}
}

// Guidance injects mid-loop guidance into a running loop: the text is queued
// on the loop's control and, when cancelTool is set, the in-flight LLM stream
// and tool execution are cancelled so the loop advances to the next iteration
// and drains the queue. PushGuidance runs before CancelStream/CancelTool —
// ordering matters, see handleGuidance.
//
// It returns an error when no loop is running for the session.
func (s *Server) Guidance(id session.SessionID, content string, cancelTool bool) error {
	s.mu.Lock()
	lc, ok := s.loopControls[id]
	s.mu.Unlock()
	if !ok || lc == nil {
		return errNoRunningLoop
	}

	if content != "" {
		lc.PushGuidance(content)
	}
	if cancelTool {
		lc.CancelStream()
		lc.CancelTool()
	}

	slog.Info("mid-loop guidance received", "session", id, "len", len(content), "cancelTool", cancelTool)
	s.bus.Publish("loop.guidance", map[string]string{
		"sessionId": string(id),
		"status":    "queued",
	})
	return nil
}

// ReplyPermission delivers a permission reply to the waiting agent loop. It
// reports false when the request is unknown (already answered, cancelled, or
// expired).
func (s *Server) ReplyPermission(id session.SessionID, permID, response string) bool {
	if s.permissions == nil {
		return false
	}
	ok := s.permissions.Reply(permission.PermissionID(permID), response)
	if ok {
		s.bus.Publish("permission.replied", map[string]string{
			"sessionId":    string(id),
			"permissionId": permID,
			"response":     response,
		})
	}
	return ok
}

// SubscribeEvents exposes the server's event bus to the worker, which relays
// hosted-session events to the master. The returned func unsubscribes.
func (s *Server) SubscribeEvents() (<-chan bus.Event, func()) {
	ch := s.bus.SubscribeAll()
	return ch, func() { s.bus.Unsubscribe(ch) }
}

// SessionRow returns the session row from this server's own store, or nil when
// absent. It exists so the worker (and tests) can verify a hosted session
// landed in the worktree server's DB without reaching into unexported fields.
func (s *Server) SessionRow(id session.SessionID) (*session.Session, error) {
	return s.store.Get(id)
}

// SessionPort returns the loopback port the server answers on, or 0 before the
// listener has bound.
func (s *Server) SessionPort() int {
	return int(s.port.Load())
}

// truncateTitle shortens a prompt to a session title, matching the worker's
// old createSessionRows behavior.
func truncateTitle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// errNoRunningLoop is the error text Guidance reports — the same string the
// HTTP handler returns as a 409.
var errNoRunningLoop = errors.New("no running agent loop for this session")
