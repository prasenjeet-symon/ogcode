package agent

import (
	"context"
	"sync"
)

// LoopControl provides a side-channel for injecting mid-loop guidance into a
// running agent loop without starting a new user turn. It lets the user send
// a new instruction that the loop picks up at the top of the next iteration,
// and optionally cancel the currently-running tool call without killing the
// whole loop.
//
// The guidance is ephemeral: it is never persisted to the database message
// history. Instead it is injected as a trailing system-prompt entry on the
// next LLM call. This avoids interactions with compaction boundaries,
// findLastTextUserMessageIndex turn-slicing, and agentic-memory
// <prior_context> filtering — all of which key off persisted user messages.
type LoopControl struct {
	mu sync.Mutex

	// pending guidance texts, drained at the top of each loop iteration.
	guidance []string

	// preflight is a high-priority directive seeded from the user's prompt
	// before the loop starts. Unlike mid-loop guidance it is NOT drained — it
	// persists for the entire turn so every iteration (including tool-call
	// follow-ups) keeps the directive in scope. Injected as a system-reminder
	// entry positioned after the date reminder, before compaction/mid-loop
	// guidance, so it never pollutes the Anthropic cacheable first block.
	preflight string

	// toolCancel cancels only the currently-running tool execution. It is
	// set before the parallel tool-execution block and cleared (and called)
	// after wg.Wait() returns. nil when no tools are running.
	toolCancel context.CancelFunc
}

// NewLoopControl creates a fresh LoopControl.
func NewLoopControl() *LoopControl {
	return &LoopControl{}
}

// SetPreflight seeds the pre-flight guidance — a high-priority directive
// derived from the user's prompt that the loop injects on every iteration.
// Unlike mid-loop guidance it is not drained: it persists for the whole turn.
// Safe for concurrent use; typically called once before the loop starts.
func (lc *LoopControl) SetPreflight(text string) {
	if lc == nil || text == "" {
		return
	}
	lc.mu.Lock()
	lc.preflight = text
	lc.mu.Unlock()
}

// Preflight returns the current pre-flight directive, or "" when none is set.
// Called by RunLoop on each iteration to inject the directive into the system
// prompt. The value is not cleared — it stays until a new loop starts with a
// fresh LoopControl (or SetPreflight overwrites it).
func (lc *LoopControl) Preflight() string {
	if lc == nil {
		return ""
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return lc.preflight
}

// PushGuidance appends a guidance text to the pending queue. Safe for
// concurrent use (called from the HTTP handler goroutine while the loop
// runs in its own goroutine).
func (lc *LoopControl) PushGuidance(text string) {
	if lc == nil || text == "" {
		return
	}
	lc.mu.Lock()
	lc.guidance = append(lc.guidance, text)
	lc.mu.Unlock()
}

// DrainGuidance returns and clears all pending guidance texts. Called at the
// top of each loop iteration by RunLoop. Returns "" when nothing is pending.
func (lc *LoopControl) DrainGuidance() string {
	if lc == nil {
		return ""
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if len(lc.guidance) == 0 {
		return ""
	}
	// Join multiple guidance texts with separators — the user may send several
	// before the loop drains them.
	var combined string
	for i, g := range lc.guidance {
		if i > 0 {
			combined += "\n\n---\n\n"
		}
		combined += g
	}
	lc.guidance = lc.guidance[:0]
	return combined
}

// HasPendingGuidance reports whether there is undelivered guidance waiting.
func (lc *LoopControl) HasPendingGuidance() bool {
	if lc == nil {
		return false
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	return len(lc.guidance) > 0
}

// SetToolCancel registers the cancel func for the currently-running tool
// execution batch. Called by RunLoop before launching parallel tool calls.
// The stored func is used by CancelTool to interrupt only the tools, not the
// loop. RunLoop clears it (and calls it to release the context) after
// wg.Wait() returns.
func (lc *LoopControl) SetToolCancel(cancel context.CancelFunc) {
	if lc == nil {
		return
	}
	lc.mu.Lock()
	lc.toolCancel = cancel
	lc.mu.Unlock()
}

// ClearToolCancel removes the stored tool-cancel func without calling it.
// Called by RunLoop after tool execution completes normally.
func (lc *LoopControl) ClearToolCancel() {
	if lc == nil {
		return
	}
	lc.mu.Lock()
	lc.toolCancel = nil
	lc.mu.Unlock()
}

// CancelTool cancels the currently-running tool execution batch, if any.
// Returns true if a tool cancellation was issued, false if no tools are
// currently running (or the control is nil).
func (lc *LoopControl) CancelTool() bool {
	if lc == nil {
		return false
	}
	lc.mu.Lock()
	cancel := lc.toolCancel
	lc.toolCancel = nil
	lc.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// --- context integration ---

type loopControlKey struct{}

// WithLoopControl returns a context carrying the given LoopControl. RunLoop
// retrieves it via LoopControlFromContext. Call sites that do not need
// guidance (CLI one-shot, search sessions, indexer) simply don't wrap the
// context — LoopControlFromContext returns nil and the loop behaves exactly
// as before.
func WithLoopControl(ctx context.Context, lc *LoopControl) context.Context {
	return context.WithValue(ctx, loopControlKey{}, lc)
}

// LoopControlFromContext extracts the LoopControl from a context, or nil.
func LoopControlFromContext(ctx context.Context) *LoopControl {
	lc, _ := ctx.Value(loopControlKey{}).(*LoopControl)
	return lc
}

// guidancePrompt wraps guidance text in a <system-reminder> block so the model
// treats it as an authoritative mid-flight instruction. Using system-reminder
// (the same wrapper used for the current-date injection) keeps it outside the
// conversation message history — the model sees it as guidance, not as a new
// user turn that shifts the turn boundary.
func guidancePrompt(text string) string {
	return "<system-reminder>\nThe user has sent new guidance while you are working. " +
		"Adjust your approach to incorporate this. Do not restart from scratch — " +
		"continue from where you are, but change direction as instructed:\n\n" + text + "\n</system-reminder>"
}

// preflightPrompt wraps the pre-flight directive in a <system-reminder> block
// that elevates the user's prompt to the highest priority. Unlike mid-loop
// guidance (which says "adjust your approach"), this tells the model that the
// user's request is the primary objective for this turn and must be treated
// with maximum focus and precedence over all other context. It is injected on
// every loop iteration — including tool-call follow-ups — so the directive is
// never lost as the conversation grows.
func preflightPrompt(text string) string {
	return "<system-reminder>\n## Pre-flight guidance — highest priority\n\n" +
		"The following is the user's primary directive for this session. " +
		"Treat it as your highest-priority objective. Everything you do — " +
		"tool calls, file edits, research, responses — should serve this " +
		"directive above all other context in this conversation:\n\n" + text + "\n</system-reminder>"
}