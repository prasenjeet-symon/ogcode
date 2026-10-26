package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/timer"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// reminderPollInterval is how often a running server polls the timer store for
// deadlines that have passed. A package var, not a const, so a test can shrink
// it and watch the ticker deliver without waiting ten seconds.
var reminderPollInterval = 10 * time.Second

// How often the poller prunes terminal rows, and how old a terminal row may
// get before its prune. Nothing reads a delivered or cancelled row — delivery
// is once and the session list shows live rows only — but the grace keeps
// history a debugging session can still query by hand, and the tick spacing
// keeps the ordinary poll a cheap claim scan. Vars, not consts, so a test can
// force a prune on every tick.
var (
	timerPruneAfter      = 7 * 24 * time.Hour
	timerPruneEveryTicks = 60
)

// scheduleTool carries out one reminder operation for the asking session. It is
// the server half of tool.ScheduleTool, wired in from serve() to keep the
// tool→server import direction — the same seam the board and teammate tools
// ride.
func (s *Server) scheduleTool(ctx context.Context, sessionID string, req tool.ScheduleRequest) (tool.ScheduleResult, error) {
	if s.timers == nil {
		// ScheduleResult carries data, not prose — a refusal rides the error and
		// ScheduleTool.Execute prints it as the tool result.
		return tool.ScheduleResult{}, errors.New("reminders are not available in this environment")
	}
	ctx, cancel := context.WithTimeout(ctx, boardOpTimeout)
	defer cancel()

	switch strings.TrimSpace(req.Op) {
	case "create":
		return s.scheduleCreate(ctx, sessionID, req)
	case "cancel":
		return s.scheduleCancel(ctx, sessionID, req)
	case "list":
		return s.scheduleList(ctx, sessionID)
	}
	return tool.ScheduleResult{}, fmt.Errorf("unknown schedule op %q", req.Op)
}

// scheduleCreate validates and stores one deadline. The note is required, and
// exactly one of delaySeconds or fireAt (an RFC3339 instant) carries the
// deadline: both is ambiguous, neither has nothing to wait on, and a fireAt in
// the past would be an instant no-op the agent never asked for.
func (s *Server) scheduleCreate(ctx context.Context, sessionID string, req tool.ScheduleRequest) (tool.ScheduleResult, error) {
	// The tool validates this in ScheduleTool.Execute before the call gets
	// here; these guards are defense for direct callers. The note is required,
	// and exactly one of delaySeconds or fireAt (an RFC3339 instant) carries
	// the deadline: both is ambiguous, neither has nothing to wait on, and a
	// fireAt in the past would be an instant no-op the agent never asked for.
	// A negative delaySeconds does not fall under "neither given" — it asked
	// to wait, for negative seconds — so it is refused below with its own
	// message. An over-cap delaySeconds is refused here too, not converted: past
	// tool.MaxDelaySeconds the seconds-to-nanoseconds product wraps negative
	// and the deadline would land in the past, firing the note instantly.
	note := strings.TrimSpace(req.Note)
	if note == "" {
		return tool.ScheduleResult{}, errors.New("note is required — say what the reminder is for")
	}

	fireAt := time.Time{}
	if req.DelaySeconds < 0 {
		// A negative delaySeconds carries a broken countdown, not "neither was
		// given": it asked to wait, for negative seconds. The wording is
		// pinned whole and mirrored in tool/schedule.go.
		return tool.ScheduleResult{}, errors.New("delaySeconds must be a positive number of seconds")
	}
	set := 0
	if req.DelaySeconds > 0 {
		if req.DelaySeconds > tool.MaxDelaySeconds {
			return tool.ScheduleResult{}, fmt.Errorf("delaySeconds %d is too long — the cap is %d seconds (366 days); use fireAt for a further deadline", req.DelaySeconds, tool.MaxDelaySeconds)
		}
		fireAt = time.Now().Add(time.Duration(req.DelaySeconds) * time.Second)
		set++
	}
	if req.FireAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.FireAt)
		if err != nil {
			return tool.ScheduleResult{}, fmt.Errorf("fireAt is not RFC3339: %v — e.g. %s", err, time.Now().UTC().Format(time.RFC3339))
		}
		if !parsed.After(time.Now()) {
			return tool.ScheduleResult{}, fmt.Errorf("fireAt %s has already passed — give a future instant, or a delaySeconds instead", parsed.Format(time.RFC3339))
		}
		fireAt = parsed
		set++
	}
	if set == 0 {
		return tool.ScheduleResult{}, errors.New("a reminder needs a deadline: delaySeconds>0 or a future fireAt, exactly one of the two")
	}
	if set > 1 {
		return tool.ScheduleResult{}, errors.New("give delaySeconds or fireAt, not both — exactly one deadline")
	}

	t, err := s.timers.Create(ctx, sessionID, note, fireAt)
	if err != nil {
		return tool.ScheduleResult{}, err
	}
	slog.Info("reminder scheduled", "timer", t.ID, "session", sessionID, "fireAt", fireAt.Format(time.RFC3339))
	// The delay form's echo is filled here: ScheduleResult.FireAt is the real
	// instant the reminder fires, which a delaySeconds create never sent.
	return tool.ScheduleResult{
		ID:     t.ID,
		FireAt: t.FireAt.Format(time.RFC3339),
	}, nil
}

// scheduleCancel cancels one of the session's own reminders before it fires.
func (s *Server) scheduleCancel(ctx context.Context, sessionID string, req tool.ScheduleRequest) (tool.ScheduleResult, error) {
	if strings.TrimSpace(req.ID) == "" {
		return tool.ScheduleResult{}, errors.New("cancel needs the reminder id from the create result")
	}
	t, err := s.timers.Cancel(ctx, sessionID, req.ID)
	if errors.Is(err, timer.ErrNotFound) {
		return tool.ScheduleResult{}, fmt.Errorf("no reminder %q belongs to this session", req.ID)
	}
	if err != nil {
		return tool.ScheduleResult{}, err
	}
	return tool.ScheduleResult{ID: t.ID}, nil
}

// scheduleList shows the session its own reminders, the ones still pending
// first.
func (s *Server) scheduleList(ctx context.Context, sessionID string) (tool.ScheduleResult, error) {
	due, err := s.timers.List(ctx, sessionID)
	if err != nil {
		return tool.ScheduleResult{}, err
	}
	if len(due) == 0 {
		return tool.ScheduleResult{}, nil
	}
	timers := make([]tool.ScheduledTimer, 0, len(due))
	for _, t := range due {
		timers = append(timers, tool.ScheduledTimer{
			ID:     t.ID,
			Note:   strings.TrimSpace(t.Note),
			FireAt: t.FireAt.Format(time.RFC3339),
			Status: t.Status,
		})
	}
	return tool.ScheduleResult{Timers: timers}, nil
}

// pollReminders claims every reminder whose deadline has passed and delivers
// each into its own session's running loop, forever, until ctx ends or the
// server stops. It is the background goroutine serve() launches beside the
// other long-lived ones; a server without a timer store (tests, other
// embedders) launches nothing.
//
// Delivery is push-like, not turn-starting: a reminder can land mid-turn, while
// an idle session keeps its row unclaimed for its next turn's catch-up.
//
// The poller runs on the caller's ctx, which Stop() never cancels — Stop ends
// the server by closing stopCh. The handle is therefore re-read under its own
// mutex on every pass and selected beside the ticker, so once Stop() has
// closed it the poller exits at once instead of ticking on against a database
// shutdown is closing. A nil stopCh (the goroutine starts before serve()
// allocates one) never fires its select case, and Stop() is a no-op until the
// channel exists, so the two windows cannot lose a stop.
func (s *Server) pollReminders(ctx context.Context) {
	if s.timers == nil {
		return
	}
	// The first pass runs immediately, so rows left due by a previous process
	// are delivered without waiting out the first interval — the same
	// refresh-then-tick shape startFeatureFlagRefresh uses — and its terminal
	// rows are pruned here too: a restart is one prune the periodic cadence
	// below would otherwise make wait out a whole interval.
	s.pollDueReminders(ctx)
	s.pruneTimers(ctx)
	ticker := time.NewTicker(reminderPollInterval)
	defer ticker.Stop()
	ticks := 0
	for {
		s.stopMu.Lock()
		stopCh := s.stopCh
		s.stopMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-stopCh: // nil until serve() allocates it; a nil case never fires
			return
		case <-ticker.C:
		}
		ticks++
		if ticks%timerPruneEveryTicks == 0 {
			s.pruneTimers(ctx)
		}
		s.pollDueReminders(ctx)
	}
}

// pollDueReminders is one tick: claim the deadlines the store says have passed
// and deliver each. An error is logged, never fatal — the poller runs for the
// server's lifetime and the next tick simply re-claims. The claim runs on the
// poll ctx, not a background one, so a Stop() that lands mid-claim cuts the
// query off instead of issuing a fresh one against a database that is already
// closing.
func (s *Server) pollDueReminders(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, boardOpTimeout)
	defer cancel()
	due, err := s.timers.ClaimDue(ctx)
	if err != nil {
		slog.Warn("timer poll failed", "err", err)
		return
	}
	for _, t := range due {
		s.deliverReminder(ctx, t)
	}
}

// pruneTimers deletes the delivered and cancelled rows the cutoff has passed.
// It is the poller's only housekeeping and runs every timerPruneEveryTicks
// ticks, so the ordinary poll stays a claim scan; anything still live —
// pending or due, however stale — is left for its session.
func (s *Server) pruneTimers(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, boardOpTimeout)
	defer cancel()
	n, err := s.timers.Prune(ctx, time.Now().Add(-timerPruneAfter))
	if err != nil {
		slog.Warn("timer prune failed", "err", err)
		return
	}
	if n > 0 {
		slog.Info("timer prune removed old reminders", "count", n)
	}
}

// deliverReminder pushes one due reminder into its session's loop, the way
// reportTeammateDone pushes a teammate's completion. When no loop is running
// for the session it does nothing beyond the log: the row stays due, and
// catchUpReminders hands it over at the session's next turn start — a reminder
// never starts a turn.
//
// The push is tracked, not stamped: the row is marked delivered only when the
// step that drains the notice actually dispatches a request carrying its text,
// so a turn that dies first leaves the row for the next turn's catch-up —
// at-least-once, never lost. (The store marks the row delivered rather than
// reported; same role the board's notified_at plays for a teammate.)
func (s *Server) deliverReminder(ctx context.Context, t timer.Timer) {
	id := session.SessionID(t.SessionID)
	lc := s.loopControl(id)
	if lc == nil {
		// Nothing is running to hand it to. The deadline has still come —
		// the session's next turn delivers it then.
		slog.Info("reminder came due while its session was idle", "timer", t.ID, "session", t.SessionID)
		return
	}
	notice := reminderNotice(t)
	// Push first: between the push and the tracking the worst outcome is a
	// reminder concluded twice; tracking first and missing the push could
	// only lose it again.
	lc.PushNotice(agent.Notice{Text: notice, Cause: agent.ReminderCause})
	if s.loopRunner != nil {
		s.loopRunner.TrackBoardReport(lc, id, agent.BoardReport{
			Text: notice,
			Mark: s.markTimerDelivered(t.ID),
		})
	}
	slog.Info("reminder delivered into a running turn", "timer", t.ID, "session", t.SessionID)
}

// catchUpReminders hands a starting turn the reminders its session scheduled
// that came due while the session was not running, as pending notices. It is
// the LoopRunner's CatchUpReminders hook, so it runs before the history is
// loaded and the step loop injects what it queued at the tail of the first
// request — the same handover catchUpTeammates performs for completions.
//
// Like the mid-turn push, a catch-up push is tracked rather than stamped: the
// row's mark runs when a step of this turn dispatches the notice. A turn that
// dies before its first step stamped nothing, so the row stays due and the
// next turn start hands it over again.
func (s *Server) catchUpReminders(ctx context.Context, sessionID session.SessionID, dir string) error {
	if s.timers == nil || dir != s.dir {
		return nil
	}
	lc := s.loopControl(sessionID)
	if lc == nil {
		return nil
	}
	// The predecessor turn left no queued notice alive — its LoopControl died
	// with the turn — so drop the tracker's stale entries here; the push below
	// hands their rows over, and nothing later concludes them twice.
	if s.loopRunner != nil {
		s.loopRunner.TrimBoardReports(sessionID, lc)
	}
	due, err := s.timers.Undelivered(ctx, string(sessionID))
	if err != nil {
		return err
	}
	var fallback []string
	for _, t := range due {
		notice := reminderNotice(t)
		lc.PushNotice(agent.Notice{Text: notice, Cause: agent.ReminderCause})
		if s.loopRunner == nil {
			// Defensive: no runner keeps a tracker, so nothing would ever
			// conclude a tracked report and these rows would sit due forever.
			// A direct mark is the only stamp available.
			fallback = append(fallback, t.ID)
			continue
		}
		s.loopRunner.TrackBoardReport(lc, sessionID, agent.BoardReport{
			Text: notice,
			Mark: s.markTimerDelivered(t.ID),
		})
	}
	if len(fallback) > 0 {
		if err := s.timers.MarkDelivered(ctx, fallback); err != nil {
			return err
		}
	}
	if len(due) > 0 {
		slog.Info("timer: caught a turn up on its due reminders", "session", sessionID, "reminders", len(due))
	}
	return nil
}

// markTimerDelivered builds the mark closure one reminder report carries. Its
// own context: the push's timeout has long expired by the time a running
// turn's step drains the notice.
func (s *Server) markTimerDelivered(id string) func() error {
	return func() error {
		mctx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
		defer cancel()
		return s.timers.MarkDelivered(mctx, []string{id})
	}
}

// reminderNotice is the one-line notice a due reminder delivers: what it is,
// when its deadline was set to, and the note the session left for itself —
// the date rides along because a reminder can sit scheduled far longer than
// a day, and a bare "15:04" cannot say which day a long-set deadline fires
// on. The text is the report's tracking key too — the notice and its
// BoardReport must carry it byte-identical, which is why both read it from
// one call.
func reminderNotice(t timer.Timer) string {
	note := strings.TrimSpace(t.Note)
	return fmt.Sprintf("Scheduled reminder came due (set for %s): %s", t.FireAt.Format("Jan 2, 15:04"), note)
}
