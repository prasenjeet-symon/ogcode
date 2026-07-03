package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/id"
	"github.com/prasenjeet-symon/ogcode/internal/mcp"
	"github.com/prasenjeet-symon/ogcode/internal/memory"
	"github.com/prasenjeet-symon/ogcode/internal/note"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// LoopRunner orchestrates the agent loop for a session.
type LoopRunner struct {
	Store           *session.Store
	Bus             *bus.Bus
	Registry        *provider.Registry
	DefaultProvider provider.Provider
	Tools           *tool.Registry
	Dir             string
	MaxSteps        int
	Memory          *memory.Memory
	MCP             *mcp.Client
	NoteStore       *note.Store
}

// RunLoop executes the core agent loop: prompt -> stream -> tools -> loop back.
func (lr *LoopRunner) RunLoop(ctx context.Context, sessionID session.SessionID, agentName string, viewportWidth int, viewportHeight int) error {
	agent := GetAgent(agentName)
	// Read MaxSteps into a local; never mutate the shared LoopRunner field. A
	// deep_search call runs a nested RunLoop concurrently with the parent loop,
	// so writing lr.MaxSteps here would be a data race.
	maxSteps := lr.MaxSteps
	if maxSteps == 0 {
		maxSteps = 1000
	}

	// Always notify the frontend when the loop exits, regardless of reason.
	// Without this, any early return (DB error, stream error, panic recovery)
	// leaves the client in a permanently-stuck loading state.
	exitReason := "error"
	defer func() {
		lr.Bus.Publish("loop.done", map[string]string{
			"sessionId": string(sessionID),
			"reason":    exitReason,
		})
	}()

	// Resolve provider based on session's model
	sess, err := lr.Store.Get(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	// Warn if the agent name doesn't match the session type — catches accidental
	// mismatches at call sites (breakdown intentionally differs, so not a hard error).
	if sess != nil && sess.SessionType != "" && sess.SessionType != agentName {
		knownMismatch := agentName == "breakdown" && sess.SessionType == "build"
		if !knownMismatch {
			slog.Warn("agent/session type mismatch — possible call-site bug",
				"sessionType", sess.SessionType, "agentName", agentName, "session", sessionID)
		}
	}

	// Load AGENT.md and MEMORY.md files from session directory
	workDir := lr.Dir
	if sess != nil && sess.Directory != "" {
		workDir = sess.Directory
	}
	agentMDContent := LoadAgentMD(workDir)
	memoryMDContent := LoadMemoryMD(workDir)

	// For note sessions: save the final assistant message as note content when the loop exits.
	// This defer runs before the loop.done publish (LIFO) so the note is persisted before
	// the frontend is notified.
	if lr.NoteStore != nil && sess != nil && sess.SessionType == "note" {
		capturedSessionID := string(sessionID)
		defer func() {
			msgs, err := lr.Store.GetMessages(sessionID, "", 1000)
			if err != nil {
				slog.Warn("note finalize: failed to load messages", "session", capturedSessionID, "err", err)
				return
			}
			var content string
			for i := len(msgs) - 1; i >= 0; i-- {
				if msgs[i].Info.Role != session.RoleAssistant {
					continue
				}
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
			if err := lr.NoteStore.FinalizeBySession(capturedSessionID, content, exitReason); err != nil {
				slog.Warn("note finalize: failed to save note", "session", capturedSessionID, "err", err)
				return
			}
			lr.Bus.Publish("note.updated", map[string]string{"sessionId": capturedSessionID})
			slog.Info("note finalized", "session", capturedSessionID, "reason", exitReason)
		}()
	}

	memoryEnabled := lr.Memory != nil && lr.Memory.Enabled()

	var p provider.Provider
	var modelID string
	if sess != nil && sess.Model != "" {
		slog.Info("resolving model for session", "session", sessionID, "requestedModel", sess.Model)
		p = lr.Registry.ResolveProvider(sess.Model)
		modelID = sess.Model
		if p != nil {
			slog.Info("resolved provider for model", "session", sessionID, "model", sess.Model, "provider", p.ID())
		} else {
			slog.Warn("failed to resolve provider for model, using default", "session", sessionID, "model", sess.Model)
		}
	}
	if p == nil {
		slog.Info("using default provider", "session", sessionID)
		// Prefer the registry's current default so credential changes applied at
		// runtime (onboarding/settings) take effect; fall back to the immutable
		// startup default only when no provider is configured at all.
		if dp := lr.Registry.Default(); dp != nil {
			p = dp
		} else {
			p = lr.DefaultProvider
		}
	}

	// Whether the active model accepts image input — passed to tools so they can
	// decide to return an image (e.g. a rendered PDF page) instead of text.
	modelSupportsImages := lr.resolveImageSupport(ctx, p, modelID)

	// Restore compaction summary from a previous turn (persisted in the session row).
	// Applied on every step so all future LLM calls stay within the context window.
	compactionSummary := ""
	if sess != nil {
		compactionSummary = sess.CompactionSummary
	}

	slog.Info("agent loop starting", "session", sessionID, "agent", agent.ID, "model", modelID)

	// Agentic memory: read graph context before the loop
	var memoryText string
	if memoryEnabled {
		graphText := lr.Memory.ReadMemory(ctx, string(sessionID))
		if graphText != "" {
			memoryText = graphText
		}

		messages, _ := lr.Store.GetMessages(sessionID, "", 1000)

		// Append the last non-tool assistant response so the LLM has continuity
		// without needing to recall it. Targeted recall for specific facts is done
		// on-demand via the memory_recall tool.
		if lastText := extractLastAssistantText(messages); lastText != "" {
			if memoryText != "" {
				memoryText += "\n\n### Last Response\n" + lastText
			} else {
				memoryText = "### Last Response\n" + lastText
			}
		}

		// Calculate net token savings: without memory the full history is sent every turn,
		// so savings = all skipped history tokens minus the memory context injected.
		// Negative means memory adds overhead (normal on short sessions); positive means savings.
		// Skip if memoryText is only whitespace (would be overhead with no context benefit).
		if strings.TrimSpace(memoryText) != "" && len(messages) > 1 {
			lastUserIdx := -1
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Info.Role == session.RoleUser {
					for _, p := range messages[i].Parts {
						if p.Type == session.PartText {
							lastUserIdx = i
							break
						}
					}
					if lastUserIdx >= 0 {
						break
					}
				}
			}
			if lastUserIdx > 0 {
				var skippedChars int
				for _, msg := range messages[:lastUserIdx] {
					for _, p := range msg.Parts {
						skippedChars += len(p.Data)
					}
				}
				// 1 token ≈ 4 chars. Net = history avoided − memory injected.
				netSaved := (skippedChars - len(memoryText)) / 4
				lr.Bus.Publish("memory.savings", map[string]any{
					"sessionId":   string(sessionID),
					"savedTokens": netSaved,
				})
				if err := lr.Store.UpdateMemoryTokensSaved(sessionID, netSaved); err != nil {
					slog.Warn("persist memory tokens saved", "err", err)
				}
			}
		}
	}

	for step := 1; step <= maxSteps; step++ {
		if step == maxSteps {
			slog.Warn("agent loop reached MaxSteps limit", "session", sessionID, "maxSteps", maxSteps)
		}
		slog.Info("agent loop step", "session", sessionID, "step", step)

		// Check for context cancellation at the start of each loop iteration
		if ctx.Err() != nil {
			slog.Info("agent loop cancelled", "session", sessionID, "step", step)
			exitReason = "aborted"
			return ctx.Err()
		}

		// Load all messages for this session (retry on DB contention)
		var messages []*session.MessageWithParts
		for dbAttempt := 0; dbAttempt < 3; dbAttempt++ {
			messages, err = lr.Store.GetMessages(sessionID, "", 1000)
			if err == nil {
				break
			}
			slog.Warn("get messages failed, retrying", "session", sessionID, "attempt", dbAttempt+1, "err", err)
			if dbAttempt < 2 {
				time.Sleep(time.Duration(dbAttempt+1) * 500 * time.Millisecond)
			}
		}
		if err != nil {
			return fmt.Errorf("load messages: %w", err)
		}

		// Check if we should continue: last assistant finished means done
		if shouldBreak(messages) {
			last := messages[len(messages)-1]
			finish := "stop"
			if last.Info.Finish != nil {
				finish = *last.Info.Finish
			}
			slog.Info("agent loop breaking", "session", sessionID, "reason", "last assistant finished", "finish", finish, "totalMessages", len(messages))
			exitReason = finish
			if memoryEnabled {
				lr.writeMemory(ctx, sessionID, p, modelID)
			}
			return nil
		}

		// Create new assistant message
		assistantID := session.MessageID(id.NewMessageID())
		assistantMsg := &session.MessageInfo{
			ID:        assistantID,
			SessionID: sessionID,
			Role:      session.RoleAssistant,
			Agent:     agent.ID,
			ParentID:  lastUserMessageID(messages),
			CreatedAt: session.Now(),
		}
		if err := lr.Store.CreateMessage(assistantMsg); err != nil {
			return fmt.Errorf("create assistant message: %w", err)
		}

		// Resolve tools for this agent
		agentTools := lr.Tools.ForAgent(agent.Tools)
		providerTools := make([]provider.ToolDefinition, 0, len(agentTools))
		for _, t := range agentTools {
			providerTools = append(providerTools, provider.ToolDefinition{
				Name:        t.ID(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			})
		}

		// Add MCP tools if available
		if lr.MCP != nil {
			for name, td := range lr.MCP.Tools() {
				providerTools = append(providerTools, provider.ToolDefinition{
					Name:        name,
					Description: td.Description,
					Parameters:  td.InputSchema,
				})
			}
		}
		slog.Info("resolved tools", "count", len(providerTools))

		// Build system prompt
		var mcpTools map[string]mcp.ToolDef
		if lr.MCP != nil {
			mcpTools = lr.MCP.Tools()
		}
		system := buildSystemPrompt(agent, workDir, memoryEnabled, agentMDContent, memoryMDContent, mcpTools, viewportWidth, viewportHeight)

		// The base system prompt is static within a session (no date, no
		// per-turn content) so it stays byte-for-byte identical across turns.
		// The current date is injected as a separate trailing system prompt
		// entry via systemReminderPrompt(). This separation is critical for
		// Anthropic prompt caching: the Anthropic provider puts the cache_control
		// breakpoint on the first (static) system block only, so the date —
		// which changes every turn — does not invalidate the cache.
		systemPrompts := []string{system, systemReminderPrompt()}
		var modelMessages []provider.ModelMessage

		if memoryEnabled {
			// Agentic memory path: memory handles context compression by filtering
			// history to the last user message and injecting <prior_context>.
			// Compaction is completely bypassed when memory is active.
			modelMessages = toProviderMessages(messages, memoryText)
		} else {
			// Compaction path (memory disabled): compaction operates on user-turn
			// boundaries, not individual tool steps. The full current user turn (from
			// the last text-user message forward) is always sent intact. Previous
			// turns are represented by the compactionSummary injected into the
			// system prompt so the model never loses the thread of the session.
			turnStartIdx := findLastTextUserMessageIndex(messages)
			if turnStartIdx >= 0 && turnStartIdx < len(messages) {
				modelMessages = toProviderMessages(messages[turnStartIdx:], "")
			} else {
				modelMessages = toProviderMessages(messages, "")
			}
			if compactionSummary != "" {
				systemPrompts = append(systemPrompts, compactionSummary)
			}
		}

		// Stream from LLM with retry for transient errors
		streamReq := provider.StreamRequest{
			Model:    modelID,
			System:   systemPrompts,
			Messages: modelMessages,
			Tools:    providerTools,
			Abort:    ctx,
		}

		compactionCount := 0

		// Proactive compaction: estimate the request body size and compact
		// before sending if it exceeds a safe threshold. This prevents sending
		// requests that will fail with context-length errors (especially with
		// smaller local models like Ollama). We only do this once per step
		// to avoid compaction loops.
		const maxRequestBodyBytes = 500 * 1024 // 500KB is a safe upper bound for most models
		estimatedSize := estimateRequestSize(streamReq)
		if !memoryEnabled && estimatedSize > maxRequestBodyBytes && compactionCount == 0 {
			before := len(streamReq.Messages)
			slog.Info("proactive compaction: estimated request body exceeds threshold", "session", sessionID, "estimatedBytes", estimatedSize, "threshold", maxRequestBodyBytes, "messages", before)
			summaryAddendum, compactedMsgs := lr.llmCompact(ctx, p, modelID, streamReq.Messages)
			streamReq.Messages = compactedMsgs
			if summaryAddendum != "" {
				streamReq.System = append(streamReq.System, summaryAddendum)
				compactionSummary = summaryAddendum
				if err := lr.Store.UpdateCompactionSummary(sessionID, summaryAddendum); err != nil {
					slog.Error("persist compaction summary", "err", err)
				}
			}
			compactionCount++
			slog.Info("proactive compaction completed", "session", sessionID, "before", before, "after", len(streamReq.Messages), "newEstimatedBytes", estimateRequestSize(streamReq))
			lr.Bus.Publish("loop.compacted", map[string]string{"sessionId": string(sessionID)})
		}

		slog.Info("calling LLM", "session", sessionID, "step", step, "model", modelID, "messages", len(streamReq.Messages))

		var streamCh <-chan provider.StreamEvent
		var streamErr error
		const maxCompactions = 2
		const maxRetries = 3
		for attempt := 1; attempt <= maxRetries; attempt++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			streamCh, streamErr = p.StreamChat(ctx, streamReq)
			if streamErr == nil {
				break
			}
			slog.Warn("stream chat attempt failed", "session", sessionID, "attempt", attempt, "err", streamErr)
			// Context length exceeded: summarize old history with the LLM and retry.
			// Only used when agentic memory is OFF; with memory active the context is
			// already compressed via <prior_context> so compaction should not run.
			if !memoryEnabled && compactionCount < maxCompactions && isContextLengthError(streamErr) {
				before := len(streamReq.Messages)
				slog.Info("context length exceeded, using LLM to compact history", "session", sessionID, "messages", before)
				summaryAddendum, compactedMsgs := lr.llmCompact(ctx, p, modelID, streamReq.Messages)
				streamReq.Messages = compactedMsgs
				if summaryAddendum != "" {
					streamReq.System = append(streamReq.System, summaryAddendum)
					// Persist so future steps and future turns reuse the same summary.
					// Use the column-specific updater to avoid overwriting concurrent
					// changes to other session fields (title, model, etc.).
					compactionSummary = summaryAddendum
					if err := lr.Store.UpdateCompactionSummary(sessionID, summaryAddendum); err != nil {
						slog.Error("persist compaction summary", "err", err)
					}
				}
				compactionCount++
				slog.Info("llm-compacted context", "session", sessionID, "before", before, "after", len(streamReq.Messages))
				lr.Bus.Publish("loop.compacted", map[string]string{"sessionId": string(sessionID)})
				attempt-- // don't count compaction as a retry attempt
				continue
			}
			if attempt < maxRetries && isTransientError(streamErr) {
				backoff := time.Duration(attempt*attempt) * time.Second
				slog.Info("retrying stream chat", "session", sessionID, "backoff", backoff)
				select {
				case <-ctx.Done():
					exitReason = "aborted"
					return ctx.Err()
				case <-time.After(backoff):
				}
				continue
			}
			// Non-transient or exhausted retries
			errStr := streamErr.Error()
			assistantMsg.Error = &errStr
			finish := "error"
			assistantMsg.Finish = &finish
			lr.Store.UpdateMessage(assistantMsg)
			lr.Bus.Publish("message.updated", assistantMsg)
			return fmt.Errorf("stream chat: %w", streamErr)
		}
		// Guard: loop exhausted all attempts via continue without returning (edge case)
		if streamErr != nil {
			errStr := streamErr.Error()
			assistantMsg.Error = &errStr
			finish := "error"
			assistantMsg.Finish = &finish
			lr.Store.UpdateMessage(assistantMsg)
			lr.Bus.Publish("message.updated", assistantMsg)
			return fmt.Errorf("stream chat: %w", streamErr)
		}

		// Process stream events
		var currentText strings.Builder
		var currentReasoning strings.Builder
		var pendingToolCalls []pendingToolCall
		var finishReason string
		var streamUsage *provider.TokenUsage

		// Streaming text part: created on first delta and flushed to DB periodically
		// so the UI shows live output instead of waiting for the full response.
		var streamTextPart *session.Part
		var lastTextFlush time.Time
		const textFlushInterval = 300 * time.Millisecond

		// Streaming reasoning part: same pattern — accumulate deltas into one part
		// instead of creating a new Part per thinking chunk.
		var streamReasoningPart *session.Part
		var lastReasoningFlush time.Time
		const reasoningFlushInterval = 1 * time.Second

		flushReasoningPart := func(final bool) {
			if currentReasoning.Len() == 0 || streamReasoningPart == nil {
				return
			}
			if !final && time.Since(lastReasoningFlush) < reasoningFlushInterval {
				return
			}
			text := currentReasoning.String()
			// Truncate extremely long reasoning to avoid flooding DB and UI
			const maxReasoningLen = 50_000
			if len(text) > maxReasoningLen {
				text = text[:maxReasoningLen] + "\n... (truncated)"
			}
			reasonData, _ := json.Marshal(session.ReasoningPartData{Text: text})
			streamReasoningPart.Data = reasonData
			streamReasoningPart.UpdatedAt = session.Now()
			lr.Store.UpdatePart(streamReasoningPart)
			lastReasoningFlush = time.Now()
			lr.Bus.Publish("message.part.updated", map[string]string{
				"sessionId": string(sessionID),
				"partId":    string(streamReasoningPart.ID),
			})
		}

		flushTextPart := func(final bool) {
			if currentText.Len() == 0 || streamTextPart == nil {
				return
			}
			if !final && time.Since(lastTextFlush) < textFlushInterval {
				return
			}
			textData, _ := json.Marshal(session.TextPartData{Text: currentText.String()})
			streamTextPart.Data = textData
			streamTextPart.UpdatedAt = session.Now()
			lr.Store.UpdatePart(streamTextPart)
			lastTextFlush = time.Now()
			lr.Bus.Publish("message.part.updated", map[string]string{
				"sessionId": string(sessionID),
				"partId":    string(streamTextPart.ID),
			})
		}

		for evt := range streamCh {
			// Check for context cancellation while processing stream events
			if ctx.Err() != nil {
				slog.Info("agent loop cancelled during stream processing", "session", sessionID)
				flushTextPart(true)
				flushReasoningPart(true)
				// Mark the assistant message as aborted
				abortedReason := "aborted"
				assistantMsg.Finish = &abortedReason
				lr.Store.UpdateMessage(assistantMsg)
				lr.Bus.Publish("message.updated", assistantMsg)
				exitReason = "aborted"
				return ctx.Err()
			}

			switch evt.Type {
			case provider.EventTextDelta:
				currentText.WriteString(evt.Text)
				if streamTextPart == nil {
					// Create the part in DB on first delta so the client can see text arriving
					textData, _ := json.Marshal(session.TextPartData{Text: currentText.String()})
					newTextPart := &session.Part{
						ID:        session.PartID(id.NewPartID()),
						MessageID: assistantID,
						SessionID: sessionID,
						Type:      session.PartText,
						Data:      textData,
						CreatedAt: session.Now(),
						UpdatedAt: session.Now(),
					}
					if err := lr.Store.CreatePart(newTextPart); err != nil {
						slog.Error("create streaming text part", "err", err)
					} else {
						streamTextPart = newTextPart
						lastTextFlush = time.Now()
						lr.Bus.Publish("message.part.updated", map[string]string{
							"sessionId": string(sessionID),
							"partId":    string(newTextPart.ID),
						})
					}
				} else {
					flushTextPart(false)
				}

			case provider.EventUsage:
				if evt.Usage != nil {
					u := *evt.Usage
					streamUsage = &u
				}

			case provider.EventToolCallStart:
				tc := pendingToolCall{
					CallID: evt.ToolCallID,
					Name:   evt.ToolName,
					Input:  evt.ToolInput,
				}
				pendingToolCalls = append(pendingToolCalls, tc)

				// Create tool part
				toolInput := evt.ToolInput
				if len(toolInput) == 0 {
					toolInput = json.RawMessage("{}")
				}
				toolData, _ := json.Marshal(session.ToolPartData{
					Tool:   evt.ToolName,
					CallID: evt.ToolCallID,
					State: session.ToolState{
						Status: session.ToolPending,
						Input:  toolInput,
					},
				})
				toolPart := &session.Part{
					ID:        session.PartID(id.NewPartID()),
					MessageID: assistantID,
					SessionID: sessionID,
					Type:      session.PartTool,
					Data:      toolData,
					CreatedAt: session.Now(),
					UpdatedAt: session.Now(),
				}
				if err := lr.Store.CreatePart(toolPart); err != nil {
					slog.Error("create tool part", "err", err)
				}
				tc.PartID = toolPart.ID
				pendingToolCalls[len(pendingToolCalls)-1] = tc

				lr.Bus.Publish("message.part.updated", map[string]string{
					"sessionId": string(sessionID),
					"partId":    string(toolPart.ID),
				})

			case provider.EventToolCallDelta:
				// Accumulate tool input deltas
				for i := range pendingToolCalls {
					if pendingToolCalls[i].CallID == evt.ToolCallID {
						pendingToolCalls[i].Input = append(pendingToolCalls[i].Input, evt.ToolInput...)
						break
					}
				}

			case provider.EventToolCallEnd:
				// Finalize tool input
				for i := range pendingToolCalls {
					if pendingToolCalls[i].CallID == evt.ToolCallID {
						pendingToolCalls[i].Ready = true
						break
					}
				}

			case provider.EventReasoning:
				currentReasoning.WriteString(evt.Text)
				if streamReasoningPart == nil {
					text := currentReasoning.String()
					const maxReasoningLen = 50_000
					if len(text) > maxReasoningLen {
						text = text[:maxReasoningLen] + "\n... (truncated)"
					}
					reasonData, _ := json.Marshal(session.ReasoningPartData{Text: text})
					newReasoningPart := &session.Part{
						ID:        session.PartID(id.NewPartID()),
						MessageID: assistantID,
						SessionID: sessionID,
						Type:      session.PartReasoning,
						Data:      reasonData,
						CreatedAt: session.Now(),
						UpdatedAt: session.Now(),
					}
					if err := lr.Store.CreatePart(newReasoningPart); err != nil {
						slog.Error("create streaming reasoning part", "err", err)
					} else {
						streamReasoningPart = newReasoningPart
						lastReasoningFlush = time.Now()
						lr.Bus.Publish("message.part.updated", map[string]string{
							"sessionId": string(sessionID),
							"partId":    string(newReasoningPart.ID),
						})
					}
				} else {
					flushReasoningPart(false)
				}

			case provider.EventFinish:
				if evt.FinishReason != nil {
					finishReason = *evt.FinishReason
				}

			case provider.EventError:
				errStr := evt.Error
				assistantMsg.Error = &errStr
				finish := "error"
				assistantMsg.Finish = &finish
				lr.Store.UpdateMessage(assistantMsg)
				lr.Bus.Publish("message.updated", assistantMsg)
				return fmt.Errorf("stream error: %s", evt.Error)
			}
		}

		// Finalize text part: flush any remaining buffered text to DB.
		// If no streaming part was created yet (e.g. model returned text without deltas),
		// create the part now.
		if currentText.Len() > 0 {
			if streamTextPart != nil {
				flushTextPart(true)
			} else {
				textData, _ := json.Marshal(session.TextPartData{Text: currentText.String()})
				textPart := &session.Part{
					ID:        session.PartID(id.NewPartID()),
					MessageID: assistantID,
					SessionID: sessionID,
					Type:      session.PartText,
					Data:      textData,
					CreatedAt: session.Now(),
					UpdatedAt: session.Now(),
				}
				if err := lr.Store.CreatePart(textPart); err != nil {
					slog.Error("create text part", "err", err)
				}
			}
		}

		// Finalize reasoning part: flush any remaining buffered reasoning to DB.
		if currentReasoning.Len() > 0 {
			if streamReasoningPart != nil {
				flushReasoningPart(true)
			} else {
				text := currentReasoning.String()
				const maxReasoningLen = 50_000
				if len(text) > maxReasoningLen {
					text = text[:maxReasoningLen] + "\n... (truncated)"
				}
				reasonData, _ := json.Marshal(session.ReasoningPartData{Text: text})
				reasonPart := &session.Part{
					ID:        session.PartID(id.NewPartID()),
					MessageID: assistantID,
					SessionID: sessionID,
					Type:      session.PartReasoning,
					Data:      reasonData,
					CreatedAt: session.Now(),
					UpdatedAt: session.Now(),
				}
				if err := lr.Store.CreatePart(reasonPart); err != nil {
					slog.Error("create reasoning part", "err", err)
				}
			}
		}

		// Mark all pending tool calls as ready when the stream is done.
		// Some providers send finish_reason "stop" alongside tool calls, so we
		// must mark them ready regardless of the reason — otherwise they stay in
		// "pending" forever and the frontend thinks tools are still running.
		if len(pendingToolCalls) > 0 {
			for i := range pendingToolCalls {
				pendingToolCalls[i].Ready = true
			}
		}

		// Detect stream interruption: if the channel closed without a finish event
		// and without any error event, the stream was likely interrupted (network,
		// timeout, etc.). Do NOT default to "stop" — that silently kills long loops.
		if finishReason == "" {
			if currentText.Len() > 0 || len(pendingToolCalls) > 0 {
				// We received content but no finish signal — stream was interrupted
				slog.Warn("stream ended without finish_reason, treating as error", "session", sessionID, "textLen", currentText.Len(), "toolCalls", len(pendingToolCalls))
				finishReason = "error"
				errStr := "stream interrupted: LLM connection closed without finish signal"
				assistantMsg.Error = &errStr
			} else {
				// No content and no finish — likely a connection failure
				slog.Warn("stream ended without content or finish_reason", "session", sessionID)
				finishReason = "error"
				errStr := "stream interrupted: no content received"
				assistantMsg.Error = &errStr
			}
		}
		assistantMsg.Finish = &finishReason
		if streamUsage != nil {
			tc := session.TokenCounts{
				Input:      streamUsage.InputTokens,
				Output:     streamUsage.OutputTokens,
				Reasoning:  streamUsage.ReasoningTokens,
				CacheRead:  streamUsage.CacheReadTokens,
				CacheWrite: streamUsage.CacheWriteTokens,
				// CacheRead and CacheWrite are input variants with different pricing;
				// include them so Total reflects all tokens actually consumed.
				Total: streamUsage.InputTokens + streamUsage.CacheReadTokens +
					streamUsage.CacheWriteTokens + streamUsage.OutputTokens,
			}
			assistantMsg.Tokens = &tc
		}
		if err := lr.Store.UpdateMessage(assistantMsg); err != nil {
			slog.Error("update message finish", "err", err)
		}
		lr.Bus.Publish("message.updated", assistantMsg)

		// Execute ready tool calls in parallel for improved throughput.
		// Built-in tools (bash, read, write, etc.) are stateless and safe for
		// concurrent use. MCP calls are serialized by the client's mutex, so
		// they won't truly run in parallel with each other but will run in
		// parallel with built-in tools. DB part updates are sequential before
		// and after the parallel execution phase to keep state consistent.
		var readyCalls []pendingToolCall
		for _, tc := range pendingToolCalls {
			if tc.Ready {
				readyCalls = append(readyCalls, tc)
			}
		}
		toolCallsExecuted := len(readyCalls) > 0

		if toolCallsExecuted {
			if len(readyCalls) > 1 {
				slog.Info("executing tool calls in parallel", "session", sessionID, "count", len(readyCalls), "tools", toolNames(readyCalls))
			}
			// Check for context cancellation before starting any execution
			if ctx.Err() != nil {
				slog.Info("agent loop cancelled before tool execution", "session", sessionID)
				exitReason = "aborted"
				return ctx.Err()
			}

			type toolExecInfo struct {
				tc     pendingToolCall
				result tool.Result
				err    error
			}

			// Mark all ready tool parts as "running" first (sequential — fast DB ops)
			execInfos := make([]toolExecInfo, len(readyCalls))
			for i, tc := range readyCalls {
				part, _ := lr.Store.GetPart(tc.PartID)
				if part != nil {
					var toolData session.ToolPartData
					json.Unmarshal(part.Data, &toolData)
					toolData.State = session.ToolState{
						Status: session.ToolRunning,
						Input:  toolData.State.Input,
						Title:  &tc.Name,
						Time: session.ToolTime{
							Start: session.Now(),
						},
					}
					updatedData, _ := json.Marshal(toolData)
					part.Data = updatedData
					part.UpdatedAt = session.Now()
					lr.Store.UpdatePart(part)
					lr.Bus.Publish("message.part.updated", map[string]string{
						"sessionId": string(sessionID),
						"partId":    string(part.ID),
					})
				}
				execInfos[i].tc = tc
			}

			// Execute all ready tool calls concurrently
			var wg sync.WaitGroup
			for i := range readyCalls {
				wg.Add(1)
				go func(idx int) {
					defer wg.Done()
					tc := readyCalls[idx]
					result, err := lr.executeTool(ctx, sessionID, assistantID, tc, agent, workDir, modelSupportsImages, modelID)
					execInfos[idx].result = result
					execInfos[idx].err = err
				}(i)
			}
			wg.Wait()

			// Update all tool parts with results (sequential — DB writes)
			for _, info := range execInfos {
				tc := info.tc
				part, perr := lr.Store.GetPart(tc.PartID)
				if perr != nil {
					slog.Error("get tool part", "err", perr)
					continue
				}
				if part == nil {
					slog.Error("tool part not found", "partId", tc.PartID)
					continue
				}

				var toolData session.ToolPartData
				if err := json.Unmarshal(part.Data, &toolData); err != nil {
					slog.Error("unmarshal tool data", "err", err)
					continue
				}

				if info.err != nil {
					errStr := info.err.Error()
					toolData.State = session.ToolState{
						Status: session.ToolError,
						Input:  tc.Input,
						Error:  &errStr,
						Title:  &tc.Name,
						Time:   toolData.State.Time,
					}
				} else {
					toolData.State = session.ToolState{
						Status:   session.ToolCompleted,
						Input:    tc.Input,
						Output:   &info.result.Output,
						Title:    &info.result.Title,
						Metadata: mustMarshal(info.result.Metadata),
						Time: session.ToolTime{
							Start: toolData.State.Time.Start,
							End:   session.Now(),
						},
					}
					if info.result.Image != nil {
						toolData.State.Image = &session.ToolImage{
							MediaType: info.result.Image.MediaType,
							Data:      info.result.Image.Data,
						}
					}
				}

				updatedData, _ := json.Marshal(toolData)
				part.Data = updatedData
				part.UpdatedAt = session.Now()
				if err := lr.Store.UpdatePart(part); err != nil {
					slog.Error("update tool part", "err", err)
				}

				lr.Bus.Publish("message.part.updated", map[string]string{
					"sessionId": string(sessionID),
					"partId":    string(part.ID),
				})
			}

			// If context was cancelled during execution, bail out now. The
			// tool-result message must not be created — it would contain
			// partial/missing outputs for unexecuted tool calls.
			if ctx.Err() != nil {
				slog.Info("agent loop cancelled after tool execution", "session", sessionID)
				exitReason = "aborted"
				return ctx.Err()
			}
		}

		// If tool calls were executed, always continue the loop regardless of
		// finish_reason — some providers send "stop" even alongside tool calls.
		// Only break when there are no tool calls to feed back.
		if !toolCallsExecuted {
			slog.Info("agent loop complete", "session", sessionID, "steps", step, "reason", finishReason)
			exitReason = finishReason
			if memoryEnabled {
				lr.writeMemory(ctx, sessionID, p, modelID)
			}
			return nil
		}

		// Create a user message with tool results so the next LLM iteration sees them.
		toolResultID := session.MessageID(id.NewMessageID())
		toolResultMsg := &session.MessageInfo{
			ID:        toolResultID,
			SessionID: sessionID,
			Role:      session.RoleUser,
			ParentID:  &assistantID,
			CreatedAt: session.Now(),
		}
		if err := lr.Store.CreateMessage(toolResultMsg); err != nil {
			slog.Error("create tool result message", "err", err)
		} else {
			for _, tc := range pendingToolCalls {
				if !tc.Ready {
					continue
				}
				part, perr := lr.Store.GetPart(tc.PartID)
				if perr != nil || part == nil {
					continue
				}
				var toolData session.ToolPartData
				if err := json.Unmarshal(part.Data, &toolData); err != nil {
					continue
				}
				resultData, _ := json.Marshal(session.ToolPartData{
					Tool:   toolData.Tool,
					CallID: toolData.CallID,
					State:  toolData.State,
				})
				resultPart := &session.Part{
					ID:        session.PartID(id.NewPartID()),
					MessageID: toolResultID,
					SessionID: sessionID,
					Type:      session.PartTool,
					Data:      resultData,
					CreatedAt: session.Now(),
					UpdatedAt: session.Now(),
				}
				if err := lr.Store.CreatePart(resultPart); err != nil {
					slog.Error("create tool result part", "err", err)
				}
			}
			lr.Bus.Publish("message.updated", toolResultMsg)
		}
	}

	// Reached MaxSteps with tool calls still pending — treat as stop.
	exitReason = "stop"
	if memoryEnabled {
		lr.writeMemory(ctx, sessionID, p, modelID)
	}
	return nil
}

// writeMemory extracts the last conversation turn and persists it via memory_add.
// chatProvider/chatModel are the session's resolved LLM — memory synthesis uses
// the same model the user is chatting with, captured here at dispatch time.
func (lr *LoopRunner) writeMemory(ctx context.Context, sessionID session.SessionID, chatProvider provider.Provider, chatModel string) {
	messages, err := lr.Store.GetMessages(sessionID, "", 1000)
	if err != nil {
		slog.Warn("writeMemory: failed to load messages", "err", err)
		return
	}

	// Find the last user message that has a text part (skip tool-result user messages)
	var userText string
	var userMsgIdx int
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Info.Role != session.RoleUser || len(messages[i].Parts) == 0 {
			continue
		}
		hasText := false
		for _, p := range messages[i].Parts {
			if p.Type == session.PartText {
				var data session.TextPartData
				if json.Unmarshal(p.Data, &data) == nil && data.Text != "" {
					userText = data.Text
					hasText = true
				}
			}
		}
		if hasText {
			userMsgIdx = i
			break
		}
	}
	if userText == "" {
		slog.Info("writeMemory: no user text found, skipping")
		return
	}

	// Build response trace from assistant messages after the user message
	responseText := buildTurnResponse(messages, userMsgIdx)
	if responseText == "" {
		slog.Info("writeMemory: no response text, skipping")
		return
	}

	slog.Info("writeMemory: persisting turn", "session", sessionID, "questionLen", len(userText), "responseLen", len(responseText))
	var chat memory.ChatClient
	if chatProvider != nil {
		chat = memory.NewChatClient(chatProvider, chatModel)
	}
	lr.Memory.WriteMemory(ctx, string(sessionID), userText, responseText, chat)
}

// buildTurnResponse serializes all assistant messages after a given user message
// into a structured text trace (tool calls, results, text).
func buildTurnResponse(messages []*session.MessageWithParts, userMsgIdx int) string {
	var b strings.Builder
	for i := userMsgIdx + 1; i < len(messages); i++ {
		m := messages[i]
		if m.Info.Role == session.RoleAssistant {
			fmt.Fprintf(&b, "--- Assistant iteration ---\n")
			for _, p := range m.Parts {
				switch p.Type {
				case session.PartText:
					var data session.TextPartData
					if json.Unmarshal(p.Data, &data) == nil && data.Text != "" {
						fmt.Fprintf(&b, "Text: %s\n", data.Text)
					}
				case session.PartTool:
					var data session.ToolPartData
					if json.Unmarshal(p.Data, &data) == nil {
						status := string(data.State.Status)
						fmt.Fprintf(&b, "Tool: %s (%s)\n", data.Tool, status)
						if data.State.Input != nil {
							fmt.Fprintf(&b, "  Input: %s\n", string(data.State.Input))
						}
						if data.State.Output != nil {
							output := *data.State.Output
							if len(output) > 500 {
								output = output[:500] + "..."
							}
							fmt.Fprintf(&b, "  Output: %s\n", output)
						}
						if data.State.Error != nil {
							fmt.Fprintf(&b, "  Error: %s\n", *data.State.Error)
						}
					}
				case session.PartReasoning:
					// Skip reasoning parts — not stored in knowledge graph.
					// Reasoning is ephemeral and should not pollute long-term memory.
				}
			}
		}
	}
	return b.String()
}

// probeImageTimeout bounds the one-time capability probe so a slow/unreachable
// provider can't stall the first turn for long.
const probeImageTimeout = 15 * time.Second

// resolveImageSupport determines whether modelID accepts image input, in order:
//  1. a persisted capability record (probed once, permanent until manual refresh);
//  2. the static catalog for Anthropic/OpenAI, which is authoritative (no probe);
//  3. a one-time live probe for dynamic providers (OpenRouter/Ollama), cached on success;
//  4. the name heuristic as a last resort when the probe is inconclusive (not cached).
func (lr *LoopRunner) resolveImageSupport(ctx context.Context, p provider.Provider, modelID string) bool {
	if modelID == "" || p == nil {
		return false
	}
	database := lr.Store.DB()

	if cap, ok, err := session.GetModelCapability(database, modelID); err == nil && ok {
		return cap.SupportsImages
	}

	// Anthropic and OpenAI ship a curated catalog with known capabilities — trust
	// it directly rather than spending a probe call. Dynamic providers fall through.
	switch p.ID() {
	case "anthropic", "openai":
		return lr.Registry.ModelSupportsImages(modelID)
	}

	pctx, cancel := context.WithTimeout(ctx, probeImageTimeout)
	defer cancel()
	supports, definitive, err := provider.ProbeImageSupport(pctx, p, modelID)
	if err != nil || !definitive {
		slog.Warn("image-support probe inconclusive; using name heuristic",
			"model", modelID, "err", err)
		return lr.Registry.ModelSupportsImages(modelID)
	}

	if serr := session.SetModelCapability(database, &session.ModelCapability{
		ModelID:        modelID,
		SupportsImages: supports,
		ProbedAt:       session.Now(),
	}); serr != nil {
		slog.Warn("failed to persist model capability", "model", modelID, "err", serr)
	}
	slog.Info("probed model image support", "model", modelID, "supportsImages", supports)
	return supports
}

func (lr *LoopRunner) executeTool(ctx context.Context, sessionID session.SessionID, messageID session.MessageID, tc pendingToolCall, a Agent, workDir string, modelSupportsImages bool, model string) (tool.Result, error) {
	// Reject tools not in the agent's allowed list — guards against prompt injection
	// or a misbehaving model calling tools it was never offered.
	if !a.HasTool(tc.Name) {
		slog.Warn("agent called disallowed tool, rejecting", "agent", a.ID, "tool", tc.Name)
		return tool.Result{Output: fmt.Sprintf("tool %q is not available to the %s agent", tc.Name, a.ID)}, nil
	}

	// Try built-in tools first
	t := lr.Tools.Get(tc.Name)
	if t != nil {
		slog.Info("executing built-in tool", "tool", tc.Name)
		tctx := tool.Context{
			SessionID:           sessionID,
			MessageID:           messageID,
			Agent:               a.ID,
			CallID:              tc.CallID,
			Ctx:                 ctx,
			SessionDir:          workDir,
			ModelSupportsImages: modelSupportsImages,
			Model:               model,
		}
		return t.Execute(ctx, tc.Input, tctx)
	}

	// Try MCP tools
	if lr.MCP != nil {
		for name, td := range lr.MCP.Tools() {
			if name == tc.Name {
				slog.Info("executing MCP tool", "tool", name)
				var args map[string]any
				if err := json.Unmarshal(tc.Input, &args); err != nil {
					return tool.Result{}, fmt.Errorf("parse MCP tool input: %w", err)
				}
				output, duration, err := lr.MCP.CallTool(ctx, name, args)
				title := td.Name
				metadata := map[string]any{
					"duration_ms": duration.Milliseconds(),
				}
				if err != nil {
					return tool.Result{Title: title, Output: err.Error(), Metadata: metadata}, err
				}
				return tool.Result{Title: title, Output: output, Metadata: metadata}, nil
			}
		}
	}

	slog.Warn("unknown tool requested", "tool", tc.Name)
	return tool.Result{}, fmt.Errorf("unknown tool: %s", tc.Name)
}

type pendingToolCall struct {
	CallID string
	Name   string
	Input  json.RawMessage
	Ready  bool
	PartID session.PartID
}

// toolNames returns the names of all tool calls for logging.
func toolNames(calls []pendingToolCall) []string {
	names := make([]string, len(calls))
	for i, tc := range calls {
		names[i] = tc.Name
	}
	return names
}

// extractLastAssistantText returns the text from the most recent assistant message
// that contains text but no tool calls (i.e. a final response, not a mid-loop tool step).
// For thinking/reasoning models the synthesis may be in the reasoning part rather than
// the text part — we fall back to reasoning when text is empty.
func extractLastAssistantText(messages []*session.MessageWithParts) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Info.Role != session.RoleAssistant {
			continue
		}
		var text, reasoning string
		hasTool := false
		for _, p := range msg.Parts {
			switch p.Type {
			case session.PartTool:
				hasTool = true
			case session.PartText:
				var data session.TextPartData
				if json.Unmarshal(p.Data, &data) == nil && data.Text != "" {
					text = data.Text
				}
			case session.PartReasoning:
				var data session.ReasoningPartData
				if json.Unmarshal(p.Data, &data) == nil && data.Text != "" {
					reasoning = data.Text
				}
			}
		}
		if hasTool {
			continue
		}
		if text != "" {
			return text
		}
		if reasoning != "" {
			return reasoning
		}
	}
	return ""
}

// sourceEntry represents a collected URL with its title, used to build the
// Sources section for search results.
type sourceEntry struct {
	URL   string
	Title string
}

// extractSearchSources scans all messages in a search session and collects
// unique source URLs from web_search and fetch_page tool calls.
// - fetch_page: extracts URL from the tool input ({"url": "..."})
// - web_search: extracts URLs from the tool output (which lists result URLs)
func extractSearchSources(messages []*session.MessageWithParts) []sourceEntry {
	seen := make(map[string]bool)
	var sources []sourceEntry

	for _, msg := range messages {
		for _, p := range msg.Parts {
			if p.Type != session.PartTool {
				continue
			}
			var data session.ToolPartData
			if json.Unmarshal(p.Data, &data) != nil {
				continue
			}

			switch data.Tool {
			case "fetch_page":
				// Extract URL from input
				var input struct {
					URL string `json:"url"`
				}
				if json.Unmarshal(data.State.Input, &input) == nil && input.URL != "" {
					if !seen[input.URL] {
						seen[input.URL] = true
						title := ""
						if data.State.Title != nil {
							title = *data.State.Title
						}
						sources = append(sources, sourceEntry{URL: input.URL, Title: title})
					}
				}

			case "web_search":
				// Extract URLs from the output text (which contains "URL: https://..." lines)
				if data.State.Output != nil {
					for _, u := range extractURLsFromText(*data.State.Output) {
						if !seen[u] {
							seen[u] = true
							sources = append(sources, sourceEntry{URL: u})
						}
					}
				}
			}
		}
	}

	return sources
}

// extractURLsFromText finds all URLs in text that appear after "URL: " markers
// in the web_search tool output format.
func extractURLsFromText(text string) []string {
	var urls []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		// Match "URL: https://..." pattern from search results
		if after, ok := strings.CutPrefix(line, "URL:"); ok {
			u := strings.TrimSpace(after)
			if strings.HasPrefix(u, "http") {
				urls = append(urls, u)
			}
		}
	}
	return urls
}

// hasSourcesSection checks whether the answer already contains a Sources section
// (so we don't duplicate it if the LLM included one).
func hasSourcesSection(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.Contains(lower, "## sources") ||
		strings.Contains(lower, "**sources**") ||
		strings.Contains(lower, "### sources")
}

// formatSources builds a markdown bullet list of sources with titles when available.
func formatSources(sources []sourceEntry) string {
	var sb strings.Builder
	for i, s := range sources {
		if s.Title != "" {
			fmt.Fprintf(&sb, "%d. [%s](%s)\n", i+1, s.Title, s.URL)
		} else {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, s.URL)
		}
	}
	return sb.String()
}

func shouldBreak(messages []*session.MessageWithParts) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	if last.Info.Role != session.RoleAssistant {
		return false
	}
	if last.Info.Finish != nil {
		f := *last.Info.Finish
		// "stop" / "end_turn" — natural completion (Anthropic uses "end_turn")
		// "length" / "max_tokens" — hit token limit, do not keep looping
		// "error" / "aborted" — terminal states
		return f == "stop" || f == "end_turn" ||
			f == "length" || f == "max_tokens" ||
			f == "error" || f == "aborted"
	}
	return false
}

func lastUserMessageID(messages []*session.MessageWithParts) *session.MessageID {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Info.Role == session.RoleUser {
			id := messages[i].Info.ID
			return &id
		}
	}
	return nil
}

// findLastTextUserMessageIndex scans backwards for the most recent user
// message that contains at least one text part (not just tool results).
// It returns the index or -1 when none is found.
func findLastTextUserMessageIndex(messages []*session.MessageWithParts) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Info.Role != session.RoleUser {
			continue
		}
		for _, p := range messages[i].Parts {
			if p.Type == session.PartText {
				var data session.TextPartData
				if json.Unmarshal(p.Data, &data) == nil && data.Text != "" {
					return i
				}
			}
		}
	}
	return -1
}

func toProviderMessages(messages []*session.MessageWithParts, memoryText string) []provider.ModelMessage {
	// When memory is active, filter to only the last user message and everything after.
	// This replaces full history with the compressed <prior_context> block.
	if memoryText != "" {
		// Find the last user message index
		lastUserIdx := -1
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Info.Role == session.RoleUser {
				// Skip tool-result user messages (they have tool parts)
				hasText := false
				for _, p := range messages[i].Parts {
					if p.Type == session.PartText {
						hasText = true
					}
				}
				if hasText {
					lastUserIdx = i
					break
				}
			}
		}

		if lastUserIdx >= 0 {
			// Include everything from the last text-user message onwards
			// plus any preceding tool-result messages (for ongoing tool chains)
			filtered := messages[lastUserIdx:]

			// Prepend <prior_context> to the first user message
			result := convertMessages(filtered)

			// Find the first user message and prepend context
			for i, msg := range result {
				if msg.Role == "user" {
					var content string
					if msg.Content != nil {
						json.Unmarshal(msg.Content, &content)
					}
					content = "<prior_context>\n" + memoryText + "\n</prior_context>\n\n" + content
					result[i].Content, _ = json.Marshal(content)
					break
				}
			}

			return result
		}
	}

	// No memory: send the full conversation history and let the model's context
	// window be the limit. Memory mode is the right solution for long sessions.
	return convertMessages(messages)
}

func convertMessages(messages []*session.MessageWithParts) []provider.ModelMessage {
	var result []provider.ModelMessage
	for _, m := range messages {
		// Collect text and tool parts
		var textParts []string
		var toolCallParts []session.ToolPartData
		var toolResultParts []session.ToolPartData

		for _, p := range m.Parts {
			switch p.Type {
			case session.PartText:
				var data session.TextPartData
				json.Unmarshal(p.Data, &data)
				if data.Text != "" {
					textParts = append(textParts, data.Text)
				}
			case session.PartTool:
				var data session.ToolPartData
				json.Unmarshal(p.Data, &data)
				if m.Info.Role == session.RoleAssistant {
					toolCallParts = append(toolCallParts, data)
				} else {
					toolResultParts = append(toolResultParts, data)
				}
			}
		}

		if m.Info.Role == session.RoleAssistant && len(toolCallParts) > 0 {
			// Assistant message with tool calls: emit as a single message with tool_calls array
			type oaiToolCall struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}

			var calls []oaiToolCall
			for _, tc := range toolCallParts {
				call := oaiToolCall{
					ID:   tc.CallID,
					Type: "function",
				}
				call.Function.Name = tc.Tool
				// Arguments must be a JSON string (the OpenAI API expects a string, not a raw object)
				if tc.State.Input != nil {
					call.Function.Arguments = string(tc.State.Input)
				} else {
					call.Function.Arguments = "{}"
				}
				calls = append(calls, call)
			}
			toolCallsJSON, _ := json.Marshal(calls)

			msg := provider.ModelMessage{
				Role:      "assistant",
				ToolCalls: toolCallsJSON,
			}
			if len(textParts) > 0 {
				msg.Content, _ = json.Marshal(strings.Join(textParts, ""))
			}
			result = append(result, msg)
		} else if m.Info.Role == session.RoleUser && len(toolResultParts) > 0 {
			// User message with tool results: emit each as a separate role=tool message
			for _, tr := range toolResultParts {
				output := ""
				if tr.State.Output != nil {
					output = *tr.State.Output
				} else if tr.State.Error != nil {
					output = "Error: " + *tr.State.Error
				}
				content, _ := json.Marshal(output)
				msg := provider.ModelMessage{
					Role:       "tool",
					Content:    content,
					ToolCallID: tr.CallID,
					Name:       tr.Tool,
				}
				if tr.State.Image != nil {
					msg.Images = []provider.MessageImage{{
						MediaType: tr.State.Image.MediaType,
						Data:      tr.State.Image.Data,
					}}
				}
				result = append(result, msg)
			}
		} else {
			// Plain text message
			if len(textParts) > 0 {
				content, _ := json.Marshal(strings.Join(textParts, ""))
				result = append(result, provider.ModelMessage{
					Role:    string(m.Info.Role),
					Content: content,
				})
			}
		}
	}
	return result
}

func buildSystemPrompt(a Agent, dir string, memoryEnabled bool, agentMDContent string, memoryMDContent string, mcpTools map[string]mcp.ToolDef, viewportWidth int, viewportHeight int) string {
	prompt := fmt.Sprintf(`%s

Working directory: %s
Platform: %s/%s`, a.System, dir, runtime.GOOS, runtime.GOARCH)

	if agentMDContent != "" {
		prompt += agentMDContent
	}

	if memoryMDContent != "" {
		prompt += memoryMDContent
	}

	// MEMORY.md section: role-aware instructions based on whether the agent
	// can write files. BuildAgent gets full read/write maintenance instructions;
	// read-only agents (Plan, Note) get read-only guidance.
	canWriteFiles := a.HasTool("write") || a.HasTool("edit")
	prompt += "\n\n" + memoryMDPrompt(canWriteFiles)

	// When no MEMORY.md exists and the agent can create one, prompt it to do so.
	if memoryMDContent == "" && canWriteFiles {
		prompt += "\n\nNo MEMORY.md file was found in this project. You should create one in the project root directory using the write tool if the project has any meaningful knowledge to record."
	}

	// Inject MCP skill descriptions (excluding memory tools)
	if len(mcpTools) > 0 {
		prompt += "\n\n## Available Skills\n\n"
		prompt += "You have access to specialized tools via MCP. Use them proactively when relevant:\n\n"

		skillSections := []struct {
			heading string
			tools   []skillDesc
		}{
			{
				heading: "### Codebase Research",
				tools: []skillDesc{
					{name: "answer_codebase", tip: "PREFERRED for grounded, synthesized answers about committed code with file paths and line citations."},
					{name: "query_codebase", tip: "For targeted search across the committed index (semantic, pattern, or hybrid)."},
					{name: "cache_map", tip: "Use first to understand what a source contains before deeper queries."},
				},
			},
			{
				heading: "### Live / Uncommitted Changes",
				tools: []skillDesc{
					{name: "hot_answer", tip: "PREFERRED for Q&A about uncommitted working-tree edits. Use when user asks about 'my changes' or recent local modifications."},
					{name: "hot_query", tip: "For real-time search across uncommitted changes."},
				},
			},
			{
				heading: "### Multi-Repo (Tracks)",
				tools: []skillDesc{
					{name: "track_answer", tip: "For synthesized answers across multiple repos in a Track."},
					{name: "track_query", tip: "For targeted search across all sources in a Track."},
					{name: "hot_track_answer", tip: "For Q&A across live uncommitted changes in a Track."},
					{name: "hot_track_query", tip: "For real-time search across live changes in a Track."},
				},
			},
		}

		for _, section := range skillSections {
			hasTool := false
			for _, s := range section.tools {
				if _, ok := mcpTools[s.name]; ok {
					hasTool = true
					break
				}
			}
			if !hasTool {
				continue
			}
			prompt += section.heading + "\n"
			for _, s := range section.tools {
				if _, ok := mcpTools[s.name]; ok {
					prompt += fmt.Sprintf("- **%s**: %s\n", s.name, s.tip)
				}
			}
			prompt += "\n"
		}
	}

	if memoryEnabled {
		prompt += `

You have access to agentic memory. Prior conversation context is provided in <prior_context> blocks, which includes a knowledge graph summary of past sessions and the most recent assistant response for continuity.

To retrieve specific past facts, decisions, or details, use the memory_recall tool with a precise question. Use it proactively whenever the current query references past context, prior decisions, or earlier work — do not guess or hallucinate past details.`
	}

	// Inject LaTeX environment info for agents that have the latex_to_pdf tool.
	if a.HasTool("latex_to_pdf") {
		prompt += latexInfoPrompt()
	}

	// Inject viewport dimensions so agents can make responsive design decisions.
	prompt += viewportPrompt(viewportWidth, viewportHeight)

	return prompt
}

type skillDesc struct {
	name string
	tip  string
}

func mustMarshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	data, _ := json.Marshal(v)
	return data
}

// isTransientError returns true for errors that are worth retrying
// (rate limits, timeouts, connection resets, server errors).
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	// Rate limiting
	if strings.Contains(lower, "rate limit") || strings.Contains(lower, "429") {
		return true
	}
	// Server errors
	if strings.Contains(lower, "500") || strings.Contains(lower, "502") || strings.Contains(lower, "503") || strings.Contains(lower, "504") {
		return true
	}
	// Connection-level issues
	if strings.Contains(lower, "connection reset") || strings.Contains(lower, "eof") ||
		strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "refused") || strings.Contains(lower, "temporary") {
		return true
	}
	// Anthropic overloaded
	if strings.Contains(lower, "overloaded") {
		return true
	}
	return false
}

// ensureStartsWithUser trims leading messages until the slice starts with a "user"
// role message, which is required for valid LLM conversation structure.
func ensureStartsWithUser(messages []provider.ModelMessage) []provider.ModelMessage {
	for len(messages) > 0 && messages[0].Role != "user" {
		messages = messages[1:]
	}
	return messages
}

// isContextLengthError returns true when the provider rejects the request because
// the prompt exceeds the model's maximum context window.
func isContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "too long") ||
		strings.Contains(lower, "context length") ||
		strings.Contains(lower, "maximum context") ||
		strings.Contains(lower, "context_length_exceeded") ||
		strings.Contains(lower, "prompt is too long") ||
		// Ollama returns a bare 400 with empty body when the prompt exceeds
		// the model's context window. The error format is "ollama API error 400: "
		// (with empty body after the colon-space) — treat this as context length
		// exceeded rather than a generic client error. We check for the empty body
		// specifically to avoid matching other 400 errors that have error details.
		(strings.Contains(lower, "api error 400") && strings.HasSuffix(strings.TrimSpace(lower), "400:"))
}

// llmCompact summarizes the older portion of the conversation using the LLM itself,
// producing a rich technical summary that replaces the trimmed middle section.
// It returns a system-prompt addendum containing the summary and the recent messages
// to keep verbatim. Falls back to mechanical truncation if the LLM call fails.
func (lr *LoopRunner) llmCompact(ctx context.Context, p provider.Provider, modelID string, messages []provider.ModelMessage) (systemAddendum string, compacted []provider.ModelMessage) {
	const keepRecent = 12
	if len(messages) <= keepRecent {
		return "", messages
	}

	oldMessages := messages[:len(messages)-keepRecent]
	recent := messages[len(messages)-keepRecent:]

	// Ensure recent starts with a user message (required for valid conversation structure)
	recent = ensureStartsWithUser(recent)
	if len(recent) == 0 {
		return "", compactMessagesTruncate(messages)
	}

	// Render old messages into readable text for the summarizer
	var history strings.Builder
	for _, m := range oldMessages {
		var content string
		if m.Content != nil {
			json.Unmarshal(m.Content, &content)
		}
		switch {
		case m.Role == "tool":
			if content != "" {
				out := content
				if len(out) > 800 {
					out = out[:800] + "...(truncated)"
				}
				fmt.Fprintf(&history, "[tool result %s]: %s\n\n", m.Name, out)
			}
		case m.ToolCalls != nil:
			fmt.Fprintf(&history, "[assistant tool calls]: %s\n\n", string(m.ToolCalls))
		case content != "":
			fmt.Fprintf(&history, "[%s]: %s\n\n", m.Role, content)
		}
	}

	if history.Len() == 0 {
		return "", recent
	}

	historyText := history.String()
	if len(historyText) > 30_000 {
		historyText = historyText[:30_000] + "\n...(older history truncated)"
	}

	summarizerPrompt := "Summarize the following conversation history concisely. Capture: the original task/goal, " +
		"key decisions made, files modified (with paths), code written or changed, errors encountered " +
		"and how they were resolved, and the current state of work. Be specific about technical details — " +
		"file paths, function names, variable names, commands run. This summary will replace the full " +
		"history so the conversation can continue within the model's context window." +
		"\n\n" + historyText

	// json.Marshal properly encodes the combined string, including any special
	// characters in historyText, producing a valid JSON string value for Content.
	summarizerContent, _ := json.Marshal(summarizerPrompt)

	summaryReq := provider.StreamRequest{
		Model: modelID,
		System: []string{
			"You are a precise technical conversation summarizer. Produce a concise but complete summary " +
				"that preserves all technical details needed to continue the work effectively.",
		},
		Messages: []provider.ModelMessage{
			{Role: "user", Content: summarizerContent},
		},
		Abort: ctx,
	}

	ch, err := p.StreamChat(ctx, summaryReq)
	if err != nil {
		slog.Warn("llm compact: summary call failed, falling back to truncation", "err", err)
		return "", compactMessagesTruncate(messages)
	}

	var summary strings.Builder
	for evt := range ch {
		if evt.Type == provider.EventTextDelta {
			summary.WriteString(evt.Text)
		}
	}

	if summary.Len() == 0 {
		slog.Warn("llm compact: empty summary received, falling back to truncation")
		return "", compactMessagesTruncate(messages)
	}

	addendum := "\n\n## Compacted Conversation History (AI-generated summary)\n\n" +
		"Earlier conversation exchanges were trimmed to fit the context window. " +
		"The following summary is authoritative — treat it as what has already been done:\n\n" +
		summary.String()

	return addendum, recent
}

// compactMessagesTruncate is the mechanical fallback: keeps the original first
// message and the most recent exchanges, discarding the middle verbatim.
func compactMessagesTruncate(messages []provider.ModelMessage) []provider.ModelMessage {
	const keepRecent = 15
	if len(messages) <= keepRecent+1 {
		return messages
	}

	original := messages[0]
	recent := messages[len(messages)-keepRecent:]

	recent = ensureStartsWithUser(recent)
	if len(recent) == 0 {
		return messages
	}

	note := "[Context auto-compacted: earlier conversation omitted. Original request preserved above.] "
	var existingContent string
	if recent[0].Content != nil {
		json.Unmarshal(recent[0].Content, &existingContent)
	}
	annotated := recent[0]
	annotated.Content, _ = json.Marshal(note + existingContent)

	result := make([]provider.ModelMessage, 0, len(recent)+1)
	result = append(result, original)
	result = append(result, annotated)
	result = append(result, recent[1:]...)
	return result
}

// RunSearchSession creates an ephemeral search-agent session, runs the full loop,
// and returns the synthesised assistant text. The session is deleted on completion.
// This is called by tool.DeepSearchTool via the tool.DeepSearchFunc contract.
func (lr *LoopRunner) RunSearchSession(ctx context.Context, query, dir, model string) (string, error) {
	if dir == "" {
		dir = lr.Dir
	}
	if model == "" {
		dp := lr.Registry.Default()
		if dp == nil {
			dp = lr.DefaultProvider
		}
		if dp != nil {
			models := dp.Models()
			if len(models) > 0 {
				model = models[0].ID
			}
		}
	}

	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   dir,
		Directory:   dir,
		Title:       "Search: " + truncateText(query, 60),
		Model:       model,
		SessionType: "search",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := lr.Store.Create(sess); err != nil {
		return "", fmt.Errorf("create search session: %w", err)
	}

	// Always clean up the ephemeral session when done.
	defer func() {
		if err := lr.Store.Delete(sess.ID); err != nil {
			slog.Warn("delete ephemeral search session", "session", sess.ID, "err", err)
		}
	}()

	// Create the initial user message.
	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: sess.ID,
		Role:      session.RoleUser,
		Agent:     "search",
		CreatedAt: session.Now(),
	}
	if err := lr.Store.CreateMessage(userMsg); err != nil {
		return "", fmt.Errorf("create search user message: %w", err)
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
	if err := lr.Store.CreatePart(userPart); err != nil {
		return "", fmt.Errorf("create search user part: %w", err)
	}

	// Run a capped child loop — search sessions need at most 5 turns
	// (decompose → web_search → fetch_page → synthesise ± one extra round).
	// Without a cap, a misbehaving LLM could spin for 1000 steps.
	// The search agent prompt instructs the model to combine search and
	// fetch into 2 LLM rounds where possible, so 8 steps gives ample
	// room (each round = 1 user + 1 assistant + potential tool-result msg).
	childRunner := *lr
	childRunner.MaxSteps = 8
	if err := childRunner.RunLoop(ctx, sess.ID, "search", 0, 0); err != nil {
		return "", fmt.Errorf("search loop: %w", err)
	}

	// Extract the final synthesised assistant text.
	msgs, err := lr.Store.GetMessages(sess.ID, "", 1000)
	if err != nil {
		return "", fmt.Errorf("load search messages: %w", err)
	}
	answer := extractLastAssistantText(msgs)
	if strings.TrimSpace(answer) == "" {
		return "The search agent did not produce a final answer (it may have run out of steps or every page fetch failed). Try a narrower query.", nil
	}

	// Collect source URLs from all web_search and fetch_page tool calls in the
	// session. This guarantees a Sources section even if the LLM forgets to
	// include one in its final text.
	sources := extractSearchSources(msgs)
	if len(sources) > 0 && !hasSourcesSection(answer) {
		answer += "\n\n## Sources\n\n" + formatSources(sources)
	}

	return answer, nil
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// estimateRequestSize provides a rough byte-size estimate of the request that
// will be serialized and sent to the LLM provider. It walks the messages and
// system prompts, summing content lengths, so it is fast and allocation-free.
// The estimate is deliberately conservative (over-counts) to trigger proactive
// compaction before the actual serialized body hits the model's context limit.
func estimateRequestSize(req provider.StreamRequest) int {
	size := 0
	for _, s := range req.System {
		size += len(s)
	}
	for _, m := range req.Messages {
		size += len(m.Role)
		if m.Content != nil {
			size += len(m.Content)
		}
		if m.ToolCalls != nil {
			size += len(m.ToolCalls)
		}
		if m.ToolCallID != "" {
			size += len(m.ToolCallID)
		}
		if m.Name != "" {
			size += len(m.Name)
		}
		for _, img := range m.Images {
			size += len(img.Data)
		}
	}
	// Tools JSON is also serialized but relatively small; skip for now.
	return size
}
