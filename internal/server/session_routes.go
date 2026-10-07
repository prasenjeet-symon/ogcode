package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
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
		Provider  string `json:"provider,omitempty"`
		Effort    string `json:"effort,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validEffort(input.Effort) {
		http.Error(w, "unknown effort level", http.StatusBadRequest)
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
		Provider:    input.Provider,
		Effort:      input.Effort,
		SessionType: "build",
		// New sessions start in the stored default mode (Ask unless the user has
		// chosen Auto somewhere). Setting it here rather than resolving it on read
		// means the session row states its own mode from the start, so a later
		// change to the default cannot move a session already in progress.
		Permission: s.permissions.DefaultMode(),
		CreatedAt:  session.Now(),
		UpdatedAt:  session.Now(),
	}

	if err := s.store.Create(sess); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.bus.Publish("session.created", sess)
	writeJSON(w, http.StatusCreated, sess)
}

// validEffort reports whether level may be stored as a session's effort: one
// of ogcode's levels, or "" for the model's default. Whether the session's
// model takes it is decided per turn (agent.resolveRunEffort), since the model
// can change after the choice is made.
func validEffort(level string) bool {
	return level == "" || provider.IsEffort(level)
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
		Provider   *string `json:"provider"`
		Effort     *string `json:"effort"`
		Permission *string `json:"permission"`
	}
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if update.Effort != nil && !validEffort(*update.Effort) {
		http.Error(w, "unknown effort level", http.StatusBadRequest)
		return
	}

	if update.Title != nil {
		sess.Title = *update.Title
	}
	if update.Model != nil {
		sess.Model = *update.Model
	}
	if update.Provider != nil {
		sess.Provider = *update.Provider
	}
	if update.Effort != nil {
		sess.Effort = *update.Effort
	}
	if update.Permission != nil {
		sess.Permission = *update.Permission
		// Remember the choice as the mode new sessions start in: a user who
		// prefers Auto should not reselect it for every session. Last toggle
		// wins, and this session keeps its own mode on its row regardless.
		if err := s.permissions.SetDefaultMode(*update.Permission); err != nil {
			slog.Warn("failed to persist default permission mode", "err", err)
		}
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
// sent during this loop run. What the model receives is unchanged by the
// transcript record below: that row is display-only and convertMessages skips
// it, so the guidance still reaches the model exactly once, via this turn.
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

	// Announce the guidance as queued BEFORE it becomes drainable. The loop
	// publishes "delivered" the moment it drains the queue, and it can do that
	// as soon as PushGuidance returns — before the cancel below, even. Published
	// any later, "queued" could reach the client after "delivered" and leave
	// its "Guidance queued" indicator up for the rest of the turn, over guidance
	// the loop had already applied. A cancel-only request queues nothing, so it
	// announces nothing: no "delivered" would ever follow to clear it.
	hasGuidance := strings.TrimSpace(input.Content) != ""
	if hasGuidance {
		s.bus.Publish("loop.guidance", map[string]string{
			"sessionId": string(sessionID),
			"status":    "queued",
		})
	}

	// Push the guidance text FIRST, then cancel. Ordering matters: cancellation
	// is what wakes the loop and advances it to the next iteration, where it
	// drains the queue and decides whether to keep running. If we cancelled
	// before pushing, the loop could wake, drain an empty queue, see the
	// stream/tool finished as a normal "stop", and exit — silently dropping the
	// guidance that lands a moment later. PushGuidance and DrainGuidance share
	// the same mutex, so once PushGuidance returns the guidance is guaranteed
	// visible before any cancellation can propagate.
	if hasGuidance {
		lc.PushGuidance(input.Content)
		// Record it in the transcript as well. The loop consumes guidance from
		// the side-channel above and never writes it down, so without this the
		// user watches their own message vanish: the composer clears, a badge
		// flashes, and the words they typed appear nowhere in the conversation.
		// The record is display-only — convertMessages skips it, so what the
		// model receives is unchanged.
		s.recordGuidanceMessage(sessionID, input.Content)
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

	w.WriteHeader(http.StatusNoContent)
}

// recordGuidanceMessage writes mid-loop guidance into the transcript as a
// display-only user message, so the user can see what they sent.
//
// Failures are logged and swallowed: the guidance itself has already been
// accepted by the running loop at this point, and failing the request over a
// transcript row would tell the user their steering did not land when it did.
func (s *Server) recordGuidanceMessage(sessionID session.SessionID, content string) {
	msg := &session.MessageInfo{
		ID:          session.NewMessageID(),
		SessionID:   sessionID,
		Role:        session.RoleUser,
		DisplayOnly: true,
		CreatedAt:   session.Now(),
	}
	if err := s.store.CreateMessage(msg); err != nil {
		slog.Warn("guidance: could not record transcript message", "session", sessionID, "err", err)
		return
	}
	textData, err := json.Marshal(session.TextPartData{Text: content})
	if err != nil {
		slog.Warn("guidance: could not encode transcript text", "session", sessionID, "err", err)
		return
	}
	part := &session.Part{
		ID:        session.NewPartID(),
		MessageID: msg.ID,
		SessionID: sessionID,
		Type:      session.PartText,
		Data:      textData,
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := s.store.CreatePart(part); err != nil {
		slog.Warn("guidance: could not record transcript text", "session", sessionID, "err", err)
		return
	}
	// Tell the clients, so the message appears without waiting for a poll.
	s.bus.Publish("message.updated", msg)
}

// transcriptPageSize is how many messages one transcript fetch returns. The web
// client pages backwards from the newest with ?before=<oldest id it holds>.
const transcriptPageSize = 300

// transcriptLimit is how many messages a transcript fetch asked for with
// ?limit=. A refresh needs only the newest few — polls and live updates merge
// them into what the client already holds — where a first load and every page
// of history take the full page, which is also the default and the cap.
func transcriptLimit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 || n > transcriptPageSize {
		return transcriptPageSize
	}
	return n
}

// hasOlderHeader tells a paginating client whether messages older than the
// page exist, so it never has to guess from the page's length — a guess that
// costs an extra, empty request whenever a history is an exact multiple of the
// page size. The body stays a bare array for every existing caller.
const hasOlderHeader = "X-Has-Older"

// writeTranscriptPage answers one page of a transcript, oldest first.
func writeTranscriptPage(w http.ResponseWriter, messages []*session.MessageWithParts, hasOlder bool) {
	if messages == nil {
		messages = []*session.MessageWithParts{}
	}
	w.Header().Set(hasOlderHeader, strconv.FormatBool(hasOlder))
	writeJSON(w, http.StatusOK, messages)
}

func (s *Server) handleGetMessages(w http.ResponseWriter, r *http.Request) {
	sessionID := session.SessionID(chi.URLParam(r, "sessionID"))
	before := session.MessageID(r.URL.Query().Get("before"))

	messages, hasOlder, err := s.store.GetMessagesPage(sessionID, before, transcriptLimit(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeTranscriptPage(w, messages, hasOlder)
}

// sessionTokens is a session's whole-transcript token totals: every assistant
// step's counts plus the session row's utility work. It is the figure the token
// pill shows for a session, so it is the session's own and does not change with
// how much of the transcript a client happens to hold.
type sessionTokens struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Reasoning  int `json:"reasoning"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
	Utility    int `json:"utility"`
	Effective  int `json:"effective"`
	Total      int `json:"total"`
}

// handleSessionTokens totals a session's tokens over its whole transcript,
// reading every step without loading it — so a long, paged session reports the
// same figure as when it was new. The token pill calls this in place of summing
// the messages it holds, which on a session past one transcript page is only the
// newest page.
func (s *Server) handleSessionTokens(w http.ResponseWriter, r *http.Request) {
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
	steps, err := s.store.ListStepUsage(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var counts session.TokenCounts
	for _, st := range steps {
		counts.Input += st.Tokens.Input
		counts.Output += st.Tokens.Output
		counts.Reasoning += st.Tokens.Reasoning
		counts.CacheRead += st.Tokens.CacheRead
		counts.CacheWrite += st.Tokens.CacheWrite
	}
	var utility int
	if u := sess.UtilityTokens; u != nil {
		counts.Input += u.Input
		counts.Output += u.Output
		counts.Reasoning += u.Reasoning
		counts.CacheRead += u.CacheRead
		counts.CacheWrite += u.CacheWrite
		utility = u.Effective()
	}
	writeJSON(w, http.StatusOK, sessionTokens{
		Input:      counts.Input,
		Output:     counts.Output,
		Reasoning:  counts.Reasoning,
		CacheRead:  counts.CacheRead,
		CacheWrite: counts.CacheWrite,
		Utility:    utility,
		Effective:  counts.Effective(),
		Total:      counts.Consumed(),
	})
}

func (s *Server) handlePrompt(w http.ResponseWriter, r *http.Request) {
	sessionID := session.SessionID(chi.URLParam(r, "sessionID"))

	var input struct {
		Content        string                  `json:"content"`
		Images         []session.ImagePartData `json:"images,omitempty"`
		Agent          string                  `json:"agent,omitempty"`
		Model          string                  `json:"model,omitempty"`
		Provider       string                  `json:"provider,omitempty"`
		ViewportWidth  int                     `json:"viewportWidth,omitempty"`
		ViewportHeight int                     `json:"viewportHeight,omitempty"`
		// Effort is the reasoning effort picked alongside the model; nil leaves
		// the session's stored choice alone, "" resets it to the default.
		Effort *string `json:"effort,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if input.Effort != nil && !validEffort(*input.Effort) {
		http.Error(w, "unknown effort level", http.StatusBadRequest)
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
	// Record the provider alongside the model. A model id can be served by more
	// than one provider, so the id alone does not pin the endpoint this
	// conversation runs on.
	if input.Provider != "" && sess.Provider != input.Provider {
		sess.Provider = input.Provider
		sess.UpdatedAt = session.Now()
		if err := s.store.Update(sess); err != nil {
			slog.Error("update session provider", "err", err)
		}
	}
	// And the effort picked with them, which this turn runs at.
	if input.Effort != nil && sess.Effort != *input.Effort {
		sess.Effort = *input.Effort
		sess.UpdatedAt = session.Now()
		if err := s.store.Update(sess); err != nil {
			slog.Error("update session effort", "err", err)
		}
	}

	// Auto-generate session title from first message content
	// Only generate if the title is still the default "New session"
	if sess.Title == "New session" && strings.TrimSpace(input.Content) != "" {
		go s.generateTitle(sessionID, input.Content, sess.Model, sess.Provider)
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

	// Start the agent loop in the background. Resume starts it the same way, so
	// the two share one path — a resumed loop that skipped permission gating, or
	// that failed to register its cancel func, would be a loop the user could
	// neither approve tools in nor stop.
	s.startSessionLoop(sessionID, input.Agent, input.ViewportWidth, input.ViewportHeight, requestOrigin(r))

	w.WriteHeader(http.StatusNoContent)
}

// generateTitle uses the LLM to generate a short title from the first message
// content and updates the session. Runs in a goroutine — failures are logged
// but never block the user's prompt.
// recordUtilityUsage adds the tokens a utility call spent to the session's
// running utility totals. Utility calls — title generation here, the risk check
// and compaction in the agent loop — stream usage on a call the main-turn
// accounting never sees, so without this it is dropped from every total. A
// no-op when there is no store or nothing was reported.
func (s *Server) recordUtilityUsage(sessionID session.SessionID, providerID, modelID string, usage *provider.TokenUsage) {
	if s.store == nil || usage == nil {
		return
	}
	tc := session.TokenCounts{
		Input:      usage.InputTokens,
		Output:     usage.OutputTokens,
		Reasoning:  usage.ReasoningTokens,
		CacheRead:  usage.CacheReadTokens,
		CacheWrite: usage.CacheWriteTokens,
	}
	// The global ledger prices the call at the model that made it.
	s.usage.RecordUtility(string(sessionID), providerID, modelID, tc, session.Now())
	if err := s.store.AddUtilityUsage(sessionID, tc); err != nil {
		slog.Warn("record utility usage", "err", err)
		return
	}
	// Announce it so an open token view picks up the new figure: utility usage
	// lands on the session row, not on a message, so no message.updated carries
	// it. Re-read the row so the event carries the accumulated totals.
	if s.bus != nil {
		if sess, gerr := s.store.Get(sessionID); gerr == nil && sess != nil {
			s.bus.Publish("session.updated", sess)
		}
	}
}

func (s *Server) generateTitle(sessionID session.SessionID, firstMessage string, model, providerID string) {
	// Truncate very long messages to avoid wasting tokens
	content := firstMessage
	if len(content) > 500 {
		content = content[:500]
	}

	// Resolve the provider for the session's model
	var p provider.Provider
	if model != "" {
		p = s.registry.ResolveProviderFor(model, providerID)
	}
	if p == nil {
		p = s.defaultProvider
	}
	if p == nil {
		slog.Warn("generateTitle: no provider available, skipping title generation")
		return
	}

	// Title generation runs on the session's own model, the way the risk check,
	// compaction and the memory summary do. A substring "fast model" heuristic
	// (haiku/mini/flash) picked the first model whose id contained "mini" —
	// minimax-m3 for every ollama session — instead of the model the user chose.
	// Fall back to the provider's default only when the session names no model,
	// and to its first model when even that is empty.
	titleModel := model
	if titleModel == "" {
		for _, m := range p.Models() {
			if m.Default {
				titleModel = m.ID
				break
			}
		}
		if titleModel == "" {
			if models := p.Models(); len(models) > 0 {
				titleModel = models[0].ID
			}
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
	var usage *provider.TokenUsage
	for evt := range ch {
		if evt.Type == provider.EventTextDelta {
			title.WriteString(evt.Text)
		}
		if evt.Type == provider.EventUsage {
			usage = evt.Usage
		}
		if evt.Type == provider.EventError {
			slog.Warn("generateTitle: stream error", "err", evt.Error)
			return
		}
	}
	// Record what the title call spent: like the risk check and compaction, it
	// streams usage the main-turn accounting never sees. Recorded even when the
	// generated title is discarded below — the tokens were spent regardless.
	s.recordUtilityUsage(sessionID, p.ID(), titleModel, usage)

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

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
