package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/id"
	"github.com/prasenjeet-symon/ogcode/internal/note"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func (s *Server) handleListNotes(w http.ResponseWriter, r *http.Request) {
	directory := r.URL.Query().Get("directory")
	if directory == "" {
		directory = s.dir
	}
	notes, err := s.noteStore.List(directory)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if notes == nil {
		notes = []*note.Note{}
	}
	writeJSON(w, http.StatusOK, notes)
}

func (s *Server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query          string `json:"query"`
		Directory      string `json:"directory"`
		Model          string `json:"model,omitempty"`
		SessionID      string `json:"sessionId,omitempty"`
		Source         string `json:"source,omitempty"`
		ViewportWidth  int    `json:"viewportWidth,omitempty"`
		ViewportHeight int    `json:"viewportHeight,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	dir := input.Directory
	if dir == "" {
		dir = s.dir
	}

	// Manual note: create blank note immediately, no AI loop
	if input.Source == note.SourceManual {
		title := input.Query
		if title == "" {
			title = "Untitled"
		}
		n := &note.Note{
			ID:        id.NewNoteID(),
			Directory: dir,
			Title:     title,
			Query:     "",
			Content:   "",
			Source:    note.SourceManual,
			Status:    note.StatusDone,
			Version:   0,
			CreatedAt: note.Now(),
			UpdatedAt: note.Now(),
		}
		if err := s.noteStore.Create(n); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.bus.Publish("note.created", n)
		writeJSON(w, http.StatusCreated, n)
		return
	}

	if input.Query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}

	// If a source session ID is provided, rewrite the query using conversation context
	query := input.Query
	if input.SessionID != "" {
		if rewritten, err := s.rewriteNoteQuery(input.SessionID, input.Query, input.Model); err != nil {
			slog.Warn("note query rewrite failed, using original query", "session", input.SessionID, "err", err)
		} else if rewritten != "" {
			query = rewritten
			slog.Info("note query rewritten", "original", truncate(input.Query, 80), "rewritten", truncate(rewritten, 80))
		}
	}

	// Create a session to run the note agent
	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   dir,
		Directory:   dir,
		Title:       "Note: " + truncate(input.Query, 60),
		Model:       input.Model,
		SessionType: "note",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := s.store.Create(sess); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Create the note record linked to the session — store original query for display
	n := &note.Note{
		ID:        id.NewNoteID(),
		Directory: dir,
		Title:     truncate(input.Query, 60),
		Query:     input.Query,
		Content:   "",
		SessionID: string(sess.ID),
		Source:    note.SourceAI,
		Status:    note.StatusGenerating,
		CreatedAt: note.Now(),
		UpdatedAt: note.Now(),
	}
	if err := s.noteStore.Create(n); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.bus.Publish("note.created", n)

	// Fire agent loop with the (potentially rewritten) query as the first user message
	go func() {
		userMsg := &session.MessageInfo{
			ID:        session.NewMessageID(),
			SessionID: sess.ID,
			Role:      session.RoleUser,
			Agent:     "note",
			CreatedAt: session.Now(),
		}
		if err := s.store.CreateMessage(userMsg); err != nil {
			slog.Error("create note user message", "err", err)
			return
		}
		textData, _ := json.Marshal(session.TextPartData{Text: query})
		userPart := &session.Part{
			ID:        session.NewPartID(),
			MessageID: userMsg.ID,
			SessionID: sess.ID,
			Type:      session.PartText,
			Data:      textData,
			CreatedAt: session.Now(),
			UpdatedAt: session.Now(),
		}
		if err := s.store.CreatePart(userPart); err != nil {
			slog.Error("create note user part", "err", err)
			return
		}
		s.bus.Publish("message.updated", userMsg)

		ctx, cancel := context.WithCancel(context.Background())
		s.mu.Lock()
		s.nextToken++
		token := s.nextToken
		s.running[sess.ID] = cancel
		s.runningToken[sess.ID] = token
		s.mu.Unlock()

		defer func() {
			s.mu.Lock()
			if s.runningToken[sess.ID] == token {
				delete(s.running, sess.ID)
				delete(s.runningToken, sess.ID)
			}
			s.mu.Unlock()
		}()

		if err := s.loopRunner.RunLoop(ctx, sess.ID, "note", input.ViewportWidth, input.ViewportHeight); err != nil {
			slog.Error("note agent loop error", "session", sess.ID, "err", err)
		}
	}()

	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) handleUpdateNote(w http.ResponseWriter, r *http.Request) {
	noteID := chi.URLParam(r, "noteID")
	var input struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	n, err := s.noteStore.Get(noteID)
	if err != nil || n == nil {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	title := noteTitle(input.Title, input.Content, n.Query)
	if err := s.noteStore.SaveContent(noteID, title, input.Content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := s.noteStore.Get(noteID)
	if err != nil || updated == nil {
		http.Error(w, "failed to retrieve updated note", http.StatusInternalServerError)
		return
	}
	s.bus.Publish("note.manual_updated", updated)
	writeJSON(w, http.StatusOK, updated)
}

func noteTitle(title, content, fallback string) string {
	if title != "" {
		return title
	}
	for _, line := range strings.SplitN(content, "\n", 20) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	if fallback != "" {
		return fallback
	}
	return "Untitled"
}

func (s *Server) handleTransformText(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text        string `json:"text"`
		Instruction string `json:"instruction"`
		Model       string `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if input.Text == "" || input.Instruction == "" {
		http.Error(w, "text and instruction are required", http.StatusBadRequest)
		return
	}

	systemPrompts := map[string]string{
		"improve": "You are a writing assistant. Improve the writing quality, clarity, and flow of the provided text. Maintain the same tone and intent. Return only the improved text with no explanations.",
		"shorter": "You are a writing assistant. Make the provided text shorter and more concise while preserving the key information. Return only the condensed text with no explanations.",
		"longer":  "You are a writing assistant. Expand the provided text with more detail, examples, and context while maintaining the same tone and intent. Return only the expanded text with no explanations.",
		"grammar": "You are a writing assistant. Fix any grammar, spelling, and punctuation errors in the provided text. Return only the corrected text with no explanations.",
	}
	systemPrompt, ok := systemPrompts[input.Instruction]
	if !ok {
		http.Error(w, "unknown instruction", http.StatusBadRequest)
		return
	}

	var p provider.Provider
	if input.Model != "" {
		p = s.registry.ResolveProvider(input.Model)
	}
	if p == nil {
		p = s.defaultProvider
	}
	if p == nil {
		http.Error(w, "no LLM provider available", http.StatusServiceUnavailable)
		return
	}

	model := input.Model
	if model == "" {
		if models := p.Models(); len(models) > 0 {
			model = models[0].ID
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	promptContent, _ := json.Marshal(input.Text)
	req := provider.StreamRequest{
		Model:    model,
		System:   []string{systemPrompt},
		Messages: []provider.ModelMessage{{Role: "user", Content: promptContent}},
	}

	ch, err := p.StreamChat(ctx, req)
	if err != nil {
		http.Error(w, fmt.Sprintf("stream error: %v", err), http.StatusInternalServerError)
		return
	}

	var result strings.Builder
	for evt := range ch {
		if evt.Type == provider.EventTextDelta {
			result.WriteString(evt.Text)
		}
		if evt.Type == provider.EventError {
			http.Error(w, fmt.Sprintf("LLM error: %s", evt.Error), http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"result": strings.TrimSpace(result.String())})
}

// rewriteNoteQuery uses conversation context from a source session to rewrite
// a short user query into a detailed, self-contained description that the Note
// Agent can understand without the full conversation history.
func (s *Server) rewriteNoteQuery(sourceSessionID, originalQuery, model string) (string, error) {
	// Fetch the last 15 user messages from the source session
	msgs, err := s.store.GetMessages(session.SessionID(sourceSessionID), "", 50)
	if err != nil {
		return "", fmt.Errorf("fetch messages: %w", err)
	}

	// Extract up to 15 user text messages (most recent first)
	var userMessages []string
	for i := len(msgs) - 1; i >= 0 && len(userMessages) < 15; i-- {
		if msgs[i].Info.Role != session.RoleUser {
			continue
		}
		var text string
		for _, p := range msgs[i].Parts {
			if p.Type == session.PartText {
				var data session.TextPartData
				if json.Unmarshal(p.Data, &data) == nil && data.Text != "" {
					text = data.Text
				}
			}
		}
		if text == "" {
			continue
		}
		userMessages = append(userMessages, text)
	}

	if len(userMessages) == 0 {
		return originalQuery, nil
	}

	// Reverse to chronological order
	for i, j := 0, len(userMessages)-1; i < j; i, j = i+1, j-1 {
		userMessages[i], userMessages[j] = userMessages[j], userMessages[i]
	}

	// Build the rewrite prompt
	var contextBuilder strings.Builder
	for i, msg := range userMessages {
		contextBuilder.WriteString(fmt.Sprintf("User message %d: %s", i+1, msg))
		if i < len(userMessages)-1 {
			contextBuilder.WriteString("\n\n")
		}
	}

	// Truncate context to avoid excessive token usage
	contextText := contextBuilder.String()
	if len(contextText) > 4000 {
		contextText = contextText[:4000] + "\n[...truncated...]"
	}

	promptText := fmt.Sprintf(
		"You are a note-writing assistant. The user wants to save notes about a topic from their conversation.\n\n"+
			"Here is the conversation context (recent user messages):\n\n%s\n\n"+
			"The user's current message they want to save as a note: \"%s\"\n\n"+
			"Based on the conversation context, rewrite the user's message into a detailed, self-contained "+
			"description of what the note should cover. The note agent will use this to research and prepare "+
			"comprehensive notes. Include relevant specifics from the conversation (file names, function names, "+
			"technical details, decisions made, etc.). Respond with ONLY the rewritten description, "+
			"no preamble or explanation.",
		contextText, originalQuery,
	)

	// Resolve provider for the LLM call — use the same model as the Note Agent
	var p provider.Provider
	if model != "" {
		p = s.registry.ResolveProvider(model)
	}
	if p == nil {
		p = s.defaultProvider
	}
	if p == nil {
		return originalQuery, fmt.Errorf("no LLM provider available")
	}

	rewriteModel := model
	if rewriteModel == "" {
		// If no model specified, use the provider's first available model
		if models := p.Models(); len(models) > 0 {
			rewriteModel = models[0].ID
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	promptContent, _ := json.Marshal(promptText)
	req := provider.StreamRequest{
		Model:    rewriteModel,
		System:   []string{"You rewrite short user queries into detailed, self-contained descriptions for a note-taking agent. Respond with only the rewritten text."},
		Messages: []provider.ModelMessage{{Role: "user", Content: promptContent}},
	}

	ch, err := p.StreamChat(ctx, req)
	if err != nil {
		return originalQuery, fmt.Errorf("rewrite stream: %w", err)
	}

	var result strings.Builder
	for evt := range ch {
		if evt.Type == provider.EventTextDelta {
			result.WriteString(evt.Text)
		}
		if evt.Type == provider.EventError {
			return originalQuery, fmt.Errorf("rewrite stream error: %s", evt.Error)
		}
	}

	rewritten := strings.TrimSpace(result.String())
	if rewritten == "" {
		return originalQuery, nil
	}
	return rewritten, nil
}

func (s *Server) handleGetNote(w http.ResponseWriter, r *http.Request) {
	noteID := chi.URLParam(r, "noteID")
	n, err := s.noteStore.Get(noteID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n == nil {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) handleListNoteVersions(w http.ResponseWriter, r *http.Request) {
	noteID := chi.URLParam(r, "noteID")
	versions, err := s.noteStore.ListVersions(noteID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if versions == nil {
		versions = []*note.NoteVersion{}
	}
	writeJSON(w, http.StatusOK, versions)
}

func (s *Server) handleExportNote(w http.ResponseWriter, r *http.Request) {
	noteID := chi.URLParam(r, "noteID")
	n, err := s.noteStore.Get(noteID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n == nil {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}

	filename := strings.ToLower(strings.ReplaceAll(n.Title, " ", "-"))
	filename = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, filename)
	if filename == "" {
		filename = "note"
	}
	if len(filename) > 50 {
		filename = filename[:50]
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.md"`, filename))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(n.Content))
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	noteID := chi.URLParam(r, "noteID")
	if err := s.noteStore.Delete(noteID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.bus.Publish("note.deleted", map[string]string{"id": noteID})
	w.WriteHeader(http.StatusNoContent)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
