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
// history. Instead it is appended to the user's turn message content on the
// next LLM call — the model sees it as additional user input, not as a system
// directive. This avoids interactions with compaction boundaries,
// findLastTextUserMessageIndex turn-slicing, and agentic-memory
// <prior_context> filtering — all of which key off persisted user messages.
type LoopControl struct {
	mu sync.Mutex

	// pending guidance texts, drained at the top of each loop iteration.
	guidance []string

	// accumulated guidance texts that have already been injected into a prior
	// iteration of this loop run. These are re-appended on every subsequent
	// iteration so the model continuously sees all guidance the user has sent
	// during this turn — the guidance accumulates below the user's original
	// message rather than being a one-shot ephemeral system prompt. Cleared
	// when a new LoopControl is created (i.e. a new user turn starts a new
	// loop run).
	delivered []string

	// toolCancel cancels only the currently-running tool execution. It is
	// set before the parallel tool-execution block and cleared (and called)
	// after wg.Wait() returns. nil when no tools are running.
	toolCancel context.CancelFunc

	// streamCancel cancels only the currently-running LLM stream. It is set
	// before the StreamChat call and cleared (and called) after the stream is
	// fully consumed. nil when no stream is in progress. This is the key to
	// making guidance feel responsive: when the user sends guidance while the
	// model is generating (which can take tens of seconds), we cancel the
	// stream immediately so the loop proceeds to the next iteration and drains
	// the guidance instead of waiting for the full generation to complete.
	streamCancel context.CancelFunc
}

// NewLoopControl creates a fresh LoopControl.
func NewLoopControl() *LoopControl {
	return &LoopControl{}
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
// Drained texts are moved into the delivered accumulator so they can be
// re-injected on every subsequent iteration of this loop run — the guidance
// accumulates below the user's message rather than being a one-shot injection.
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
	// Move drained texts into the delivered accumulator.
	lc.delivered = append(lc.delivered, lc.guidance...)
	lc.guidance = lc.guidance[:0]
	return combined
}

// DeliveredGuidance returns all guidance texts that have been drained (and thus
// injected) during this loop run, joined into a single string. This is called
// by RunLoop on every iteration to re-append accumulated guidance to the user's
// turn message so the model continuously sees all mid-loop guidance. Returns ""
// when no guidance has been delivered yet.
func (lc *LoopControl) DeliveredGuidance() string {
	if lc == nil {
		return ""
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	if len(lc.delivered) == 0 {
		return ""
	}
	var combined string
	for i, g := range lc.delivered {
		if i > 0 {
			combined += "\n\n---\n\n"
		}
		combined += g
	}
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

// SetStreamCancel registers the cancel func for the currently-running LLM
// stream. Called by RunLoop just before calling StreamChat. The stored func is
// used by CancelStream to interrupt the stream without killing the loop.
// RunLoop clears it (and calls it to release the child context) after the
// stream is fully consumed.
func (lc *LoopControl) SetStreamCancel(cancel context.CancelFunc) {
	if lc == nil {
		return
	}
	lc.mu.Lock()
	lc.streamCancel = cancel
	lc.mu.Unlock()
}

// ClearStreamCancel removes the stored stream-cancel func without calling it.
// Called by RunLoop after the stream is fully consumed (or the guidance handler
// after it has called the cancel and the stream has wound down).
func (lc *LoopControl) ClearStreamCancel() {
	if lc == nil {
		return
	}
	lc.mu.Lock()
	lc.streamCancel = nil
	lc.mu.Unlock()
}

// CancelStream cancels the currently-running LLM stream, if any. Returns true
// if a stream cancellation was issued, false if no stream is in progress (or
// the control is nil). This is the primary mechanism for making mid-loop
// guidance feel responsive: it interrupts the model's generation so the loop
// can immediately proceed to the next iteration where it drains the guidance.
func (lc *LoopControl) CancelStream() bool {
	if lc == nil {
		return false
	}
	lc.mu.Lock()
	cancel := lc.streamCancel
	lc.streamCancel = nil
	lc.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// CancelAll cancels both the LLM stream and any running tool execution. This
// is the full "stop whatever you're doing right now" entry point used by the
// guidance handler so the user does not have to wait for the model to finish
// generating or for a long-running tool to complete before the loop picks up
// the new guidance. Returns true if at least one cancellation was issued.
func (lc *LoopControl) CancelAll() bool {
	if lc == nil {
		return false
	}
	streamed := lc.CancelStream()
	tooled := lc.CancelTool()
	return streamed || tooled
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

// WithoutLoopControl returns a copy of ctx with any LoopControl value cleared
// (set to nil). This is used by nested loop invocations — notably the
// deep_search tool's RunSearchSession — so the child loop does not inherit the
// parent session's LoopControl. Without this, the child loop would drain the
// parent's pending guidance (stealing mid-loop instructions) and overwrite the
// parent's stream/tool cancel funcs, hijacking the parent's guidance
// side-channel for the duration of the search.
func WithoutLoopControl(ctx context.Context) context.Context {
	return context.WithValue(ctx, loopControlKey{}, nil)
}

// --- permission gating integration ---

type permissionGatingKey struct{}

// WithPermissionGating marks a context as belonging to an interactive session
// that has a UI able to answer permission prompts. Only loops started with this
// flag will pause on an "Ask" permission decision; headless loops (task,
// breakdown, note, search, CLI) never carry it, so they never block waiting for
// an approval that no one is there to give.
func WithPermissionGating(ctx context.Context) context.Context {
	return context.WithValue(ctx, permissionGatingKey{}, true)
}

// PermissionGatingEnabled reports whether this context was started as an
// interactive, permission-gated session.
func PermissionGatingEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(permissionGatingKey{}).(bool)
	return enabled
}

// guidanceLabel is the heading prepended to accumulated guidance text when it
// is appended to the user's turn message. It labels the injected content so the
// model understands this is mid-loop guidance from the user, not a new turn.
const guidanceLabel = "[Mid-loop guidance — the user sent this while you were working. " +
	"Adjust your approach to incorporate it. Do not restart from scratch; continue from " +
	"where you are, but change direction as instructed.]"

// guidanceUserContent wraps accumulated guidance text into a labeled block
// suitable for appending to a user-role message. The model sees this as
// additional user input within the current turn, not as a system directive.
func guidanceUserContent(text string) string {
	return "\n\n" + guidanceLabel + "\n" + text
}