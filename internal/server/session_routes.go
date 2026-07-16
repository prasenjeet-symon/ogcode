package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/note"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	directory := r.URL.Query().Get("directory")
	if directory == "" {
		directory = s.dir
	}

	sessions, err := s.store.List(directory)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sessions == nil {
		sessions = []*session.Session{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Directory string `json:"directory"`
		Model     string `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	dir := input.Directory
	if dir == "" {
		dir = s.dir
	}

	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   dir,
		Directory:   dir,
		Title:       "New session",
		Model:       input.Model,
		SessionType: "build",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}

	if err := s.store.Create(sess); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.bus.Publish("session.created", sess)
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := session.SessionID(chi.URLParam(r, "sessionID"))
	sess, err := s.store.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	id := session.SessionID(chi.URLParam(r, "sessionID"))
	sess, err := s.store.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	var update struct {
		Title      *string `json:"title"`
		Model      *string `json:"model"`
		Permission *string `json:"permission"`
	}
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if update.Title != nil {
		sess.Title = *update.Title
	}
	if update.Model != nil {
		sess.Model = *update.Model
	}
	if update.Permission != nil {
		sess.Permission = *update.Permission
	}
	sess.UpdatedAt = session.Now()

	if err := s.store.Update(sess); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.bus.Publish("session.updated", sess)
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := session.SessionID(chi.URLParam(r, "sessionID"))

	// Abort any running agent loop for this session before deleting
	s.mu.Lock()
	if cancel, ok := s.running[id]; ok {
		cancel()
		delete(s.running, id)
		delete(s.runningToken, id)
		delete(s.loopControls, id)
	}
	s.mu.Unlock()

	// Finalize any note that was being generated for this session
	if n, noteErr := s.noteStore.GetBySessionID(string(id)); noteErr == nil && n != nil && n.Status == note.StatusGenerating {
		// Collect whatever content exists in the assistant messages
		content := ""
		if msgs, msgsErr := s.store.GetMessages(id, "", 1000); msgsErr == nil {
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].Info.Role == session.RoleAssistant {
					for _, p := range msgs[i].Parts {
						if p.Type == session.PartText {
							var data session.TextPartData
							if json.Unmarshal(p.Data, &data) == nil && data.Text != "" {
								content = data.Text
							}
						}
					}
					if content != "" {
						break
					}
				}
			}
		}
		reason := "aborted"
		if finErr := s.noteStore.FinalizeBySession(string(id), content, reason); finErr != nil {
			slog.Warn("finalize note on session delete", "session", id, "err", finErr)
		} else {
			s.bus.Publish("note.updated", map[string]string{"sessionId": string(id)})
		}
	}

	if err := s.store.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.bus.Publish("session.deleted", map[string]string{"id": string(id)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAbortSession(w http.ResponseWriter, r *http.Request) {
	sessionID := session.SessionID(chi.URLParam(r, "sessionID"))

	s.mu.Lock()
	cancel, ok := s.running[sessionID]
	if ok {
		delete(s.running, sessionID)
		delete(s.runningToken, sessionID)
		delete(s.loopControls, sessionID)
	}
	s.mu.Unlock()

	if ok {
		cancel()
		slog.Info("aborted session", "session", sessionID)
	}

	// Mark the last unfinished assistant message as aborted and cancel all in-progress tool calls
	messages, err := s.store.GetMessages(sessionID, "", 100)
	if err == nil {
		abortedReason := "aborted"

		for i := len(messages) - 1; i >= 0; i-- {
			m := messages[i]

			// Mark first unfinished assistant message as aborted
			if m.Info.Role == session.RoleAssistant && m.Info.Finish == nil && m.Info.Error == nil {
				m.Info.Finish = &abortedReason
				if err := s.store.UpdateMessage(&m.Info); err != nil {
					slog.Error("update aborted message", "err", err)
				}
				slog.Info("marked message as aborted", "session", sessionID, "message", m.Info.ID)
				s.bus.Publish("message.updated", &m.Info)
			}

			// Cancel all in-progress tool calls in all messages
			if len(m.Parts) > 0 {
				for _, part := range m.Parts {
					if part.Type == session.PartTool {
						var toolData session.ToolPartData
						if err := json.Unmarshal(part.Data, &toolData); err == nil {
							// Check if tool is still running or pending
							if toolData.State.Status == session.ToolPending || toolData.State.Status == session.ToolRunning {
								cancelledErr := "Request cancelled by user"
								toolData.State.Status = session.ToolError
								toolData.State.Error = &cancelledErr
								toolData.State.Time.End = session.Now()

								updatedData, _ := json.Marshal(toolData)
								part.Data = updatedData
								part.UpdatedAt = session.Now()

								if err := s.store.UpdatePart(&part); err != nil {
									slog.Error("update cancelled tool part", "err", err)
								}
								slog.Info("cancelled tool call", "session", sessionID, "tool", toolData.Tool, "callId", toolData.CallID)
								s.bus.Publish("message.part.updated", map[string]string{
									"sessionId": string(sessionID),
									"partId":    string(part.ID),
								})
							}
						}
					}
				}
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleGuidance injects a mid-loop instruction into a running agent loop
// without starting a new user turn. The guidance text is delivered to the loop
// at the top of its next iteration via the LoopControl side-channel and
// appended to the user's turn message content (not the system prompt) — the
// model sees it as additional user input within the current turn. The guidance
// accumulates across iterations so the model continuously sees all guidance
// sent during this loop run. It is never persisted to the message DB.
// Optionally cancels the currently-running tool call so the loop can act on
// the new guidance immediately instead of waiting for the tool to finish.
func (s *Server) handleGuidance(w http.ResponseWriter, r *http.Request) {
	sessionID := session.SessionID(chi.URLParam(r, "sessionID"))

	var input struct {
		Content    string `json:"content"`
		CancelTool bool   `json:"cancelTool,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(input.Content) == "" && !input.CancelTool {
		http.Error(w, "content or cancelTool required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	lc, ok := s.loopControls[sessionID]
	s.mu.Unlock()

	if !ok || lc == nil {
		// No running loop — nothing to inject into.
		http.Error(w, "no running agent loop for this session", http.StatusConflict)
		return
	}

	// Push the guidance text FIRST, then cancel. Ordering matters: cancellation
	// is what wakes the loop and advances it to the next iteration, where it
	// drains the queue and decides whether to keep running. If we cancelled
	// before pushing, the loop could wake, drain an empty queue, see the
	// stream/tool finished as a normal "stop", and exit — silently dropping the
	// guidance that lands a moment later. PushGuidance and DrainGuidance share
	// the same mutex, so once PushGuidance returns the guidance is guaranteed
	// visible before any cancellation can propagate.
	if strings.TrimSpace(input.Content) != "" {
		lc.PushGuidance(input.Content)
	}

	// Now cancel the in-flight work so the loop can proceed to the next
	// iteration (where it drains the guidance) without waiting. This cancels
	// BOTH the LLM stream (if the model is currently generating) AND any running
	// tool execution (if tools are currently running). Without stream
	// cancellation the user has to wait for the full model generation to
	// complete — sometimes tens of seconds — before the guidance is acted on,
	// which makes the feature feel unresponsive.
	streamCancelled := false
	toolCancelled := false
	if input.CancelTool {
		streamCancelled = lc.CancelStream()
		toolCancelled = lc.CancelTool()
	}

	slog.Info("mid-loop guidance received", "session", sessionID, "len", len(input.Content), "cancelTool", input.CancelTool, "streamCancelled", streamCancelled, "toolCancelled", toolCancelled)
	s.bus.Publish("loop.guidance", map[string]string{
		"sessionId": string(sessionID),
		"status":    "queued",
	})

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetMessages(w http.ResponseWriter, r *http.Request) {
	sessionID := session.SessionID(chi.URLParam(r, "sessionID"))
	before := session.MessageID(r.URL.Query().Get("before"))
	limit := 300

	messages, err := s.store.GetMessages(sessionID, before, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if messages == nil {
		messages = []*session.MessageWithParts{}
	}
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	sessionID := session.SessionID(chi.URLParam(r, "sessionID"))

	var input struct {
		Content        string           `json:"content"`
		Images         []session.ImagePartData `json:"images,omitempty"`
		Agent          string           `json:"agent,omitempty"`
		Model          string           `json:"model,omitempty"`
		ViewportWidth  int              `json:"viewportWidth,omitempty"`
		ViewportHeight int              `json:"viewportHeight,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	sess, err := s.store.Get(sessionID)
	if err != nil || sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Update session model if provided and different
	if input.Model != "" && sess.Model != input.Model {
		sess.Model = input.Model
		sess.UpdatedAt = session.Now()
		if err := s.store.Update(sess); err != nil {
			slog.Error("update session model", "err", err)
		}
	}

	// Auto-generate session title from first message content
	// Only generate if the title is still the default "New session"
	if sess.Title == "New session" && strings.TrimSpace(input.Content) != "" {
		go s.generateTitle(sessionID, input.Content, input.Model)
	}

	// Create user message
	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: sessionID,
		Role:      session.RoleUser,
		Agent:     input.Agent,
		CreatedAt: session.Now(),
	}
	if err := s.store.CreateMessage(userMsg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Create text part for user message
	textData, _ := json.Marshal(session.TextPartData{Text: input.Content})
	userPart := &session.Part{
		ID:        session.NewPartID(),
		MessageID: userMsg.ID,
		SessionID: sessionID,
		Type:      session.PartText,
		Data:      textData,
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := s.store.CreatePart(userPart); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Create image parts for any user-uploaded images.
	for _, img := range input.Images {
		if img.Data == "" || img.MediaType == "" {
			continue
		}
		imgData, _ := json.Marshal(img)
		imgPart := &session.Part{
			ID:        session.NewPartID(),
			MessageID: userMsg.ID,
			SessionID: sessionID,
			Type:      session.PartImage,
			Data:      imgData,
			CreatedAt: session.Now(),
			UpdatedAt: session.Now(),
		}
		if err := s.store.CreatePart(imgPart); err != nil {
			slog.Error("create user image part", "err", err)
		}
	}

	s.bus.Publish("message.updated", userMsg)

	// Mark any unfinished assistant messages as aborted. These are orphans from a
	// previous loop that crashed or was interrupted before setting a finish reason
	// (e.g. server restart). Without this, the client sees an open-ended assistant
	// message and polls forever thinking the loop is still active.
	if orphans, err := s.store.GetMessages(sessionID, "", 100); err == nil {
		abortedReason := "aborted"
		for _, m := range orphans {
			if m.Info.Role == session.RoleAssistant && m.Info.Finish == nil && m.Info.Error == nil {
				m.Info.Finish = &abortedReason
				if updateErr := s.store.UpdateMessage(&m.Info); updateErr == nil {
					s.bus.Publish("message.updated", &m.Info)
				}
			}
		}
	}

	// Start agent loop in background with cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	// Create a LoopControl for this loop so the user can inject mid-loop
	// guidance and cancel in-flight tools without killing the loop.
	lc := agent.NewLoopControl()
	ctx = agent.WithLoopControl(ctx, lc)
	// Cancel any already-running loop for this session before starting a new one.
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
			// Only delete our own cancel func - a newer prompt may have
			// replaced it while we were running. Check the token to verify.
			s.mu.Lock()
			if s.runningToken[sessionID] == token {
				delete(s.running, sessionID)
				delete(s.runningToken, sessionID)
				delete(s.loopControls, sessionID)
			}
			s.mu.Unlock()
		}()
		if err := s.loopRunner.RunLoop(ctx, sessionID, input.Agent, input.ViewportWidth, input.ViewportHeight); err != nil {
			slog.Error("agent loop error", "session", sessionID, "err", err)
		}
	}()

	w.WriteHeader(http.StatusNoContent)
}

// generateTitle uses the LLM to generate a short title from the first message
// content and updates the session. Runs in a goroutine — failures are logged
// but never block the user's prompt.
func (s *Server) generateTitle(sessionID session.SessionID, firstMessage string, model string) {
	// Truncate very long messages to avoid wasting tokens
	content := firstMessage
	if len(content) > 500 {
		content = content[:500]
	}

	// Resolve the provider for the session's model
	var p provider.Provider
	if model != "" {
		p = s.registry.ResolveProvider(model)
	}
	if p == nil {
		p = s.defaultProvider
	}
	if p == nil {
		slog.Warn("generateTitle: no provider available, skipping title generation")
		return
	}

	// Use a fast model for title generation to minimize latency and cost.
	// Falls back to the session model if no fast alternative is available.
	titleModel := model
	for _, m := range p.Models() {
		// Prefer haiku-class models for title generation
		if strings.Contains(strings.ToLower(m.ID), "haiku") ||
			strings.Contains(strings.ToLower(m.ID), "mini") ||
			strings.Contains(strings.ToLower(m.ID), "flash") {
			titleModel = m.ID
			break
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	promptContent, _ := json.Marshal(
		"Generate a very short title (5-8 words max) for a conversation that starts with this message. " +
			"Respond with ONLY the title, no quotes, no punctuation at the end, no explanation. " +
			"The title should capture the main topic or intent.\n\nMessage: " + content,
	)

	req := provider.StreamRequest{
		Model:    titleModel,
		System:   []string{"You generate concise, descriptive titles. Respond with only the title text, nothing else."},
		Messages: []provider.ModelMessage{{Role: "user", Content: promptContent}},
	}

	ch, err := p.StreamChat(ctx, req)
	if err != nil {
		slog.Warn("generateTitle: stream failed", "err", err)
		return
	}

	var title strings.Builder
	for evt := range ch {
		if evt.Type == provider.EventTextDelta {
			title.WriteString(evt.Text)
		}
		if evt.Type == provider.EventError {
			slog.Warn("generateTitle: stream error", "err", evt.Error)
			return
		}
	}

	generated := strings.TrimSpace(title.String())
	// Strip surrounding quotes if the model adds them
	generated = strings.Trim(generated, "\"'`")
	if generated == "" {
		slog.Warn("generateTitle: empty title generated, skipping")
		return
	}
	// Cap title length
	if len(generated) > 100 {
		generated = generated[:100] + "…"
	}

	// Re-fetch the session to avoid stale data (title may have been manually changed)
	sess, err := s.store.Get(sessionID)
	if err != nil || sess == nil {
		slog.Warn("generateTitle: session not found", "err", err)
		return
	}
	// Only update if the title is still the default — don't overwrite a user-set title
	if sess.Title != "New session" {
		slog.Info("generateTitle: title was manually changed, skipping", "session", sessionID)
		return
	}

	sess.Title = generated
	sess.UpdatedAt = session.Now()
	if err := s.store.Update(sess); err != nil {
		slog.Error("generateTitle: failed to update session title", "err", err)
		return
	}

	s.bus.Publish("session.updated", sess)
	slog.Info("generateTitle: updated session title", "session", sessionID, "title", generated)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}