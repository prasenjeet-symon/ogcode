package tool

import "context"

// RecallScope tells the memory_map tool which turn summaries the current recall
// is allowed to see. It is carried on the context rather than passed as tool
// arguments so the model can never widen its own scope or point recall at
// another conversation — the recall runner sets it from trusted values.
type RecallScope struct {
	// Scope is "project" (every summary in the workspace) or "session" (only the
	// target conversation's summaries).
	Scope string
	// SessionID is the conversation to restrict to when Scope is "session".
	SessionID string
	// ProjectID is the resolved workspace key whose summaries are in play.
	ProjectID string
}

type recallScopeKey struct{}

// WithRecallScope returns a context carrying scope for the memory_map tool.
func WithRecallScope(ctx context.Context, scope RecallScope) context.Context {
	return context.WithValue(ctx, recallScopeKey{}, scope)
}

// RecallScopeFromContext returns the recall scope set by the recall runner, if
// any. ok is false when the tool is invoked outside a scoped recall session.
func RecallScopeFromContext(ctx context.Context) (RecallScope, bool) {
	s, ok := ctx.Value(recallScopeKey{}).(RecallScope)
	return s, ok
}

// RecallFunc runs the read-only memory recall sub-agent for a question at a
// given scope and returns its concise written answer. Implemented by
// agent.LoopRunner.RunMemoryRecallSession and wired in from server.go to avoid
// the tool→agent import cycle, exactly like TaskFunc.
type RecallFunc func(ctx context.Context, question, scope, targetSessionID, dir, model string) (string, error)

// RecallBarrier lets a recall wait for in-flight background turn-summary work to
// settle before it runs, so it never reads a stale or partial index.
// memfile.Manager satisfies it; a nil barrier waits for nothing.
type RecallBarrier interface {
	Wait(project string)
}
