package agent

import (
	"context"
	"log/slog"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// resolveRunEffort returns the reasoning effort a run asks its model for: the
// session's choice when the model takes it on the run's provider, otherwise ""
// — the vendor's default. It is read once per run, like the model: a turn that
// changed effort between steps would change part of the cached prompt mid-turn
// on Claude, and a choice made while a turn runs reaches the next one.
func resolveRunEffort(p provider.Provider, sess *session.Session, modelID string) string {
	if sess == nil || sess.Effort == "" {
		return ""
	}
	effort := provider.ResolveEffort(p, modelID, sess.Effort)
	if effort == "" {
		// Not an error: the session moved to a model with other levels, or the
		// endpoint changed under it. The model's default is the safe reading.
		slog.Info("session effort not taken by this model; running at its default",
			"session", sess.ID, "model", modelID, "effort", sess.Effort)
	}
	return effort
}

// runEffortKey carries the effort a run resolved, so the work it delegates — a
// task sub-agent — asks for the same depth the user picked for the session.
type runEffortKey struct{}

// withRunEffort stamps ctx with the run's effort.
func withRunEffort(ctx context.Context, effort string) context.Context {
	if effort == "" {
		return ctx
	}
	return context.WithValue(ctx, runEffortKey{}, effort)
}

// runEffortFrom returns the effort of the run ctx belongs to, or "".
func runEffortFrom(ctx context.Context) string {
	effort, _ := ctx.Value(runEffortKey{}).(string)
	return effort
}
