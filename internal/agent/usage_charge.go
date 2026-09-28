package agent

import (
	"context"
	"log/slog"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// Model calls made on a session's behalf outside its own step loop — a task or
// memory-recall sub-agent, the deep-search pipeline — are spent by that session,
// so their tokens belong in its totals. The work runs behind a tool hook that
// only receives a context, so RunLoop stamps its session on the way in and the
// spawned work charges whatever session it finds there. A child loop re-stamps
// its own session, so nested work lands on the child first and reaches the
// parent when the child is folded in.

type usageSessionKey struct{}

// withUsageSession marks ctx as running on behalf of sessionID.
func withUsageSession(ctx context.Context, sessionID session.SessionID) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, usageSessionKey{}, sessionID)
}

// usageSessionFrom returns the session ctx runs on behalf of, if any.
func usageSessionFrom(ctx context.Context) (session.SessionID, bool) {
	id, ok := ctx.Value(usageSessionKey{}).(session.SessionID)
	return id, ok && id != ""
}

// chargeUsage records a one-shot call's tokens, spent on providerID/modelID,
// against the session that owns ctx. A context with no session — a caller
// outside any agent turn — charges nothing, which is the same as before rather
// than a guess.
func (lr *LoopRunner) chargeUsage(ctx context.Context, providerID, modelID string, usage *provider.TokenUsage) {
	if id, ok := usageSessionFrom(ctx); ok {
		lr.recordUtilityUsage(id, providerID, modelID, usage)
	}
}

// foldChildUsage charges everything an ephemeral child session spent — every
// step of its loop plus its own utility calls — to the session that spawned it.
// It must run before the child is deleted: the child's counts live on its own
// messages and session row, and deleting it used to take them along, so a
// sub-agent's whole run vanished from every total.
func (lr *LoopRunner) foldChildUsage(ctx context.Context, childID session.SessionID) {
	parentID, ok := usageSessionFrom(ctx)
	if !ok || lr.Store == nil || parentID == childID {
		return
	}
	msgs, err := lr.Store.GetMessages(childID, "", 1_000_000)
	if err != nil {
		slog.Warn("fold child usage: load messages", "child", childID, "err", err)
		return
	}
	var tc session.TokenCounts
	for _, m := range msgs {
		if t := m.Info.Tokens; t != nil {
			addTokenCounts(&tc, *t)
		}
	}
	if sess, err := lr.Store.Get(childID); err == nil && sess != nil && sess.UtilityTokens != nil {
		addTokenCounts(&tc, *sess.UtilityTokens)
	}
	if tc == (session.TokenCounts{}) {
		return
	}
	lr.recordUtilityCounts(parentID, tc)
}

func addTokenCounts(dst *session.TokenCounts, src session.TokenCounts) {
	dst.Input += src.Input
	dst.Output += src.Output
	dst.Reasoning += src.Reasoning
	dst.CacheRead += src.CacheRead
	dst.CacheWrite += src.CacheWrite
}
