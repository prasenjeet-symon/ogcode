package worker

import (
	"log/slog"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// resumableSessionTypes are the interactive session kinds a worker marks
// resumable after an interrupted turn — mirroring the server's
// resumableSessionTypes (internal/server/resume_routes.go). A worker only hosts
// build-class sessions today (startAgent defaults the agent name to "build"),
// but the guard keeps repair semantics honest if that ever widens: task/index/
// note sessions are short and cheap to restart, so marking them resumable would
// be a surprise.
var resumableSessionTypes = map[string]bool{
	"":      true,
	"build": true,
	"plan":  true,
}

// recoverInterruptedSessions reconciles every interactive session in a
// workspace DB whose last turn was left unfinished by a crash, so a later
// resume request is valid. This is the worker-side port of the server's
// recoverInterruptedSessions (internal/server/resume_routes.go:103).
//
// Recovery is repair-only: ReconcileSession closes any dangling tool calls and
// claims the turn with an interruption record so the session's next request is
// well-formed. It never restarts a loop — resuming stays the operator's call
// (via the tunneled UI's resume button). A call that returns a non-nil target
// means the session is now resumable; we only count those so a boot log line
// reflects repair that actually happened.
func recoverInterruptedSessions(store *session.Store, runner *agent.LoopRunner, logger *slog.Logger) {
	if store == nil || runner == nil {
		return
	}
	sessions, err := store.ListAll()
	if err != nil {
		logger.Warn("worker boot recovery: list sessions", "err", err)
		return
	}
	recovered := 0
	for _, sess := range sessions {
		if !resumableSessionTypes[sess.SessionType] {
			continue
		}
		target, err := runner.ReconcileSession(sess.ID)
		if err != nil {
			logger.Warn("worker boot recovery: reconcile", "session", sess.ID, "err", err)
			continue
		}
		if target != nil {
			recovered++
		}
	}
	if recovered > 0 {
		logger.Info("recovered interrupted sessions; they can be resumed from the tunneled UI", "count", recovered)
	}
}
