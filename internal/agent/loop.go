package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/id"
	"github.com/prasenjeet-symon/ogcode/internal/memory"
	"github.com/prasenjeet-symon/ogcode/internal/note"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/project"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/search"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/skill"
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
	NoteStore       *note.Store
	// SearchBridge is the web-search backend used by the deep-research pipeline
	// (RunSearchSession) and by web_search and fetch_page. nil when search is
	// disabled — deep_search is only registered when it is non-nil.
	SearchBridge search.Backend
	// SearchParams, when set, returns the current deep-research tuning read fresh
	// from the global config DB, so settings-screen changes take effect on the next
	// deep_search without a restart. nil → built-in defaults.
	SearchParams func() session.SearchConfig
	// IndexedFileCount, when set, reports how many files the project index holds
	// for a directory. It lets the system prompt state up front whether
	// codebase_map has anything to return, instead of making every session in an
	// unindexed project spend a call discovering that it does not. A closure
	// rather than the store itself so this package keeps no dependency on
	// docindex. nil (CLI, tests) leaves the prompt silent and the agent probing,
	// which is the behaviour that predates this field.
	IndexedFileCount func(dir string) int
	// Permissions gates mutating tool calls (bash/write/edit) behind user
	// approval. nil disables gating entirely (CLI, tests). Even when set, a loop
	// only prompts when its context carries WithPermissionGating — so headless
	// runs (task, breakdown, note, search) never block on an approval UI.
	Permissions *permission.Manager
	// Skills resolves the skills available in a project directory. nil (CLI,
	// tests) means no skill is ever listed and the skill tool has nothing to
	// load, which is the behaviour that predates the feature.
	Skills *skill.Loader
}

// RunLoop executes the core agent loop: prompt -> stream -> tools -> loop back.
// maxReasoningLen bounds how much of a thinking block is stored, so a runaway
// reasoning stream can't flood the DB or the UI.
const maxReasoningLen = 50_000

// The error is named so the deferred loop.done publish can report it. Every
// `return someErr` in the body assigns to it regardless of how `err` is shadowed
// in inner scopes, which is why the publish can stay a single defer up here
// rather than being threaded through a dozen exit paths.
func (lr *LoopRunner) RunLoop(ctx context.Context, sessionID session.SessionID, agentName string, viewportWidth int, viewportHeight int) (runErr error) {
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
	//
	// The reason alone is not enough. Several exit paths fail before an
	// assistant message exists to carry the error — get session, load messages,
	// create assistant message — and their error used to reach nothing but a
	// slog line in the server's stdout. The client saw a loop that stopped for
	// no stated reason. Carry the text so it has something to show.
	exitReason := "error"
	defer func() {
		payload := map[string]string{
			"sessionId": string(sessionID),
			"reason":    exitReason,
		}
		if runErr != nil {
			payload["error"] = runErr.Error()
		}
		lr.Bus.Publish("loop.done", payload)
	}()

	// Registered after the publish defer, so it runs BEFORE it (LIFO) and the
	// publish above reports the panic instead of a bare "error".
	//
	// Tool execution already recovers (see executeReadyToolCalls) and the task
	// runner recovers around its own RunLoop call, but the interactive loop had
	// no recover anywhere on the path: a panic in prompt assembly, message
	// conversion or compaction took the whole server down with it, which the
	// user experiences as every session going silent at once. Recovering here
	// covers every caller rather than one route.
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		runErr = fmt.Errorf("agent loop panicked: %v", r)
		exitReason = "panic"
		slog.Error("agent loop panicked",
			"session", sessionID, "agent", agentName,
			"panic", r, "stack", string(debug.Stack()))

		// A panic mid-turn can leave the assistant message holding a tool_use
		// with no matching tool_result. Every other abnormal exit reconciles
		// (see the stream-error paths below); skipping it here would make the
		// crash keep costing turns after it was survived, because the next
		// prompt builds a request the provider rejects with a 400.
		//
		// Guarded on its own: the store is a plausible cause of the panic being
		// recovered, and a second panic raised inside this deferred function
		// would escape it and take down the process — which is the one outcome
		// this whole block exists to prevent.
		func() {
			defer func() {
				if rr := recover(); rr != nil {
					slog.Error("agent loop: reconcile after panic panicked too",
						"session", sessionID, "panic", rr)
				}
			}()
			if _, rerr := lr.ReconcileSession(sessionID); rerr != nil {
				slog.Warn("reconcile after panic", "session", sessionID, "err", rerr)
			}
		}()
	}()

	// Resolve provider based on session's model
	sess, err := lr.Store.Get(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	// Warn if the agent name doesn't match the session type — catches accidental
	// mismatches at call sites (breakdown intentionally differs, so not a hard error).
	if sess != nil && sess.SessionType != "" && sess.SessionType != agentName {
		// The breakdown agent and task-execution agent both run on sessions
		// created with SessionType "build" — these are expected, not bugs.
		knownMismatch := (agentName == "breakdown" || agentName == "task") && sess.SessionType == "build"
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

	// Skills are resolved once per turn, alongside AGENT.md and MEMORY.md, and
	// the same list is used for every step of it. Only agents holding the tool
	// pay for the scan — for the rest the listing would name a call they were
	// never offered.
	var visibleSkills []skill.Skill
	if lr.Skills != nil && agent.HasTool("skill") {
		reg := lr.Skills.Load(workDir)
		visibleSkills = reg.Visible()
		// Config-driven ask/deny rules have to be in the session's ruleset
		// before the first skill call, or the default catch-all Allow answers
		// for them. Seeding is idempotent, so repeating it per turn is free and
		// never clobbers an "always allow" the user has since granted.
		if lr.Permissions != nil {
			lr.Permissions.EnsureRules(string(sessionID), skillPermissionRules(reg))
		}
	}

	// Resolved once per turn rather than per step: the prompt is rebuilt on every
	// step, and the index only changes when the user deliberately rebuilds it, so
	// a query per step would buy accuracy nobody can observe. -1 means unreported.
	indexedFiles := -1
	if lr.IndexedFileCount != nil {
		indexedFiles = lr.IndexedFileCount(workDir)
	}

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

	// Resolve the provider/model and the two fixed per-run model attributes.
	p, modelID, modelSupportsImages, modelContextWindow := lr.resolveRunModel(ctx, sess, sessionID)

	// Whether this endpoint serves a repeated prefix from a cache. Seeded from
	// whatever is already known about it, so only the first turn against a new
	// endpoint pays the observation window; the verdict is handed back when the
	// turn ends, however it ends.
	cacheObs := newCacheObserver(lr.Store.DB(), p, modelID)
	defer func() { rememberCacheVerdict(lr.Store.DB(), p, modelID, cacheObs.Verdict()) }()
	compactionThreshold := compactionThresholdTokens(modelContextWindow)
	// The model's output ceiling (0 = unknown). Sent as the per-request output
	// budget so long responses aren't truncated by a provider's conservative
	// default — see outputTokenBudget.
	modelMaxOutput := 0
	if lr.Registry != nil {
		modelMaxOutput = lr.Registry.MaxOutputTokens(modelID)
	}

	// lastInputTokens is the provider-reported input-side token count from the
	// previous step's response (Input + CacheRead + CacheWrite) — the exact size
	// the model processed. The proactive-compaction check prefers it over the
	// byte estimate once a step has completed; it stays 0 on the first step,
	// where the estimate is the only signal available.
	lastInputTokens := 0

	// Restore compaction summary from a previous turn (persisted in the session row).
	// Applied on every step so all future LLM calls stay within the context window.
	compactionSummary := ""

	// In-turn compaction: what the agent has summarized away of its own work
	// this turn. Only ever set on endpoints that do not cache a repeated prefix,
	// where re-sending the history costs full price on every step.
	var watermark compactionWatermark

	// How much file, search, and page content the agent has pulled into this turn,
	// and whether that has reached the point of reminding it that compact_context
	// exists. Sized against the same threshold the loop's own compaction uses, so
	// the reminder means the same thing on a small local model and a large hosted one.
	pressure := newReadPressure(compactionThreshold)
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

	// In-memory working set of the conversation. The DB stays the durable log
	// (all streaming/tool writes go there), but re-reading and re-unmarshaling the
	// entire history on every iteration is wasteful on long, many-step turns.
	// Instead we load the full history once, then fold in only the messages this
	// loop creates, identified by their known IDs. We deliberately do NOT use a
	// time/ID cursor: the message ULIDs are millisecond-timestamped with
	// non-monotonic entropy, so two messages in the same millisecond can sort in
	// either order — a "> lastID" delta fetch could silently skip one. Known-ID
	// folding is exact and preserves creation order; a message deleted mid-turn
	// (an orphaned, guidance-cancelled assistant) returns (nil,nil) from
	// GetMessage and is skipped, so the working set matches what a full reload
	// would produce. Draining at the top of the loop covers every loop-back path.
	const workingSetCap = 1000
	var messages []*session.MessageWithParts
	messagesLoaded := false
	var newMessageIDs []session.MessageID // created in the prior iteration, folded in next

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

		// Drain any mid-loop guidance that was injected since the last iteration.
		// This is the ephemeral side-channel: the user can send new instructions
		// while the loop is running. We drain them here and append them to the
		// user's turn message content on the upcoming LLM call. The guidance is
		// never persisted to the message DB, so it does not interact with
		// compaction turn boundaries or agentic-memory <prior_context> slicing.
		pendingGuidance := ""
		if lc := LoopControlFromContext(ctx); lc != nil {
			pendingGuidance = lc.DrainGuidance()
			if pendingGuidance != "" {
				slog.Info("mid-loop guidance injected", "session", sessionID, "step", step, "len", len(pendingGuidance))
				lr.Bus.Publish("loop.guidance", map[string]string{
					"sessionId": string(sessionID),
					"status":    "delivered",
				})
			}
		}

		// Refresh the in-memory working set. First iteration: full load (retry on
		// DB contention). Subsequent iterations: fold in the messages the previous
		// iteration created, by known ID and in creation order.
		if !messagesLoaded {
			for dbAttempt := 0; dbAttempt < 3; dbAttempt++ {
				messages, err = lr.Store.GetMessages(sessionID, "", workingSetCap)
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
			messagesLoaded = true
		} else {
			for _, mid := range newMessageIDs {
				m, gerr := lr.Store.GetMessage(mid)
				if gerr != nil {
					// A transient read failure would drop a message from the working
					// set and desync the conversation, so fall back to a full reload.
					slog.Warn("fold message into working set failed; reloading", "session", sessionID, "message", mid, "err", gerr)
					messages, err = lr.Store.GetMessages(sessionID, "", workingSetCap)
					if err != nil {
						return fmt.Errorf("reload messages: %w", err)
					}
					break
				}
				if m != nil { // nil ⇒ deleted (orphaned assistant) — skip
					messages = append(messages, m)
				}
			}
			// Bound the working set to the most recent messages, mirroring the old
			// full-reload window; compaction bounds the actual request size.
			if len(messages) > workingSetCap {
				messages = messages[len(messages)-workingSetCap:]
			}
		}
		newMessageIDs = newMessageIDs[:0]

		// Check if we should continue: last assistant finished means done
		if shouldBreak(messages) {
			// Don't break if there is pending guidance — the user injected a
			// new instruction while the loop was running and we just drained
			// it at the top of this iteration. If we broke here the guidance
			// would be silently discarded because it is never persisted to the
			// message DB (it's an ephemeral append to the user message content).
			// Continue so the guidance reaches the LLM on this iteration's call.
			if pendingGuidance == "" {
				// Re-check: guidance may have arrived after DrainGuidance at
				// the top of this iteration but before/during the DB load
				// above. Without this re-check the loop would exit and silently
				// drop the guidance (it is never persisted to the message DB).
				// Drain it here so the loop continues and injects it into the
				// upcoming user message.
				if lc := LoopControlFromContext(ctx); lc != nil {
					pendingGuidance = lc.DrainGuidance()
				}
			}
			if pendingGuidance == "" {
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
			slog.Info("agent loop continuing for pending guidance", "session", sessionID, "step", step, "len", len(pendingGuidance))
		}

		// Create new assistant message
		assistantID := session.MessageID(id.NewMessageID())
		// The message this turn is answering. Its CreatedAt is when the server
		// accepted the prompt, which is where QueuedMs is measured from — from
		// step 2 on that is the loop's own tool-result message, so QueuedMs then
		// measures the gap between tool results and the next dispatch.
		parentID, parentCreatedAt := lastUserMessageRef(messages)
		assistantMsg := &session.MessageInfo{
			ID:        assistantID,
			SessionID: sessionID,
			Role:      session.RoleAssistant,
			Agent:     agent.ID,
			ParentID:  parentID,
			CreatedAt: session.Now(),
		}
		if err := lr.Store.CreateMessage(assistantMsg); err != nil {
			return fmt.Errorf("create assistant message: %w", err)
		}
		// Track for folding into the next iteration's working set. If this message
		// is later orphaned and deleted, GetMessage returns nil and it's skipped.
		newMessageIDs = append(newMessageIDs, assistantID)

		// Resolve tools for this agent
		toolIDs := agent.Tools
		// compact_context earns its round trip only when a repeated prefix is
		// re-billed in full. On a caching endpoint compacting is a net loss: it
		// invalidates the cached prefix, so the next request re-establishes the
		// whole thing at full price. Withheld while the verdict is still unknown.
		compactContextOffered := cacheObs.Verdict() == provider.CacheAbsent && canCompactContext(agent)
		// The read-pressure reminder tells the agent to call compact_context, so it
		// may only be attached on a step where the agent was actually handed it.
		pressure.setOffered(compactContextOffered)
		if compactContextOffered {
			toolIDs = append(append([]string{}, toolIDs...), "compact_context")
		}
		// The agent as this step actually sees it. executeTool checks its Tools
		// as an allowlist, so it must match what was offered on the wire.
		effectiveAgent := agent
		effectiveAgent.Tools = toolIDs
		agentTools := lr.Tools.ForAgent(toolIDs)
		providerTools := make([]provider.ToolDefinition, 0, len(agentTools))
		for _, t := range agentTools {
			providerTools = append(providerTools, provider.ToolDefinition{
				Name:        t.ID(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			})
		}

		slog.Info("resolved tools", "count", len(providerTools))

		// Build system prompt, tuned to the model's family (Claude / GPT / Gemini /
		// local). The family is fixed for the session, so this stays cache-stable.
		providerID := ""
		if p != nil {
			providerID = p.ID()
		}
		// Entry [0] is static within a session so it stays byte-for-byte identical
		// across turns; per-turn content (viewport, date) follows as separate
		// entries. This separation is critical for Anthropic prompt caching: the
		// provider puts the cache_control breakpoint on the first system block
		// only, so anything that changes mid-session must stay out of it.
		systemPrompts := buildSystemPromptEntries(agent, workDir, memoryEnabled, agentMDContent, memoryMDContent, viewportWidth, viewportHeight, modelFamily(providerID, modelID), indexedFiles)
		var modelMessages []provider.ModelMessage

		if memoryEnabled {
			// Agentic memory path: memory handles context compression by filtering
			// history to the last user message and injecting <prior_context>.
			// Compaction is completely bypassed when memory is active — but
			// <prior_context> compresses across turns, not within one, so the
			// in-turn watermark still applies here.
			visible := messages
			if start := watermark.sliceStart(messages, 0); start > 0 {
				visible = messages[start:]
			}
			modelMessages = toProviderMessages(visible, memoryText, modelSupportsImages, modelID)
		} else {
			// Compaction path (memory disabled): automatic compaction operates on
			// user-turn boundaries, not individual tool steps, so the current user
			// turn (from the last text-user message forward) is sent intact unless
			// the agent has narrowed it itself via compact_context. Previous turns
			// are represented by the compactionSummary injected into the system
			// prompt so the model never loses the thread of the session.
			turnStartIdx := findLastTextUserMessageIndex(messages)
			if turnStartIdx >= 0 && turnStartIdx < len(messages) {
				modelMessages = toProviderMessages(messages[watermark.sliceStart(messages, turnStartIdx):], "", modelSupportsImages, modelID)
			} else {
				modelMessages = toProviderMessages(messages, "", modelSupportsImages, modelID)
			}
			if compactionSummary != "" {
				systemPrompts = append(systemPrompts, compactionSummary)
			}
		}
		if watermark.active() {
			modelMessages = prependCompactionSummary(modelMessages, watermark.summary)
		}
		// Kept out of the cacheable base: whether this agent holds compact_context
		// is resolved mid-turn by observation, so it can flip between steps.
		// Anything that changes mid-session must stay out of entry [0].
		if compactContextOffered {
			systemPrompts = append(systemPrompts, compactContextPrompt())
		}
		// Out here rather than in entry [0] for the same reason: the user can
		// add or edit a skill while the session is open, and the cached prefix
		// must stay byte-identical across the whole session.
		if guidance := skillGuidancePrompt(visibleSkills); guidance != "" {
			systemPrompts = append(systemPrompts, guidance)
		}

		// Mid-loop guidance: appended to the user's turn message content (not the
		// system prompt) so the model sees it as additional user input within the
		// current turn, not as a system directive. The guidance accumulates across
		// iterations — DrainGuidance (called above) moved the fresh batch into the
		// delivered accumulator, and DeliveredGuidance returns the full accumulated
		// set for this loop run. We re-append the full set on every iteration so
		// the model continuously sees all guidance the user has sent during this
		// turn. Ephemeral — never persisted to the DB, never shifts turn boundaries.
		if lc := LoopControlFromContext(ctx); lc != nil {
			if accumulated := lc.DeliveredGuidance(); accumulated != "" {
				appendGuidanceToUserMessage(modelMessages, accumulated)
			}
		}

		// Derive a child context for the LLM stream. This allows mid-loop stream
		// cancellation: when the user sends guidance, CancelStream cancels only
		// this child, so the stream winds down (the provider's HTTP request is
		// cancelled and the event channel closes) and the loop proceeds to the
		// next iteration where it drains the guidance. The loop's own ctx stays
		// alive, so the loop keeps running. When there is no LoopControl, the
		// child is still derived but never independently cancelled — behaviour
		// is identical to before.
		streamCtx, streamCancelFn := context.WithCancel(ctx)
		// Safety net: guarantee the child context is released on every path. The
		// happy path releases it promptly via the explicit streamCancelFn() call
		// after the stream is consumed (before tool execution, so it doesn't
		// linger through long tool calls, and so ctx's child set doesn't grow one
		// entry per loop step). But the retry and event loops below have several
		// early error/abort returns that would otherwise skip that release and
		// leak the cancel func — go vet flags this as a lostcancel. This deferred
		// call covers those returns; on the happy path it is a harmless idempotent
		// no-op because the context is already cancelled.
		defer streamCancelFn()
		lc := LoopControlFromContext(ctx)
		if lc != nil {
			lc.SetStreamCancel(streamCancelFn)

			// Close the race window between DrainGuidance at the top of this
			// iteration and SetStreamCancel above. Guidance pushed in that gap
			// is now in the queue but pendingGuidance was drained before it
			// arrived, so it is NOT in this iteration's user message. The
			// HTTP handler's CancelStream also returned false (the cancel func
			// wasn't registered yet), so the stream was never interrupted.
			// Without this check the stream would run to completion (10-30s)
			// and the guidance would only be applied on the NEXT iteration.
			// Re-check the queue: if guidance arrived, cancel the stream so
			// the next iteration drains it and injects it promptly.
			if lc.HasPendingGuidance() {
				slog.Info("guidance arrived during pre-stream gap, cancelling stream before it starts", "session", sessionID, "step", step)
				streamCancelFn()
				lc.ClearStreamCancel()
				continue
			}
		}

		// Stream from LLM with retry for transient errors
		streamReq := provider.StreamRequest{
			Model:    modelID,
			System:   systemPrompts,
			Messages: modelMessages,
			Tools:    providerTools,
			// The agent loop is the one caller that asks for thinking. Providers
			// that have no reasoning mode, and models that would need a
			// configuration ogcode cannot size safely, ignore it.
			Thinking: true,
		}

		compactionCount := 0

		// Proactive compaction: estimate the request body size and compact
		// before sending if it exceeds a safe threshold. This prevents sending
		// requests that will fail with context-length errors (especially with
		// smaller local models like Ollama). We only do this once per step
		// to avoid compaction loops.
		//
		// The threshold is derived from the model's context window when known
		// (compact at contextWindow − reserve), so a 200k model uses far more of
		// its window than a small local model — instead of a single fixed cap
		// that is simultaneously too high for tiny models and too low for large
		// ones. Unknown windows fall back to a fixed token cap.
		maxRequestTokens := compactionThreshold
		// Prefer the provider-reported input token count from the previous step
		// when we have one: it is the exact size the model processed. Otherwise use
		// the local estimate (which now counts tool schemas and uses a BPE-aware
		// heuristic instead of bytes÷4). Take the larger of the two — the reported
		// count catches under-counting, and the fresh estimate catches the newest
		// tool results not yet reflected in any reported count — so the check errs
		// toward compacting early rather than overflowing.
		requestTokens := effectiveRequestTokens(estimateRequestTokens(streamReq), lastInputTokens)
		if !memoryEnabled && requestTokens > maxRequestTokens && compactionCount == 0 {
			before := len(streamReq.Messages)
			slog.Info("proactive compaction: request tokens exceed threshold", "session", sessionID, "estimatedTokens", requestTokens, "reportedInputTokens", lastInputTokens, "thresholdTokens", maxRequestTokens, "contextWindow", modelContextWindow, "messages", before)
			compactionSummary = lr.compactRequest(ctx, p, modelID, sessionID, &streamReq, compactionSummary, modelContextWindow)
			compactionCount++
			// The reported count describes the request that was just discarded.
			// effectiveRequestTokens takes the LARGER of estimate and reported, so
			// leaving it set would keep sizing every remaining step of the turn
			// against history that is no longer being sent.
			lastInputTokens = 0
			slog.Info("proactive compaction completed", "session", sessionID, "before", before, "after", len(streamReq.Messages), "newEstimatedTokens", estimateRequestTokens(streamReq))
			requestTokens = effectiveRequestTokens(estimateRequestTokens(streamReq), lastInputTokens)
		}

		// Ask for as much output as the model and the remaining window allow.
		// Compaction inside the retry loop below only shrinks the request further,
		// which leaves this budget valid (more room, not less).
		streamReq.MaxTokens = outputTokenBudget(modelMaxOutput, modelContextWindow, requestTokens)

		slog.Info("calling LLM", "session", sessionID, "step", step, "model", modelID, "messages", len(streamReq.Messages), "maxTokens", streamReq.MaxTokens)

		var streamCh <-chan provider.StreamEvent
		var streamErr error
		const maxCompactions = 2
		const maxRetries = 3
		for attempt := 1; attempt <= maxRetries; attempt++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Stamped per attempt, not once before the loop: a retry can sleep
			// for seconds of backoff, and billing that to the model would make
			// TTFT describe ogcode's retry schedule instead of the model.
			dispatchedAt := session.Now()
			streamCh, streamErr = p.StreamChat(streamCtx, streamReq)
			if streamErr == nil {
				// StreamChat returns only once the provider has answered 200, so
				// reaching here is the moment the request is known to have landed
				// at the model — the double tick, and the start of TTFT.
				assistantMsg.Delivery = &session.Delivery{
					DispatchedAt: dispatchedAt,
					ConnectedAt:  session.Now(),
					QueuedMs:     dispatchedAt - parentCreatedAt,
					Attempts:     attempt,
				}
				if parentCreatedAt == 0 {
					assistantMsg.Delivery.QueuedMs = 0
				}
				if err := lr.Store.UpdateMessage(assistantMsg); err != nil {
					slog.Warn("persist delivery on stream connect", "session", sessionID, "err", err)
				}
				lr.Bus.Publish("message.updated", assistantMsg)
				break
			}
			// Stream was cancelled by mid-loop guidance (the stream child context
			// was cancelled but the loop context is still alive). Don't retry, don't
			// error — just break out so the loop continues to the next iteration
			// where it drains the guidance. The partial assistant message (if any
			// text was streamed) is finalized below as a normal "stop".
			if streamCtx.Err() != nil && ctx.Err() == nil {
				slog.Info("stream cancelled by mid-loop guidance", "session", sessionID, "step", step)
				// Use a pre-closed channel, NOT nil: ranging over a nil channel
				// blocks forever, which would deadlock the loop at the
				// `for evt := range streamCh` below with no way to recover — the
				// abort check is inside that loop body and never runs. A closed
				// channel makes the range exit immediately and fall through to the
				// guidance-cancel finalization path (which finishes the turn as a
				// "stop", deletes the orphan assistant message, and continues the
				// loop to drain the pending guidance).
				closed := make(chan provider.StreamEvent)
				close(closed)
				streamCh = closed
				streamErr = nil
				break
			}
			slog.Warn("stream chat attempt failed", "session", sessionID, "attempt", attempt, "err", streamErr)
			// Context length exceeded: summarize old history with the LLM and retry.
			// Only used when agentic memory is OFF; with memory active the context is
			// already compressed via <prior_context> so compaction should not run.
			if !memoryEnabled && compactionCount < maxCompactions && isContextLengthError(streamErr) {
				before := len(streamReq.Messages)
				slog.Info("context length exceeded, using LLM to compact history", "session", sessionID, "messages", before)
				compactionSummary = lr.compactRequest(ctx, p, modelID, sessionID, &streamReq, compactionSummary, modelContextWindow)
				compactionCount++
				// Same reason as the proactive path: the reported count belongs to
				// the oversized request that just failed.
				lastInputTokens = 0
				slog.Info("llm-compacted context", "session", sessionID, "before", before, "after", len(streamReq.Messages))
				attempt-- // don't count compaction as a retry attempt
				continue
			}
			if attempt < maxRetries && isTransientError(streamErr) {
				// Default to a quadratic backoff (1s, 4s, 9s). When the server sent
				// a Retry-After, honor it — it knows the rate-limit window better
				// than our fixed schedule — but cap it so a pathological value can't
				// stall the loop for minutes.
				backoff := time.Duration(attempt*attempt) * time.Second
				if ra := retryAfterFromError(streamErr); ra > 0 {
					backoff = ra
					if backoff > maxRetryBackoff {
						backoff = maxRetryBackoff
					}
				}
				slog.Info("retrying stream chat", "session", sessionID, "attempt", attempt, "backoff", backoff)
				select {
				case <-ctx.Done():
					exitReason = "aborted"
					return ctx.Err()
				case <-time.After(backoff):
				}
				continue
			}
			// Non-transient or exhausted retries. Record what kind of failure
			// this was alongside the provider's own words: the message is what
			// the user reads, and the classification is what a later resume
			// decides from.
			errStr := streamErr.Error()
			assistantMsg.Error = &errStr
			finish := "error"
			assistantMsg.Finish = &finish
			assistantMsg.Interrupted = classifyInterruption(streamErr, step)
			// When the model rejects an image it does not support, invalidate the
			// cached capability so the next run (or resume) re-probes and resolves
			// to false — convertMessages then strips tool-result images and the
			// retried request succeeds.
			if assistantMsg.Interrupted != nil && assistantMsg.Interrupted.Reason == session.InterruptModelCapability {
				if derr := session.DeleteModelCapability(lr.Store.DB(), modelID); derr != nil {
					slog.Warn("invalidate model capability after image rejection", "model", modelID, "err", derr)
				} else {
					slog.Info("invalidated model image capability after rejection", "model", modelID)
				}
			}
			lr.Store.UpdateMessage(assistantMsg)
			lr.Bus.Publish("message.updated", assistantMsg)
			// Close any tool call this turn left unanswered before leaving. A
			// dangling tool_use makes the *next* request invalid, whether that
			// request comes from a resume or from the user simply typing again.
			if _, rerr := lr.ReconcileSession(sessionID); rerr != nil {
				slog.Warn("reconcile after stream failure", "session", sessionID, "err", rerr)
			}
			return fmt.Errorf("stream chat: %w", streamErr)
		}
		// Guard: loop exhausted all attempts via continue without returning (edge case)
		if streamErr != nil {
			errStr := streamErr.Error()
			assistantMsg.Error = &errStr
			finish := "error"
			assistantMsg.Finish = &finish
			assistantMsg.Interrupted = classifyInterruption(streamErr, step)
			if assistantMsg.Interrupted != nil && assistantMsg.Interrupted.Reason == session.InterruptModelCapability {
				if derr := session.DeleteModelCapability(lr.Store.DB(), modelID); derr != nil {
					slog.Warn("invalidate model capability after image rejection", "model", modelID, "err", derr)
				}
			}
			lr.Store.UpdateMessage(assistantMsg)
			lr.Bus.Publish("message.updated", assistantMsg)
			if _, rerr := lr.ReconcileSession(sessionID); rerr != nil {
				slog.Warn("reconcile after exhausted retries", "session", sessionID, "err", rerr)
			}
			return fmt.Errorf("stream chat: %w", streamErr)
		}

		// Process stream events
		var currentText strings.Builder
		var currentReasoning strings.Builder
		var currentReasoningSignature string
		var pendingToolCalls []pendingToolCall
		var finishReason string
		var streamUsage *provider.TokenUsage

		// markFirstToken records when the model started answering, whatever it
		// chose to say first. Text, reasoning and a tool call all count: on a
		// thinking model reasoning is what arrives first, and waiting for text
		// would report the entire thinking phase as time-to-first-token.
		//
		// Delivery is nil when the stream never opened — the guidance-cancel path
		// substitutes a pre-closed channel — and FirstTokenAt is written once,
		// because a turn has exactly one first token.
		markFirstToken := func(kind string) {
			if assistantMsg.Delivery == nil || assistantMsg.Delivery.FirstTokenAt != 0 {
				return
			}
			now := session.Now()
			assistantMsg.Delivery.FirstTokenAt = now
			assistantMsg.Delivery.TTFTMs = now - assistantMsg.Delivery.DispatchedAt
			assistantMsg.Delivery.FirstTokenKind = kind
			if err := lr.Store.UpdateMessage(assistantMsg); err != nil {
				slog.Warn("persist delivery on first token", "session", sessionID, "err", err)
			}
			lr.Bus.Publish("message.updated", assistantMsg)
			slog.Info("first token", "session", sessionID, "step", step, "model", modelID,
				"kind", kind, "ttftMs", assistantMsg.Delivery.TTFTMs,
				"queuedMs", assistantMsg.Delivery.QueuedMs, "attempts", assistantMsg.Delivery.Attempts)
		}

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
			if len(text) > maxReasoningLen {
				text = text[:maxReasoningLen] + "\n... (truncated)"
			}
			reasonData, _ := json.Marshal(session.ReasoningPartData{Text: text, Signature: currentReasoningSignature, Model: modelID})
			streamReasoningPart.Data = reasonData
			streamReasoningPart.UpdatedAt = session.Now()
			lr.Store.UpdatePart(streamReasoningPart)
			lastReasoningFlush = time.Now()
			lr.Bus.Publish("message.part.updated", map[string]string{
				"sessionId": string(sessionID),
				"partId":    string(streamReasoningPart.ID),
			})
		}

		// createReasoningPart stores one thinking block as a part of its own.
		// Blocks are kept separate because the API compares the sequence it
		// receives on replay against what it generated: concatenating two blocks,
		// or dropping one that carried no text, breaks the round-trip.
		createReasoningPart := func(data session.ReasoningPartData) *session.Part {
			data.Model = modelID
			if len(data.Text) > maxReasoningLen {
				data.Text = data.Text[:maxReasoningLen] + "\n... (truncated)"
			}
			raw, _ := json.Marshal(data)
			part := &session.Part{
				ID:        session.PartID(id.NewPartID()),
				MessageID: assistantID,
				SessionID: sessionID,
				Type:      session.PartReasoning,
				Data:      raw,
				CreatedAt: session.Now(),
				UpdatedAt: session.Now(),
			}
			if err := lr.Store.CreatePart(part); err != nil {
				slog.Error("create reasoning part", "err", err)
				return nil
			}
			lastReasoningFlush = time.Now()
			lr.Bus.Publish("message.part.updated", map[string]string{
				"sessionId": string(sessionID),
				"partId":    string(part.ID),
			})
			return part
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
			// Check for loop context cancellation (full abort) while processing stream events
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
				// Drain the rest of the channel so the provider's stream reader
				// unblocks and closes the HTTP connection instead of leaking it.
				go drainStreamEvents(streamCh)
				return ctx.Err()
			}

			// Check for mid-loop guidance cancellation: the stream child context
			// was cancelled but the loop context is still alive. Stop consuming
			// events so the loop can proceed to the next iteration and drain the
			// guidance. Whatever text/tool calls were streamed so far are kept as
			// a partial assistant turn — the model will see them in the next
			// iteration's history.
			if streamCtx.Err() != nil {
				slog.Info("stream cancelled by mid-loop guidance during event processing", "session", sessionID, "step", step)
				// Drain the remaining events in the background so the provider's
				// stream-reader goroutine can finish (its next read errors on the
				// cancelled context) and close the underlying HTTP connection,
				// rather than blocking forever on a send into a now-unread channel.
				// A leaked connection starves rate-limited free endpoints and makes
				// the *next* request stall — the observed "guidance queued, then
				// nothing happens" hang.
				go drainStreamEvents(streamCh)
				break
			}

			switch evt.Type {
			case provider.EventTextDelta:
				markFirstToken("text")
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
				markFirstToken("tool")
				tc := pendingToolCall{
					CallID: evt.ToolCallID,
					Name:   evt.ToolName,
					Input:  evt.ToolInput,
				}
				pendingToolCalls = append(pendingToolCalls, tc)

				// Create tool part.
				//
				// The arguments are whatever the provider sent, and not every
				// provider sends valid JSON — a proxy that streams incomplete
				// tool-call deltas will hand over a truncated object. Marshalling
				// validates any json.RawMessage it contains, so an unchecked
				// error here writes a part with nil Data, and that one part then
				// breaks two things far from this line: the UI renders it as
				// "malformed", and the request builder cannot pair it with
				// anything, because the call id was lost along with the rest of
				// the record. Anthropic then rejects every later request in the
				// session with `unknown variant ` + "`tool`" + `.
				//
				// Coerce instead of dropping. The call id is what history needs
				// to stay valid; a tool invoked with {} fails in a way the model
				// can read and retry.
				toolInput := validToolInput(evt.ToolInput)
				if len(evt.ToolInput) > 0 && !bytes.Equal(toolInput, evt.ToolInput) {
					slog.Warn("tool call arguments were not valid JSON; recording an empty object",
						"session", sessionID, "tool", evt.ToolName, "callId", evt.ToolCallID)
				}
				toolData, err := json.Marshal(session.ToolPartData{
					Tool:   evt.ToolName,
					CallID: evt.ToolCallID,
					State: session.ToolState{
						Status: session.ToolPending,
						Input:  toolInput,
					},
				})
				if err != nil {
					// Belt and braces: never persist a part that cannot be read
					// back. Keeping the id matters more than keeping the input.
					slog.Error("marshal tool part; falling back to an empty input", "err", err, "callId", evt.ToolCallID)
					toolData, _ = json.Marshal(session.ToolPartData{
						Tool:   evt.ToolName,
						CallID: evt.ToolCallID,
						State:  session.ToolState{Status: session.ToolPending, Input: json.RawMessage("{}")},
					})
				}
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

			case provider.EventReasoningStart:
				// A thinking block opened. Close the one before it so each block
				// keeps its own text and its own signature instead of being merged
				// into its neighbour.
				markFirstToken("reasoning")
				flushReasoningPart(true)
				streamReasoningPart = nil
				currentReasoning.Reset()
				currentReasoningSignature = ""

			case provider.EventReasoning:
				markFirstToken("reasoning")
				currentReasoning.WriteString(evt.Text)
				if streamReasoningPart == nil {
					streamReasoningPart = createReasoningPart(session.ReasoningPartData{
						Text:      currentReasoning.String(),
						Signature: currentReasoningSignature,
					})
				} else {
					flushReasoningPart(false)
				}

			case provider.EventReasoningSignature:
				// Capture the Anthropic thinking signature for round-tripping
				// back to the API on subsequent turns. Without the signature,
				// multi-turn thinking breaks with an API error.
				currentReasoningSignature = evt.Signature
				if streamReasoningPart == nil {
					// No part yet means the block produced no text: `display` is
					// "omitted" (the default on current models), so the signature
					// that arrives just before the block closes is the only evidence
					// it existed. Store it — an empty-text block still has to be
					// replayed exactly as received, and a missing one fails the turn.
					markFirstToken("reasoning")
					streamReasoningPart = createReasoningPart(session.ReasoningPartData{
						Text:      currentReasoning.String(),
						Signature: currentReasoningSignature,
					})
				} else {
					text := currentReasoning.String()
					if len(text) > maxReasoningLen {
						text = text[:maxReasoningLen] + "\n... (truncated)"
					}
					reasonData, _ := json.Marshal(session.ReasoningPartData{
						Text:      text,
						Signature: currentReasoningSignature,
						Model:     modelID,
					})
					streamReasoningPart.Data = reasonData
					streamReasoningPart.UpdatedAt = session.Now()
					lr.Store.UpdatePart(streamReasoningPart)
				}

			case provider.EventReasoningRedacted:
				// A safety-redacted block is a content block of its own, with an
				// opaque payload and no text. Close whatever thinking block was
				// streaming and store this one separately, so the sequence replays
				// in the order the model produced it — the API compares the blocks
				// coming back against what it generated and rejects a resequenced
				// or partial one.
				markFirstToken("reasoning")
				flushReasoningPart(true)
				streamReasoningPart = nil
				currentReasoning.Reset()
				currentReasoningSignature = ""
				createReasoningPart(session.ReasoningPartData{RedactedData: evt.RedactedData})

			case provider.EventFinish:
				if evt.FinishReason != nil {
					finishReason = *evt.FinishReason
				}

			case provider.EventError:
				errStr := evt.Error
				assistantMsg.Error = &errStr
				finish := "error"
				assistantMsg.Finish = &finish
				// Record how the turn died and close any tool call it left open,
				// exactly as the pre-stream failure paths do. Providers report a
				// dropped connection here rather than by closing the channel in
				// silence, so this is the path a mid-stream network failure takes —
				// without the record the turn is not resumable, and without the
				// reconcile a dangling tool_use invalidates the next request.
				assistantMsg.Interrupted = classifyInterruption(errors.New(evt.Error), step)
				lr.Store.UpdateMessage(assistantMsg)
				lr.Bus.Publish("message.updated", assistantMsg)
				// Drain the remaining events in the background (as the abort and
				// guidance exit paths do) so the provider's reader goroutine can
				// finish and release the HTTP connection, instead of leaking it if
				// it was mid-send on a full channel buffer.
				go drainStreamEvents(streamCh)
				if _, rerr := lr.ReconcileSession(sessionID); rerr != nil {
					slog.Warn("reconcile after stream error event", "session", sessionID, "err", rerr)
				}
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
				createReasoningPart(session.ReasoningPartData{
					Text:      currentReasoning.String(),
					Signature: currentReasoningSignature,
				})
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
		// Exception: when the stream was cancelled by mid-loop guidance (the stream
		// child context is done but the loop context is alive), treat it as a normal
		// "stop" so the loop continues to the next iteration and drains the guidance.
		streamCancelledByGuidance := streamCtx.Err() != nil && ctx.Err() == nil
		if finishReason == "" {
			if streamCancelledByGuidance {
				// Stream was interrupted by guidance — not an error. Treat as a
				// normal stop so the loop continues and picks up the guidance.
				slog.Info("stream cancelled by guidance, treating as stop", "session", sessionID, "step", step, "textLen", currentText.Len(), "toolCalls", len(pendingToolCalls))
				finishReason = "stop"
			} else if currentText.Len() > 0 || len(pendingToolCalls) > 0 {
				// We received content but no finish signal — stream was interrupted
				slog.Warn("stream ended without finish_reason, treating as error", "session", sessionID, "textLen", currentText.Len(), "toolCalls", len(pendingToolCalls))
				finishReason = "error"
				errStr := "stream interrupted: LLM connection closed without finish signal"
				assistantMsg.Error = &errStr
				assistantMsg.Interrupted = &session.Interruption{
					Reason:    session.InterruptNetwork,
					Resumable: true,
					Detail:    "The provider closed the connection before finishing this turn. Resuming picks up from here.",
					Step:      step,
				}
			} else {
				// No content and no finish — likely a connection failure
				slog.Warn("stream ended without content or finish_reason", "session", sessionID)
				finishReason = "error"
				errStr := "stream interrupted: no content received"
				assistantMsg.Error = &errStr
				assistantMsg.Interrupted = &session.Interruption{
					Reason:    session.InterruptNetwork,
					Resumable: true,
					Detail:    "The connection produced nothing before closing. Resuming retries the step.",
					Step:      step,
				}
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
			// Carry the reported input-side token count forward so the next step's
			// proactive-compaction check can use the exact size the model saw
			// instead of the byte estimate.
			lastInputTokens = streamUsage.InputTokens + streamUsage.CacheReadTokens + streamUsage.CacheWriteTokens
			// Only a step that re-sent the previous step's prefix is evidence.
			// Step 1 establishes the prefix rather than reusing it, and a step
			// that compacted rewrote it — both legitimately report no cache read
			// on an endpoint that does cache.
			cacheObs.Observe(streamUsage.CacheReadTokens, streamUsage.CacheWriteTokens, step > 1 && compactionCount == 0)
		}
		if err := lr.Store.UpdateMessage(assistantMsg); err != nil {
			slog.Error("update message finish", "err", err)
		}
		lr.Bus.Publish("message.updated", assistantMsg)

		// When mid-loop guidance cancels a text-only stream (no tool calls), the
		// partial assistant message has no matching tool_result and no valid
		// continuation. Leaving it in the DB would produce two consecutive
		// assistant role messages on the next prompt — the Anthropic and OpenAI
		// APIs both require strictly alternating user/assistant roles and reject
		// this with a 400. Delete the orphan so the history stays valid. The
		// guidance itself is delivered on the next iteration via the user message.
		if streamCancelledByGuidance && len(pendingToolCalls) == 0 {
			slog.Info("deleting text-only assistant message cancelled by guidance", "session", sessionID, "step", step, "textLen", currentText.Len())
			if err := lr.Store.DeleteMessage(assistantID); err != nil {
				slog.Error("delete guidance-cancelled assistant message", "err", err)
			}
			lr.Bus.Publish("message.deleted", map[string]string{
				"sessionId": string(sessionID),
				"messageId": string(assistantID),
			})
		}

		// Release the stream child context and clear the registration so a
		// future guidance event doesn't cancel a stream that's already done.
		if lc != nil {
			lc.ClearStreamCancel()
		}
		streamCancelFn()

		// Execute ready tool calls in parallel for improved throughput.
		// Built-in tools (bash, read, write, etc.) are stateless and safe for
		// concurrent use. DB part updates are sequential before and after the
		// parallel execution phase to keep state consistent.
		//
		// Skip tool execution when the stream was cancelled by mid-loop guidance:
		// the tool calls are partial (the model was interrupted mid-generation) and
		// the user wants the loop to act on the guidance immediately, not execute
		// a half-formed tool call first.
		var readyCalls []pendingToolCall
		if streamCancelledByGuidance && len(pendingToolCalls) > 0 {
			slog.Info("skipping tool execution after guidance-cancelled stream", "session", sessionID, "step", step, "pendingToolCalls", len(pendingToolCalls))
			// The stream was interrupted while the model was still emitting tool
			// calls. We won't execute these partial calls, but each tool_use we
			// already persisted on the assistant message MUST be paired with a
			// tool_result in the following user message — otherwise the next LLM
			// request fails the API's tool_use/tool_result pairing check (Anthropic
			// and OpenAI both 400 on a dangling tool_use). cancelPartialToolCalls
			// marks each part cancelled (for the UI) and emits the matching error
			// tool-result message so the conversation history stays valid.
			if cancelID := lr.cancelPartialToolCalls(sessionID, assistantID, pendingToolCalls); cancelID != "" {
				newMessageIDs = append(newMessageIDs, cancelID)
			}
			pendingToolCalls = nil
		}
		for _, tc := range pendingToolCalls {
			if tc.Ready {
				readyCalls = append(readyCalls, tc)
			}
		}
		toolCallsExecuted := len(readyCalls) > 0

		if toolCallsExecuted {
			// Execute against the tools this step actually offered, not the
			// agent's static list: executeTool's allowlist is the guard against a
			// model calling something it was never given, and compact_context is
			// added per step by cache verdict. Passing the static agent here would
			// reject every compaction as a disallowed tool.
			toolResults, aborted := lr.executeReadyToolCalls(ctx, sessionID, assistantID, readyCalls, effectiveAgent, workDir, modelSupportsImages, modelID, pressure)
			if aborted {
				exitReason = "aborted"
				return ctx.Err()
			}
			// The watermark points at the assistant message that asked for the
			// compaction, so that message and the tool_result answering it stay
			// in context together. Everything before it stops being sent from the
			// next step on.
			//
			// Keyed off the tool's own success marker, never off the call having
			// been made: a summary the tool rejected leaves the context untouched,
			// and dropping history for it would truncate the agent mid-turn while
			// it believes nothing happened.
			for _, tc := range readyCalls {
				if tc.Name != "compact_context" {
					continue
				}
				if ok, _ := toolResults[tc.CallID].Metadata["compacted"].(bool); !ok {
					slog.Info("compact_context did not take effect", "session", sessionID, "step", step)
					continue
				}
				summary, perr := tool.ParseCompactContextArgs(tc.Input)
				if perr != nil {
					continue
				}
				if watermark.set(assistantID, summary, messages) {
					// The next step sends the summary plus a short tail, but the
					// reported count still describes the full pre-compaction
					// request. Since effectiveRequestTokens prefers the larger of
					// the two, keeping it would floor the output budget for the
					// rest of the turn and could trigger an LLM-driven compaction
					// of an already-tiny request — the exact expense compact_context
					// exists to avoid on these endpoints.
					lastInputTokens = 0
					// The agent just did what the read-pressure reminder asks for.
					// Start counting again from what it kept.
					pressure.reset()
					slog.Info("in-turn context compacted", "session", sessionID, "step", step,
						"messagesInWorkingSet", len(messages), "summaryChars", len(summary))
				}
			}
		}

		// If tool calls were executed, always continue the loop regardless of
		// finish_reason — some providers send "stop" even alongside tool calls.
		// Only break when there are no tool calls to feed back.
		if !toolCallsExecuted {
			// Before breaking, re-check for guidance that arrived during this
			// iteration's streaming/tool execution. Such guidance was queued
			// after we drained at the top of this iteration, so it would be
			// silently lost if we exited now (guidance is never persisted).
			// Use HasPendingGuidance (not DrainGuidance) so we don't move it
			// into the delivered accumulator prematurely — the next iteration's
			// top-of-loop DrainGuidance will drain it, move it to delivered, and
			// inject it into the user message. Without this check the
			// shouldBreak guard at the next iteration's top would see an empty
			// queue and exit, dropping the guidance.
			if lc != nil && lc.HasPendingGuidance() {
				slog.Info("agent loop continuing for guidance received during iteration", "session", sessionID, "step", step)
				continue
			}
			slog.Info("agent loop complete", "session", sessionID, "steps", step, "reason", finishReason)
			exitReason = finishReason
			if memoryEnabled {
				lr.writeMemory(ctx, sessionID, p, modelID)
			}
			return nil
		}

		// Create a user message with tool results so the next LLM iteration sees
		// them, and fold it into the working set.
		if resultID := lr.writeToolResultMessage(sessionID, assistantID, pendingToolCalls); resultID != "" {
			newMessageIDs = append(newMessageIDs, resultID)
		}
	}

	// Reached MaxSteps with tool calls still pending — treat as stop.
	exitReason = "stop"
	if memoryEnabled {
		lr.writeMemory(ctx, sessionID, p, modelID)
	}
	return nil
}

// drainStreamEvents consumes any remaining events from a stream channel that the
// loop has stopped reading after a cancel/abort break. This lets the provider's
// stream-reader goroutine run to completion — its next read errors on the
// cancelled context, so it closes the channel and, crucially, the underlying
// HTTP connection — instead of blocking forever on a send into a channel nobody
// reads and leaking the connection. Leaked connections accumulate against a
// rate-limited endpoint's concurrency budget and stall subsequent requests.
func drainStreamEvents(ch <-chan provider.StreamEvent) {
	for range ch {
	}
}

// writeToolResultMessage creates the user message that carries the executed tool
// results back to the model on the next iteration, with one tool-result part per
// ready call (copied from the assistant's now-completed tool parts). It returns
// the new message's ID (empty on failure) so the caller can fold it into the
// working set.
func (lr *LoopRunner) writeToolResultMessage(sessionID session.SessionID, assistantID session.MessageID, pendingToolCalls []pendingToolCall) session.MessageID {
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
		return ""
	}
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
	return toolResultID
}

// cancelPartialToolCalls handles tool calls that were still being streamed when
// mid-loop guidance cancelled the LLM stream. These calls are never executed,
// but the assistant message already carries a persisted tool_use block for each
// one. The Anthropic and OpenAI APIs both require every tool_use to be followed
// by a matching tool_result, so leaving them unpaired makes the *next* request
// fail with a 400. This marks each part as cancelled (so the UI stops showing it
// as running) and creates a single tool-result user message pairing every
// cancelled tool_use with an error result, keeping the conversation valid — the
// same call/result pairing the mid-loop tool-execution cancel path maintains.
// It returns the ID of the tool-result message it creates (empty when none was
// created), so the caller can fold that message into its in-memory working set.
func (lr *LoopRunner) cancelPartialToolCalls(sessionID session.SessionID, assistantID session.MessageID, calls []pendingToolCall) session.MessageID {
	if len(calls) == 0 {
		return ""
	}

	// First pass: mark each partial tool part as cancelled (for the UI) and
	// collect the data needed to emit a matching tool_result for each. We defer
	// creating the result message until we know at least one part is real, so a
	// batch of vanished parts never produces an empty (invalid) user message.
	type cancelledCall struct {
		tool   string
		callID string
		state  session.ToolState
	}
	var results []cancelledCall
	for _, tc := range calls {
		part, perr := lr.Store.GetPart(tc.PartID)
		if perr != nil || part == nil {
			continue
		}
		var toolData session.ToolPartData
		if err := json.Unmarshal(part.Data, &toolData); err != nil {
			continue
		}
		// A stream cancelled mid-tool-call leaves partial, invalid JSON in the
		// accumulated input. convertMessages re-sends this verbatim as the
		// tool_use arguments on the resumed request; strict OpenAI-compatible
		// endpoints reject or stall on invalid JSON. Coerce any non-object /
		// invalid input to an empty object so the resumed request stays valid.
		callInput := validToolInput(tc.Input)
		errStr := "Cancelled by mid-loop guidance"
		toolData.State = session.ToolState{
			Status: session.ToolError,
			Input:  callInput,
			Error:  &errStr,
			Title:  &tc.Name,
			Time:   toolData.State.Time,
		}
		// Never replace a readable record with an unreadable one. callInput is
		// coerced above so this cannot fail today, but a part with nil Data is
		// the failure that costs a whole session, and skipping the write is
		// always the better half of that trade.
		updatedData, merr := json.Marshal(toolData)
		if merr != nil {
			slog.Error("marshal cancelled tool part; leaving it as it was", "err", merr, "callId", toolData.CallID)
			continue
		}
		part.Data = updatedData
		part.UpdatedAt = session.Now()
		if err := lr.Store.UpdatePart(part); err != nil {
			slog.Error("update cancelled tool part", "err", err)
		}
		lr.Bus.Publish("message.part.updated", map[string]string{
			"sessionId": string(sessionID),
			"partId":    string(part.ID),
		})
		results = append(results, cancelledCall{tool: toolData.Tool, callID: toolData.CallID, state: toolData.State})
	}

	if len(results) == 0 {
		return ""
	}

	// Emit one tool-result user message pairing every cancelled tool_use with an
	// error result, so the assistant's tool_use blocks are never left dangling.
	resultID := session.MessageID(id.NewMessageID())
	resultMsg := &session.MessageInfo{
		ID:        resultID,
		SessionID: sessionID,
		Role:      session.RoleUser,
		ParentID:  &assistantID,
		CreatedAt: session.Now(),
	}
	if err := lr.Store.CreateMessage(resultMsg); err != nil {
		slog.Error("create cancelled tool-result message", "err", err)
		return ""
	}
	for _, r := range results {
		resultData, _ := json.Marshal(session.ToolPartData{
			Tool:   r.tool,
			CallID: r.callID,
			State:  r.state,
		})
		resultPart := &session.Part{
			ID:        session.PartID(id.NewPartID()),
			MessageID: resultID,
			SessionID: sessionID,
			Type:      session.PartTool,
			Data:      resultData,
			CreatedAt: session.Now(),
			UpdatedAt: session.Now(),
		}
		if err := lr.Store.CreatePart(resultPart); err != nil {
			slog.Error("create cancelled tool-result part", "err", err)
		}
	}
	lr.Bus.Publish("message.updated", resultMsg)
	return resultID
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

	// Stamp the workspace onto the write so project-scoped recall can find this
	// turn later. The session row is the only place that knows the directory and
	// type; a miss here is not fatal — the fact is still stored session-scoped.
	scope := memory.Scope{SessionID: string(sessionID)}
	if sess, err := lr.Store.Get(sessionID); err == nil && sess != nil {
		dir := sess.Directory
		if dir == "" {
			dir = sess.ProjectID
		}
		scope.ProjectID = project.Resolve(dir)
		scope.SessionType = sess.SessionType
		scope.SessionName = sess.Title
	} else if err != nil {
		slog.Warn("writeMemory: session lookup failed; storing without project scope", "session", sessionID, "err", err)
	}

	lr.Memory.WriteMemory(ctx, scope, userText, responseText, chat)
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

// resolveRunModel picks the provider and model for a run and resolves the two
// fixed per-run model attributes the loop needs (image support, context window).
// It prefers the session's model, falling back to the registry's current default
// and then the immutable startup default.
func (lr *LoopRunner) resolveRunModel(ctx context.Context, sess *session.Session, sessionID session.SessionID) (p provider.Provider, modelID string, supportsImages bool, contextWindow int) {
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
	supportsImages = lr.resolveImageSupport(ctx, p, modelID)
	// The active model's context window (0 = unknown), used to size the
	// proactive-compaction trigger.
	if lr.Registry != nil {
		contextWindow = lr.Registry.ContextWindow(modelID)
	}
	return p, modelID, supportsImages, contextWindow
}

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

	// Permission gate: for interactive, gated sessions, mutating tools
	// (bash/write/edit) require user approval. Non-gated runs (headless task,
	// breakdown, note, search, CLI) and a nil manager skip this entirely.
	if lr.Permissions != nil && PermissionGatingEnabled(ctx) {
		action, err := lr.requestPermission(ctx, sessionID, tc, model)
		if err != nil {
			return tool.Result{}, err // context cancelled while awaiting approval
		}
		if action == permission.Deny {
			slog.Info("tool call denied by user", "session", sessionID, "tool", tc.Name)
			return tool.Result{
				Denied: true,
				Title:  tc.Name,
				Output: fmt.Sprintf("Permission denied by the user — the %s call was not run. Do not retry it; ask the user how to proceed or take a different approach.", tc.Name),
			}, nil
		}
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
		// Honor cancellation at the dispatch boundary. Several built-in tools do
		// bounded local work and don't check ctx themselves, so a mid-loop abort
		// or guidance-cancel wouldn't stop one that's about to run. Checking here
		// gives every tool a single cooperative cancellation point, so the loop
		// doesn't kick off new tool work after the user has already cancelled.
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		res, err := t.Execute(ctx, tc.Input, tctx)
		if err == nil {
			res = capToolOutput(res)
		}
		return res, err
	}

	slog.Warn("unknown tool requested", "tool", tc.Name)
	return tool.Result{}, fmt.Errorf("unknown tool: %s", tc.Name)
}

// executeReadyToolCalls runs the ready tool calls concurrently, writing each
// tool part's running → completed/error state to the DB (and publishing part
// updates) as it goes. It returns loopAborted=true when the loop's OWN context
// was cancelled — the caller must then stop and return ctx.Err() without creating
// a tool-result message. A mid-loop tool cancel (only the tool child context is
// cancelled) is handled internally and returns false, so the loop continues and
// pairs the cancelled results with a tool-result message (keeping tool_use /
// tool_result pairing valid).
// executeReadyToolCalls runs every ready call and returns each one's result
// keyed by call ID, so the caller can act on what a tool actually did rather
// than on the fact that it was invoked.
func (lr *LoopRunner) executeReadyToolCalls(ctx context.Context, sessionID session.SessionID, assistantID session.MessageID, readyCalls []pendingToolCall, agent Agent, workDir string, modelSupportsImages bool, modelID string, pressure *readPressure) (results map[string]tool.Result, loopAborted bool) {
	if len(readyCalls) > 1 {
		slog.Info("executing tool calls in parallel", "session", sessionID, "count", len(readyCalls), "tools", toolNames(readyCalls))
	}
	// Check for context cancellation before starting any execution
	if ctx.Err() != nil {
		slog.Info("agent loop cancelled before tool execution", "session", sessionID)
		return nil, true
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
		var toolData session.ToolPartData
		if part != nil && json.Unmarshal(part.Data, &toolData) == nil {
			toolData.State = session.ToolState{
				Status: session.ToolRunning,
				Input:  toolData.State.Input,
				Title:  &tc.Name,
				Time: session.ToolTime{
					Start: session.Now(),
				},
			}
			if updatedData, merr := json.Marshal(toolData); merr == nil {
				part.Data = updatedData
				part.UpdatedAt = session.Now()
				lr.Store.UpdatePart(part)
			} else {
				slog.Error("marshal running tool part", "err", merr, "callId", toolData.CallID)
			}
			lr.Bus.Publish("message.part.updated", map[string]string{
				"sessionId": string(sessionID),
				"partId":    string(part.ID),
			})
		}
		execInfos[i].tc = tc
	}

	// Derive a child context for tool execution. This allows mid-loop tool
	// cancellation: CancelTool on the LoopControl cancels only this child, so
	// wg.Wait() returns early with cancelled/errored results, and the loop
	// continues to its next iteration (where it picks up any injected guidance).
	// The loop's own ctx remains uncancelled, so the loop stays alive. When there
	// is no LoopControl (CLI, search, indexer), the child is still derived but
	// simply never independently cancelled — behaviour is identical to before.
	toolCtx, toolCancel := context.WithCancel(ctx)
	lc := LoopControlFromContext(ctx)
	if lc != nil {
		lc.SetToolCancel(toolCancel)
	}

	// Execute all ready tool calls concurrently
	var wg sync.WaitGroup
	for i := range readyCalls {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tc := readyCalls[idx]
			// A panic in a tool runs on this goroutine, where nothing above it
			// can recover: an unrecovered panic in any goroutine takes the whole
			// process down, so one bad tool call kills every session the server
			// is serving, not just this turn. Convert it into a failed tool call
			// — the loop already knows how to report one to the model and to the
			// user — and log the stack so the bug is still diagnosable.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("tool panicked",
						"session", sessionID, "tool", tc.Name, "panic", r,
						"stack", string(debug.Stack()))
					execInfos[idx].result = tool.Result{}
					execInfos[idx].err = fmt.Errorf("tool %s panicked: %v", tc.Name, r)
				}
			}()
			result, err := lr.executeTool(toolCtx, sessionID, assistantID, tc, agent, workDir, modelSupportsImages, modelID)
			execInfos[idx].result = result
			execInfos[idx].err = err
		}(i)
	}
	wg.Wait()

	// Fold this step's reading into the turn's running total and, if a reminder is
	// due, append it below the first content-returning result. This has to happen
	// before the results are written to the store: the model-facing history is
	// rebuilt from the stored output on every later step, so text that is not part
	// of that output is text the model never sees.
	for i := range execInfos {
		if execInfos[i].err != nil {
			continue
		}
		pressure.observe(execInfos[i].tc.Name, &execInfos[i].result)
	}
	pressure.endStep()

	results = make(map[string]tool.Result, len(execInfos))
	for _, info := range execInfos {
		if info.err == nil {
			results[info.tc.CallID] = info.result
		}
	}

	// Detect mid-loop tool cancellation BEFORE calling toolCancel() ourself. The
	// child context was cancelled (by CancelTool via the guidance endpoint) but
	// the loop context is still alive. We must capture this state before the
	// cleanup call to toolCancel() below, because that call always cancels toolCtx
	// — which would make toolCtx.Err() non-nil even on perfectly normal tool
	// completion, producing a false-positive "cancelled by user" message.
	toolCtxCancelled := toolCtx.Err() != nil && ctx.Err() == nil

	// Clear the tool-cancel registration and release the child context.
	if lc != nil {
		lc.ClearToolCancel()
	}
	toolCancel()

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
			// When the tool child context was cancelled mid-loop (not the loop
			// itself), surface a clear cancellation message rather than a raw
			// context.Canceled error string.
			if toolCtxCancelled {
				errStr = "Tool execution cancelled by user mid-loop guidance"
			}
			toolData.State = session.ToolState{
				Status: session.ToolError,
				Input:  tc.Input,
				Error:  &errStr,
				Title:  &tc.Name,
				Time:   toolData.State.Time,
			}
		} else if info.result.Denied {
			// The permission gate blocked the call; it never ran. Record a
			// distinct "denied" status so the UI and DB can tell a denied call
			// apart from one that completed or errored. The denial message is in
			// Output (not Error) because it is normal flow, not a failure.
			toolData.State = session.ToolState{
				Status: session.ToolDenied,
				Input:  tc.Input,
				Output: &info.result.Output,
				Title:  &tc.Name,
				Time: session.ToolTime{
					Start: toolData.State.Time.Start,
					End:   session.Now(),
				},
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

		updatedData, merr := json.Marshal(toolData)
		if merr != nil {
			slog.Error("marshal tool result part; leaving it as it was", "err", merr, "callId", toolData.CallID)
			continue
		}
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

	// If the loop context (not just the tool child) was cancelled during
	// execution, bail out now. The tool-result message must not be created — it
	// would contain partial/missing outputs for unexecuted tool calls. When only
	// the tool child was cancelled (mid-loop tool cancel via guidance), the loop
	// context is still alive, so we fall through and let the caller create the
	// tool-result message with the cancelled results — the call/result pairing
	// the API expects stays valid.
	if ctx.Err() != nil {
		slog.Info("agent loop cancelled after tool execution", "session", sessionID)
		return results, true
	}
	return results, false
}

// requestPermission evaluates the session ruleset for a tool call and, when the
// decision is "ask", publishes a permission.requested event and blocks until the
// user replies (or the tool/loop context is cancelled). It returns the resolved
// action (Allow or Deny). An "always" reply is recorded so subsequent matching
// calls in this session auto-allow.
func (lr *LoopRunner) requestPermission(ctx context.Context, sessionID session.SessionID, tc pendingToolCall, model string) (permission.Action, error) {
	pattern := permissionPattern(tc)
	action := lr.Permissions.Ruleset(string(sessionID)).Evaluate(tc.Name, pattern)
	if action == permission.Allow || action == permission.Deny {
		return action, nil
	}

	// action == Ask. In Auto mode, let the risk classifier auto-approve calls it
	// judges safe (rules first, an LLM check for the unclear middle); genuinely
	// risky calls still fall through to the prompt. Ask mode always prompts.
	if sess, _ := lr.Store.Get(sessionID); sess != nil && sess.Permission == "auto" {
		if lr.assessAutoRisk(ctx, sess, tc, pattern, model) == permission.RiskSafe {
			slog.Info("auto-approved low-risk tool call", "session", sessionID, "tool", tc.Name)
			return permission.Allow, nil
		}
	}

	// Ask: register a pending request and surface it to the UI.
	req := permission.Request{
		ID:        permission.NewPermissionID(),
		SessionID: string(sessionID),
		Tool:      tc.Name,
		Input:     string(tc.Input),
		Patterns:  []string{pattern},
	}
	pr := lr.Permissions.Create(req)
	lr.Bus.Publish("permission.requested", map[string]string{
		"sessionId":    string(sessionID),
		"permissionId": string(req.ID),
		"tool":         tc.Name,
		"pattern":      pattern,
		"input":        string(tc.Input),
	})
	slog.Info("awaiting tool permission", "session", sessionID, "tool", tc.Name, "permission", req.ID)

	select {
	case <-ctx.Done():
		// The tool child context was cancelled (mid-loop guidance) or the whole
		// loop was aborted while we waited. Drop the pending request so it can't
		// leak, and let the caller unwind on the context error.
		lr.Permissions.Remove(req.ID)
		lr.Bus.Publish("permission.replied", map[string]string{
			"sessionId":    string(sessionID),
			"permissionId": string(req.ID),
			"response":     "cancelled",
		})
		return permission.Deny, ctx.Err()
	case reply := <-pr.ReplyCh:
		switch reply {
		case "always":
			// Grant this tool for the rest of the session.
			lr.Permissions.AddRule(string(sessionID), permission.Rule{
				Permission: tc.Name,
				Pattern:    "*",
				Action:     permission.Allow,
			})
			return permission.Allow, nil
		case "once":
			return permission.Allow, nil
		default: // "reject" or anything unexpected
			return permission.Deny, nil
		}
	}
}

// riskLLMTimeout bounds the Auto-mode LLM risk check so it can't stall a tool
// call for long. On timeout/error the verdict is RiskAsk (fail safe).
const riskLLMTimeout = 12 * time.Second

// assessAutoRisk decides, in Auto mode, whether a tool call is safe to run
// without asking. write/edit are judged purely by the path rules; bash uses the
// command rules and escalates the unclear middle to a quick LLM check. The
// model argument is the loop's resolved model ID (may differ from sess.Model
// when the session has no model pinned); it is forwarded to the LLM risk check
// so it resolves the same provider the loop is using, not an arbitrary first
// provider from a ResolveProvider("") fallback.
func (lr *LoopRunner) assessAutoRisk(ctx context.Context, sess *session.Session, tc pendingToolCall, pattern, model string) permission.Risk {
	switch tc.Name {
	case "write", "edit":
		return permission.ClassifyWrite(pattern, sess.Directory)
	case "bash":
		r := permission.ClassifyBash(pattern)
		if r == permission.RiskUnclear {
			r = lr.assessCommandRiskLLM(ctx, model, pattern)
		}
		return r
	default:
		// Any other gated tool with no specific rule → ask.
		return permission.RiskAsk
	}
}

// assessCommandRiskLLM asks the model whether a shell command the rules couldn't
// classify is safe to auto-run. Verdicts are cached (command risk is context-
// independent). Any failure — no provider, error, timeout, or an ambiguous
// answer — resolves to RiskAsk so Auto mode never auto-runs something it isn't
// confident about.
func (lr *LoopRunner) assessCommandRiskLLM(ctx context.Context, model, command string) permission.Risk {
	if v, ok := lr.Permissions.CachedRisk(command); ok {
		return v
	}
	p := lr.Registry.ResolveProvider(model)
	if p == nil {
		if dp := lr.Registry.Default(); dp != nil {
			p = dp
		} else {
			p = lr.DefaultProvider
		}
	}
	if p == nil {
		return permission.RiskAsk
	}

	const system = "You are a strict security gate for an autonomous coding agent that runs shell " +
		"commands in a developer's project. Decide whether a command is safe to run automatically " +
		"WITHOUT asking the user. Answer SAFE only for read-only, inspection, build, test, lint, or " +
		"routine easily-reversible project changes. Answer ASK if the command could delete or overwrite " +
		"important data, write outside the project, exfiltrate data over the network, download and execute " +
		"code, change system or global state, run with elevated privileges, or have irreversible side " +
		"effects. When unsure, answer ASK. Reply with exactly one word: SAFE or ASK."
	userContent, _ := json.Marshal("Command:\n" + command)

	reqCtx, cancel := context.WithTimeout(ctx, riskLLMTimeout)
	defer cancel()
	ch, err := p.StreamChat(reqCtx, provider.StreamRequest{
		Model:     model,
		System:    []string{system},
		Messages:  []provider.ModelMessage{{Role: "user", Content: userContent}},
		MaxTokens: 8,
	})
	if err != nil {
		slog.Warn("auto-mode risk check failed; asking", "err", err)
		return permission.RiskAsk
	}
	var out strings.Builder
	for evt := range ch {
		if evt.Type == provider.EventTextDelta {
			out.WriteString(evt.Text)
		}
	}
	up := strings.ToUpper(out.String())
	verdict := permission.RiskAsk
	// The model is asked for exactly one word: SAFE or ASK. Require the trimmed
	// output to BE "SAFE" (allowing trailing punctuation), not merely contain it
	// — "NOT SAFE", "not safe", "UNSAFE", and "It is safe" all contain "SAFE" as
	// a substring, and the old Contains check auto-approved all of them. A strict
	// equals match is fail-safe: anything ambiguous defaults to RiskAsk.
	if isSafeVerdict(up) {
		verdict = permission.RiskSafe
	}
	lr.Permissions.CacheRisk(command, verdict)
	slog.Info("auto-mode risk verdict", "command", truncateText(command, 80), "verdict", verdict)
	return verdict
}

// isSafeVerdict reports whether the model's uppercased risk verdict is exactly
// "SAFE" (ignoring surrounding whitespace and trailing punctuation). It must
// NOT match substrings: "NOT SAFE", "not safe", "UNSAFE", and "It is safe" all
// contain "SAFE" and would be auto-approved by a naive Contains check.
func isSafeVerdict(up string) bool {
	t := strings.TrimSpace(up)
	t = strings.TrimRight(t, "!.,;")
	t = strings.TrimSpace(t)
	return t == "SAFE"
}

// permissionPattern extracts the resource a tool call acts on, for ruleset
// matching and UI display: the target path for write/edit, the command for bash,
// otherwise "*".
func permissionPattern(tc pendingToolCall) string {
	switch tc.Name {
	case "write", "edit", "read":
		var in struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(tc.Input, &in) == nil && in.Path != "" {
			return in.Path
		}
	case "bash":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(tc.Input, &in) == nil && in.Command != "" {
			return in.Command
		}
	case "skill":
		// The pattern is the skill being loaded, so a rule — configured, or
		// granted by an "always allow" reply — applies to that one skill rather
		// than to every skill at once.
		var in struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(tc.Input, &in) == nil && in.Name != "" {
			return in.Name
		}
	}
	return "*"
}

// skillPermissionRules converts a registry's configured skill actions into
// permission rules.
//
// Only ask and deny are emitted. Allow is what the default ruleset's trailing
// catch-all already produces, so a rule for it would add a line per skill and
// change nothing.
//
// Each rule names one concrete skill, never the glob the config was written
// with: permission.matchGlob resolves an exact match or a bare "*" and nothing
// else, so a rule carrying "internal-*" would match no call at all and the deny
// would be silently inert. The glob is resolved on this side, where the set of
// names it covers is known.
func skillPermissionRules(reg *skill.Registry) permission.Ruleset {
	var rules permission.Ruleset
	for _, s := range reg.List() {
		switch reg.Action(s.Name) {
		case skill.Ask:
			rules = append(rules, permission.Rule{Permission: "skill", Pattern: s.Name, Action: permission.Ask})
		case skill.Deny:
			rules = append(rules, permission.Rule{Permission: "skill", Pattern: s.Name, Action: permission.Deny})
		}
	}
	return rules
}

// capToolOutput is the global backstop that bounds any tool result before it
// enters the model context. Tools that already truncated themselves (read,
// bash) set Result.Truncated and are left untouched; everything else — grep,
// glob, and any future or MCP-style tool — is capped to
// tool.MaxToolOutputBytes/MaxToolOutputLines, keeping the head. This stops a
// single oversized result from dominating the turn and being re-sent on every
// later step.
func capToolOutput(res tool.Result) tool.Result {
	if res.Truncated || res.Output == "" {
		return res
	}
	output, truncated := tool.TruncateOutput(res.Output, tool.KeepHead)
	res.Output = output
	res.Truncated = truncated
	return res
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

// lastUserMessageRef returns the id of the most recent user-role message and
// the time it was created. The id becomes the assistant message's ParentID; the
// timestamp is where QueuedMs is measured from. Both are zero when the session
// has no user message yet, which a caller must treat as "unknown" rather than
// as an epoch timestamp.
func lastUserMessageRef(messages []*session.MessageWithParts) (*session.MessageID, int64) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Info.Role == session.RoleUser {
			id := messages[i].Info.ID
			return &id, messages[i].Info.CreatedAt
		}
	}
	return nil, 0
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

func toProviderMessages(messages []*session.MessageWithParts, memoryText string, modelSupportsImages bool, modelID string) []provider.ModelMessage {
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
			result := convertMessages(filtered, modelSupportsImages, modelID)

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
	return convertMessages(messages, modelSupportsImages, modelID)
}

// replayableReasoning decides whether an assistant message's stored thinking
// blocks may be sent back to modelID.
//
// Thinking blocks are tied to the model that produced them. Replayed to any
// other model they are silently ignored but still billed as input, and an
// unsigned block — what an OpenAI-family model stores, since it keeps its
// reasoning server-side and returns only the text — has nothing to verify it
// with and is rejected outright. A session whose model is switched mid-flight
// therefore carries blocks the new model cannot use, and forwarding them costs
// tokens at best and a 400 at worst.
//
// The decision is all-or-nothing per message: the API compares the sequence of
// thinking blocks coming back against what it generated, so dropping some and
// keeping others is itself a 400. Anything unrecognised drops the whole set —
// replay is only *required* within a tool-use turn, where the blocks were
// produced by the model that is about to be called again.
func replayableReasoning(parts []session.ReasoningPartData, modelID string) []provider.ReasoningPart {
	if len(parts) == 0 || modelID == "" {
		return nil
	}
	out := make([]provider.ReasoningPart, 0, len(parts))
	for _, d := range parts {
		// Parts stored before the origin was recorded have unknown provenance.
		if d.Model != modelID {
			return nil
		}
		if d.Signature == "" && d.RedactedData == "" {
			return nil
		}
		out = append(out, provider.ReasoningPart{
			Text:         d.Text,
			Signature:    d.Signature,
			RedactedData: d.RedactedData,
		})
	}
	return out
}

// validToolInput returns tool-call arguments that are safe to persist and to
// replay, substituting an empty object for anything that is not a JSON object.
//
// Not every provider sends valid JSON: a proxy that forwards a truncated
// tool-call delta as a complete one produces arguments that stop mid-string.
// Persisting those is worse than losing them, because marshalling a
// json.RawMessage validates it — an unchecked error there writes a part with no
// data at all, and the call id goes with it. The id is what keeps the
// conversation valid; the arguments are something the model can supply again.
func validToolInput(raw json.RawMessage) json.RawMessage {
	var obj map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		return json.RawMessage("{}")
	}
	return raw
}

func convertMessages(messages []*session.MessageWithParts, modelSupportsImages bool, modelID string) []provider.ModelMessage {
	var result []provider.ModelMessage
	for _, m := range messages {
		// Collect text, tool, and reasoning parts
		var textParts []string
		var toolCallParts []session.ToolPartData
		var toolResultParts []session.ToolPartData
		var imageParts []session.ImagePartData
		var reasoningData []session.ReasoningPartData

		for _, p := range m.Parts {
			switch p.Type {
			case session.PartText:
				var data session.TextPartData
				json.Unmarshal(p.Data, &data)
				if data.Text != "" {
					textParts = append(textParts, data.Text)
				}
			case session.PartImage:
				var data session.ImagePartData
				json.Unmarshal(p.Data, &data)
				if data.Data != "" && data.MediaType != "" {
					imageParts = append(imageParts, data)
				}
			case session.PartTool:
				var data session.ToolPartData
				// A tool part with no usable call id cannot be paired with its
				// counterpart on any provider: OpenAI rejects an empty
				// tool_call_id, and Anthropic rejects both a tool_use with no id
				// and a tool_result whose tool_use_id answers nothing.
				//
				// Drop it from the request instead of sending it. The call side
				// and the result side are dropped by the same rule, and the
				// result copies its id from the call it answers, so a valid call
				// never loses its result and a malformed one takes its own
				// orphan with it. Without this, a single malformed part makes
				// every later request in that session invalid — the session is
				// bricked, and no amount of retrying or resuming clears it.
				if err := json.Unmarshal(p.Data, &data); err != nil || data.CallID == "" {
					slog.Warn("dropping unpairable tool part from request",
						"session", m.Info.SessionID, "message", m.Info.ID,
						"part", p.ID, "tool", data.Tool, "err", err)
					continue
				}
				if m.Info.Role == session.RoleAssistant {
					toolCallParts = append(toolCallParts, data)
				} else {
					toolResultParts = append(toolResultParts, data)
				}
			case session.PartReasoning:
				var data session.ReasoningPartData
				json.Unmarshal(p.Data, &data)
				reasoningData = append(reasoningData, data)
			}
		}

		reasoningParts := replayableReasoning(reasoningData, modelID)

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
				Role:           "assistant",
				ToolCalls:      toolCallsJSON,
				ReasoningParts: reasoningParts,
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
				if tr.State.Image != nil && modelSupportsImages {
					msg.Images = []provider.MessageImage{{
						MediaType: tr.State.Image.MediaType,
						Data:      tr.State.Image.Data,
					}}
				}
				result = append(result, msg)
			}
		} else {
			// Plain text message
			if len(textParts) > 0 || len(imageParts) > 0 {
				content, _ := json.Marshal(strings.Join(textParts, ""))
				msg := provider.ModelMessage{
					Role:    string(m.Info.Role),
					Content: content,
				}
				if len(imageParts) > 0 {
					msg.Images = make([]provider.MessageImage, 0, len(imageParts))
					for _, ip := range imageParts {
						msg.Images = append(msg.Images, provider.MessageImage{
							MediaType: ip.MediaType,
							Data:      ip.Data,
						})
					}
				}
				// Forward reasoning/thinking blocks for assistant messages.
				// Anthropic requires these to be passed back with their signatures
				// on subsequent turns; other providers ignore this field.
				if m.Info.Role == session.RoleAssistant && len(reasoningParts) > 0 {
					msg.ReasoningParts = reasoningParts
				}
				result = append(result, msg)
			}
		}
	}
	return result
}

// buildSystemPrompt builds the full system prompt with no model-family tuning
// (family = generic). Retained for callers/tests that have no model in hand.
func buildSystemPrompt(a Agent, dir string, memoryEnabled bool, agentMDContent string, memoryMDContent string, viewportWidth int, viewportHeight int) string {
	return buildSystemPromptForFamily(a, dir, memoryEnabled, agentMDContent, memoryMDContent, viewportWidth, viewportHeight, "")
}

// buildSystemPromptForFamily returns the assembled entries joined into one
// string. The provider is given the entries separately (see
// buildSystemPromptEntries) so the cacheable prefix can be isolated; this
// convenience form is for callers and tests that just want the whole text.
func buildSystemPromptForFamily(a Agent, dir string, memoryEnabled bool, agentMDContent string, memoryMDContent string, viewportWidth int, viewportHeight int, family string) string {
	return strings.Join(buildSystemPromptEntries(a, dir, memoryEnabled, agentMDContent, memoryMDContent, viewportWidth, viewportHeight, family, -1), "\n\n")
}

// buildSystemPromptEntries returns the system-prompt entries in wire order:
//
//	[0]  the static base — agent instructions, project context, memory guidance.
//	     Providers attach the cache breakpoint here, so nothing that changes
//	     between the steps of a turn may go in it.
//
//	     Two things in it do change *across* turns, and knowingly: AGENT.md and
//	     MEMORY.md are re-read once per turn (RunLoop, top), and the prompt
//	     actively tells the agent to write to MEMORY.md as it works. Each such
//	     edit makes the next turn a cache write rather than a cache read. That is
//	     the price of having the project's own knowledge inside the cached prefix
//	     where it is cheapest to re-send on every step of every other turn; it is
//	     paid once per edit, not per step. Anything that varies more often than
//	     that — the viewport, the index status, the date, the skill list, the
//	     compaction offer — belongs in the entries below.
//	[1:] per-turn dynamic content: the rendering viewport (the browser resends
//	     its window size with every prompt), the project index status, and the
//	     current date.
//	last the agent's FinalInstruction, kept adjacent to the model's response.
//
// Putting the viewport in [0] would invalidate the cached tools+system prefix
// every time the user resized their window — several times a turn, not once —
// which is the same reason the date lives out here rather than in the base. The
// index status is out here for the same reason: a user can build the index
// while the session is open.
//
// indexedFiles < 0 means no count was reported and the status line is omitted.
func buildSystemPromptEntries(a Agent, dir string, memoryEnabled bool, agentMDContent string, memoryMDContent string, viewportWidth int, viewportHeight int, family string, indexedFiles int) []string {
	entries := []string{staticSystemPrompt(a, dir, memoryEnabled, agentMDContent, memoryMDContent, family)}

	if vp := viewportPrompt(viewportWidth, viewportHeight); vp != "" {
		entries = append(entries, strings.TrimSpace(vp))
	}

	// Only agents that hold codebase_map can act on this; for the rest it
	// describes a tool they were never offered.
	if a.projectScoped() {
		if st := indexStatusPrompt(indexedFiles); st != "" {
			entries = append(entries, st)
		}
	}

	// The LaTeX environment is detected once and cached for the process, so it is
	// static *today*. But detection is a probe of the host, not a session-fixed
	// value — a future change (or a test forcing the cache) could make it vary, and
	// the static block must stay byte-identical by construction, not by caching.
	// It lands here next to the other host-derived, per-turn entries for the same
	// reason the index status does.
	if a.HasTool("latex_to_pdf") {
		if lp := latexInfoPrompt(); lp != "" {
			entries = append(entries, strings.TrimSpace(lp))
		}
	}
	entries = append(entries, systemReminderPrompt())

	// Output-only agents pin their format constraint last, where it sits closest
	// to the model's response and is least likely to be diluted by the sections
	// above. It is static, but a couple of sentences outside the cached block
	// cost far less than letting dynamic content into the block.
	if a.FinalInstruction != "" {
		entries = append(entries, a.FinalInstruction)
	}
	return entries
}

// staticSystemPrompt builds the cacheable base: the agent's own instructions
// plus everything that is fixed for the whole session. The model-family
// working-style block belongs here — the model is fixed per session, so it stays
// byte-identical across turns.
func staticSystemPrompt(a Agent, dir string, memoryEnabled bool, agentMDContent string, memoryMDContent string, family string) string {
	// Project context (working dir, host env, AGENT.md, MEMORY.md) is only
	// relevant to agents that operate on the user's codebase. Utility agents —
	// the keyword indexer and web-research agent — skip it to keep their prompt
	// lean, which also avoids referencing files and tools they don't have.
	prompt := a.System
	if a.projectScoped() {
		if style := modelFamilyStylePrompt(family); style != "" {
			prompt += "\n\n" + style
		}
		prompt += fmt.Sprintf("\n\nWorking directory: %s\nPlatform: %s/%s%s", dir, runtime.GOOS, runtime.GOARCH, osEnvPrompt(a.HasTool("bash")))

		if agentMDContent != "" {
			prompt += agentMDContent
		}

		if memoryMDContent != "" {
			prompt += memoryMDContent
		}

		// MEMORY.md section: role-aware instructions based on whether the agent
		// can write files, and on whether a <memory-md> block was actually
		// prepended above. BuildAgent gets full read/write maintenance
		// instructions; read-only agents (Plan, Note) get read-only guidance.
		// The absent-file case (including the nudge to create one) is handled
		// inside memoryMDPrompt so the section never points at a tag that the
		// prompt does not contain.
		canWriteFiles := a.HasTool("write") || a.HasTool("edit")
		// The agentic-memory comparison is gated on the same condition as the
		// paragraph below: the recall tools are only registered when memory is
		// initialised, so describing them otherwise names a call the agent will
		// never be offered.
		hasRecall := memoryEnabled && a.HasTool("memory_recall")
		prompt += "\n\n" + memoryMDPrompt(canWriteFiles, memoryMDContent != "", hasRecall)
	}

	// Only advertise agentic memory to agents that actually have the memory_recall
	// tool (Build, Task, Plan). Note/Breakdown/Index/Search lack it, so telling
	// them to "use the memory_recall tool" would reference a tool they don't have.
	if memoryEnabled && a.HasTool("memory_recall") {
		prompt += `

You have access to agentic memory. Prior conversation context is provided in <prior_context> blocks, which includes a knowledge graph summary of THIS conversation and the most recent assistant response for continuity. It does not contain anything from earlier sessions.

To retrieve specific past facts, decisions, or details, use the memory_recall tool with a precise question. Use it proactively whenever the current query references past context, prior decisions, or earlier work — do not guess or hallucinate past details.`

		if a.HasTool("project_memory_recall") {
			prompt += `

Agentic memory has two scopes, and picking the wrong one loses information:
- memory_recall searches THIS conversation only. Use it for what was said or done earlier in this session.
- project_memory_recall searches EVERY past conversation in this project. Use it when the question reaches beyond this session — why something was built the way it was, what was tried before, when a convention or decision was introduced, or anything referring to work you have no record of in this session.

Results from project_memory_recall are attributed to the conversation and date they came from. Treat the most recent fact as current when two disagree, and say so rather than presenting a superseded decision as if it still stood.

project_memory_recall also accepts scope: "session", which runs that same dated, attributed search over the current conversation only. Reach for it when you want this session's history with timestamps and ordering rather than the flat summary memory_recall returns.`
		}
	}

	// The instruction-source boundary closes the cacheable block. It applies to
	// every agent — the utility ones read supplied documents and fetched pages
	// too — and it sits last because it is the rule most likely to be tested by
	// the very next thing the agent reads.
	canAct := a.HasTool("bash") || a.HasTool("write") || a.HasTool("edit")
	prompt += "\n\n" + untrustedContentPrompt(canAct, agentMDContent != "", a.HasTool("skill"))

	return prompt
}

func mustMarshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	data, _ := json.Marshal(v)
	return data
}

// maxRetryBackoff caps how long a single retry waits, so a server-provided
// Retry-After (which can be large) can never stall the loop for minutes.
const maxRetryBackoff = 120 * time.Second

// isTransientError returns true for errors that are worth retrying
// (rate limits, timeouts, connection resets, server errors). It prefers the
// provider's structured status code and falls back to string matching for
// stream-level and network errors that carry no HTTP status.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *provider.APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsTransient()
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

// appendGuidanceToUserMessage appends mid-loop guidance text to the content of
// the first user text message in the slice. The guidance is labeled so the model
// understands it is mid-loop guidance from the user, not a new turn. This keeps
// the guidance within the user's turn message — the model sees it as additional
// user input, not as a system directive — and avoids creating consecutive
// same-role messages that would violate provider alternating-role requirements.
//
// If no user text message is found (e.g. the conversation starts with tool
// results), the guidance is appended to the first user-role message regardless.
// If there are no user messages at all, the guidance is dropped (the loop will
// re-attempt on the next iteration).
//
// The synthetic compaction-summary message that prependCompactionSummary inserts
// at the front of the slice is skipped: it is a user-role message, but it stands
// for the agent's own record of earlier steps, not for the user's turn prompt.
// Attaching mid-loop guidance to it would conflate the two labels and bury the
// live instruction inside the "[Earlier steps compacted...]" preamble.
func appendGuidanceToUserMessage(messages []provider.ModelMessage, guidance string) {
	if len(messages) == 0 || guidance == "" {
		return
	}
	// Find the first user message that carries text content (not a tool result,
	// which has a ToolCallID) and is not the compaction-summary preamble. This is
	// the user's original turn prompt.
	for i, m := range messages {
		if m.Role != "user" || m.ToolCallID != "" {
			continue
		}
		var content string
		if m.Content != nil {
			json.Unmarshal(m.Content, &content)
		}
		if strings.HasPrefix(content, compactionSummaryPreamble) {
			continue
		}
		content += guidanceUserContent(guidance)
		messages[i].Content, _ = json.Marshal(content)
		return
	}
	// Fallback: no user text message — append to the first user message of any
	// kind (e.g. a tool-result-only turn), again skipping the compaction-summary
	// preamble. This is rare but keeps guidance from being silently dropped.
	for i, m := range messages {
		if m.Role != "user" {
			continue
		}
		var content string
		if m.Content != nil {
			json.Unmarshal(m.Content, &content)
		}
		if strings.HasPrefix(content, compactionSummaryPreamble) {
			continue
		}
		content += guidanceUserContent(guidance)
		messages[i].Content, _ = json.Marshal(content)
		return
	}
}

// isContextLengthError returns true when the provider rejects the request because
// the prompt exceeds the model's maximum context window. It prefers the provider's
// structured status/body classification and falls back to string matching.
func isContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *provider.APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsContextLength()
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	return provider.IsContextLengthMessage(msg) ||
		// Ollama returns a bare 400 with empty body when the prompt exceeds
		// the model's context window. The error format is "ollama API error 400: "
		// (with empty body after the colon-space) — treat this as context length
		// exceeded rather than a generic client error. We check for the empty body
		// specifically to avoid matching other 400 errors that have error details,
		// and for the ollama prefix so a body-less 400 from some other provider is
		// not relabelled as an overflow it never was.
		(strings.Contains(lower, "ollama api error 400") && strings.HasSuffix(strings.TrimSpace(lower), "400:"))
}

// retryAfterFromError returns the server-provided Retry-After hint carried by a
// structured provider error, or 0 when none is present.
func retryAfterFromError(err error) time.Duration {
	var apiErr *provider.APIError
	if errors.As(err, &apiErr) {
		return apiErr.RetryAfter
	}
	return 0
}

// compactionKeepRecent returns how many of the most recent messages to keep
// verbatim during compaction, scaled to the model's context window: a 200k model
// can afford far more verbatim recent context than an 8k one. An unknown window
// keeps the historical default of 12.
func compactionKeepRecent(contextWindow int) int {
	const minKeep, maxKeep = 8, 40
	if contextWindow <= 0 {
		return 12
	}
	keep := contextWindow / 10000
	if keep < minKeep {
		keep = minKeep
	}
	if keep > maxKeep {
		keep = maxKeep
	}
	return keep
}

// replaceOrAppendSummary returns system with a fresh compaction summary. When the
// prior summary is the last system entry (it was appended at the top of the
// iteration), it is replaced in place — because the new summary already folds the
// prior one in, appending would duplicate that content. Otherwise it is appended.
func replaceOrAppendSummary(system []string, prevSummary, newSummary string) []string {
	if n := len(system); n > 0 && prevSummary != "" && system[n-1] == prevSummary {
		system[n-1] = newSummary
		return system
	}
	return append(system, newSummary)
}

// compactRequest compacts a request's history in place: it summarizes the older
// messages (folding in the prior summary so nothing is lost), swaps the trimmed
// messages into streamReq, replaces the prior summary in the system prompt, and
// persists + announces the compaction. It returns the updated compaction summary
// (unchanged when the summarizer produced nothing). Shared by the proactive
// (pre-send, size-based) and reactive (on context-length error) compaction paths.
func (lr *LoopRunner) compactRequest(ctx context.Context, p provider.Provider, modelID string, sessionID session.SessionID, streamReq *provider.StreamRequest, prevSummary string, contextWindow int) string {
	summaryAddendum, compactedMsgs := lr.llmCompact(ctx, p, modelID, streamReq.Messages, prevSummary, contextWindow)
	streamReq.Messages = compactedMsgs
	newSummary := prevSummary
	if summaryAddendum != "" {
		// The new summary folds in the prior one, so replace rather than append to
		// avoid duplicating that content in the system prompt.
		streamReq.System = replaceOrAppendSummary(streamReq.System, prevSummary, summaryAddendum)
		newSummary = summaryAddendum
		if err := lr.Store.UpdateCompactionSummary(sessionID, summaryAddendum); err != nil {
			slog.Error("persist compaction summary", "err", err)
		}
	}
	lr.Bus.Publish("loop.compacted", map[string]string{"sessionId": string(sessionID)})
	return newSummary
}

// llmCompact summarizes the older portion of the conversation using the LLM
// itself, producing a rich technical summary that replaces the trimmed middle
// section. It returns a system-prompt addendum containing the summary and the
// recent messages to keep verbatim. When prevSummary is non-empty it is folded
// into the new summary so re-compaction never drops earlier context; keepRecent
// scales with the model's context window. Falls back to mechanical truncation if
// the LLM call fails.
func (lr *LoopRunner) llmCompact(ctx context.Context, p provider.Provider, modelID string, messages []provider.ModelMessage, prevSummary string, contextWindow int) (systemAddendum string, compacted []provider.ModelMessage) {
	keepRecent := compactionKeepRecent(contextWindow)
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
	const maxHistoryChars = 30_000
	if len(historyText) > maxHistoryChars {
		// Keep the MOST RECENT slice of old history — older exchanges are already
		// captured in the prior summary we fold in below (and matter less for
		// continuing the work than what happened most recently).
		historyText = "...(older history omitted — see the prior summary)\n" + historyText[len(historyText)-maxHistoryChars:]
	}

	// Anchored, structured template (modeled on opencode's compaction summary):
	// a fixed set of sections yields more complete and consistent summaries than
	// a free-form "summarize this" instruction, and the explicit "preserve exact
	// …" rule stops the model from paraphrasing away paths and symbols the next
	// steps depend on.
	sectionTemplate := "Organize the result into exactly the following sections. " +
		"Be specific and preserve exact file paths, function/type/variable names, commands run, and error " +
		"strings verbatim — the agent will continue the work from this summary alone, without re-reading the history.\n\n" +
		"## Objective\nThe user's overall goal, in one or two sentences.\n\n" +
		"## Important Details\nKey facts, decisions, and constraints established so far (with file paths and symbols).\n\n" +
		"## Work State\n### Completed\nWhat is finished — files created/modified (with paths) and code changed.\n" +
		"### In progress\nWhat is currently being worked on.\n" +
		"### Blocked / open questions\nAnything unresolved or waiting on the user.\n\n" +
		"## Next Steps\nThe concrete next actions to take, in order.\n\n" +
		"## Relevant Files\nFiles read or touched so far, each with a one-line note on its role.\n\n"

	// When a prior summary exists, instruct the model to MERGE it with the new
	// history rather than summarize the history alone — this is what makes
	// re-compaction non-destructive (earlier context is retained instead of
	// dropped when the prior summary is superseded).
	var summarizerPrompt string
	if prev := strings.TrimSpace(prevSummary); prev != "" {
		summarizerPrompt = "You are maintaining a running summary of a long coding session. A PRIOR SUMMARY of earlier " +
			"history is provided, followed by the NEW HISTORY since then. Produce a single, updated summary that MERGES " +
			"both — keep every still-relevant fact from the prior summary and integrate the new history; do not drop " +
			"earlier facts merely because the new history does not repeat them.\n\n" +
			sectionTemplate +
			"### PRIOR SUMMARY\n\n" + prev + "\n\n### NEW HISTORY\n\n" + historyText
	} else {
		summarizerPrompt = "Summarize the conversation history below. " + sectionTemplate +
			"Conversation history:\n\n" + historyText
	}

	// json.Marshal properly encodes the combined string, including any special
	// characters in historyText, producing a valid JSON string value for Content.
	summarizerContent, _ := json.Marshal(summarizerPrompt)

	summaryReq := provider.StreamRequest{
		Model: modelID,
		System: []string{
			"You are a precise technical conversation summarizer for a coding agent. Produce a complete, " +
				"structured summary that preserves every detail needed to continue the work without re-reading " +
				"the original history. Preserve exact file paths, symbol names, commands, error strings, and URLs " +
				"verbatim. Do not mention that the context was compacted.",
		},
		Messages: []provider.ModelMessage{
			{Role: "user", Content: summarizerContent},
		},
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

// subagentMaxSteps caps a delegated sub-agent's loop. 0 means "use the main
// agent default" (RunLoop maps 0 → 1000), so the sub-agent gets the same step
// budget as the interactive main agent. The cap is still a backstop against a
// misbehaving child spinning forever.
const subagentMaxSteps = 0

// RunTaskSession creates an ephemeral read-only sub-agent session, runs the full
// loop for a delegated investigation, and returns the sub-agent's final written
// answer. The session is deleted on completion. Called by tool.TaskTool via the
// tool.TaskFunc contract. The sub-agent (SubagentAgent) is depth-1 — its toolset
// omits `task`, so it cannot spawn further sub-agents.
func (lr *LoopRunner) RunTaskSession(ctx context.Context, description, prompt, dir, model string) (string, error) {
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

	label := description
	if label == "" {
		label = prompt
	}
	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   dir,
		Directory:   dir,
		Title:       "Task: " + truncateText(label, 60),
		Model:       model,
		SessionType: "subagent",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := lr.Store.Create(sess); err != nil {
		return "", fmt.Errorf("create subagent session: %w", err)
	}

	// Always clean up the ephemeral session when done.
	defer func() {
		if err := lr.Store.Delete(sess.ID); err != nil {
			slog.Warn("delete ephemeral subagent session", "session", sess.ID, "err", err)
		}
	}()

	// Create the initial user message carrying the delegated task.
	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: sess.ID,
		Role:      session.RoleUser,
		Agent:     "subagent",
		CreatedAt: session.Now(),
	}
	if err := lr.Store.CreateMessage(userMsg); err != nil {
		return "", fmt.Errorf("create subagent user message: %w", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: prompt})
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
		return "", fmt.Errorf("create subagent user part: %w", err)
	}

	// Run a capped child loop. Strip the parent's LoopControl so the child does
	// not drain the parent's mid-loop guidance or overwrite its cancel funcs
	// (same rationale as RunSearchSession), and clear Permissions — the sub-agent
	// is headless with no UI to answer prompts (and its read-only toolset needs
	// no gating anyway). Cancellation still propagates via ctx.
	childCtx := WithoutLoopControl(ctx)
	childRunner := *lr
	childRunner.MaxSteps = subagentMaxSteps
	childRunner.Permissions = nil
	if err := childRunner.RunLoop(childCtx, sess.ID, "subagent", 0, 0); err != nil {
		return "", fmt.Errorf("subagent loop: %w", err)
	}

	// Extract the sub-agent's final written answer.
	msgs, err := lr.Store.GetMessages(sess.ID, "", 1000)
	if err != nil {
		return "", fmt.Errorf("load subagent messages: %w", err)
	}
	answer := extractLastAssistantText(msgs)
	if strings.TrimSpace(answer) == "" {
		return "The sub-agent did not produce a final answer (it may have run out of steps). Try narrowing the task.", nil
	}
	return answer, nil
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// Token budgeting for proactive compaction lives in tokens.go
// (estimateRequestTokens / effectiveRequestTokens / compactionThresholdTokens).
