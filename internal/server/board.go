package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/board"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

const (
	// boardWaitDefaultSeconds and boardWaitMaxSeconds bound teammate wait.
	boardWaitDefaultSeconds = 60
	boardWaitMaxSeconds     = 120

	// boardHandleAttempts bounds how often a minted handle is re-rolled when
	// RegisterFresh says its name is taken: a four-hex-character suffix has
	// 65,536 combinations, so exhausting this many rolls means something is
	// deeply wrong, not merely unlucky.
	boardHandleAttempts = 8

	// boardOpTimeout bounds one board operation outside the tool call itself
	// (a roster read, a status write at loop end), so a stuck query cannot hold
	// a loop's cleanup open.
	boardOpTimeout = 10 * time.Second
)

// boardTool carries out one board operation for the build agent. It is the
// server half of tool.BoardTool, wired in from serve() to keep the
// tool→server import direction.
func (s *Server) boardTool(ctx context.Context, sessionID string, req tool.BoardRequest) (tool.BoardResult, error) {
	if s.board == nil {
		return tool.BoardResult{Notice: "The board is not available in this environment."}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, boardOpTimeout)
	defer cancel()

	id := session.SessionID(sessionID)
	sess, err := s.store.Get(id)
	if err != nil {
		return tool.BoardResult{}, err
	}
	if sess == nil {
		return tool.BoardResult{}, fmt.Errorf("session %s not found", sessionID)
	}
	// Build-only, like the tool itself: the roster and the completion report are
	// gated too, so this is the third door to the same room.
	if sess.SessionType != "build" {
		return tool.BoardResult{Notice: "The board is only available to the build agent."}, nil
	}
	if _, err := s.boardHandle(ctx, id, sess.SessionType); err != nil {
		return tool.BoardResult{}, err
	}

	roster, err := s.board.Roster(ctx)
	if err != nil {
		return tool.BoardResult{}, err
	}
	return tool.BoardResult{
		Roster: roster,
		Notice: fmt.Sprintf("%d agents in this project, %d still working.", len(roster), liveCount(roster)),
	}, nil
}

// liveCount is how many handles are still working: running, or idle between
// turns of an interactive session.
func liveCount(roster []board.Handle) int {
	n := 0
	for _, h := range roster {
		if h.Status == board.StatusRunning || h.Status == board.StatusIdle {
			n++
		}
	}
	return n
}

// boardHandle returns the handle a session is on the board as, joining it when
// the session has none yet.
func (s *Server) boardHandle(ctx context.Context, id session.SessionID, agentName string) (board.Handle, error) {
	h, err := s.board.BySession(ctx, string(id))
	if err != nil {
		return board.Handle{}, err
	}
	if h.Handle != "" {
		return h, nil
	}
	return s.joinBoard(ctx, id, agentName)
}

// mintFreeHandle claims reg's row on the board under a name newName() mints,
// rolling up to attempts names: RegisterFresh refuses a name already on the
// board, and this rolls a new name and tries again instead of failing. The
// closure exists so a test can force collisions deterministically; callers in
// production wrap board.NewHandle(role). A name a caller chose by hand keeps
// its own registration path.
func (s *Server) mintFreeHandle(ctx context.Context, reg board.Handle, attempts int, newName func() string) (board.Handle, error) {
	var taken error
	for i := 0; i < attempts; i++ {
		reg.Handle = newName()
		h, err := s.board.RegisterFresh(ctx, reg)
		if err == nil {
			return h, nil
		}
		if !errors.Is(err, board.ErrHandleTaken) {
			return board.Handle{}, err
		}
		taken = err
	}
	if taken == nil {
		taken = board.ErrHandleTaken
	}
	return board.Handle{}, fmt.Errorf("no free handle after %d attempts: %w", attempts, taken)
}

// joinBoard puts a session on the roster. Only the build agent joins: the plan
// agent has a transcript of its own and no board tool, and a headless task
// session works in a worktree that is deleted when it finishes, so neither has
// a teammate to coordinate with.
func (s *Server) joinBoard(ctx context.Context, id session.SessionID, agentName string) (board.Handle, error) {
	if agentName != "build" {
		return board.Handle{}, fmt.Errorf("the %s agent does not join the board", agentName)
	}
	// The name is minted here and belongs to nobody until a row claims it, so
	// it is registered fresh: a roll that lands on a name a live row already
	// holds is rolled again, not taken over with a new session underneath it.
	h, err := s.mintFreeHandle(ctx, board.Handle{
		SessionID: string(id),
		Role:      agentName,
		Kind:      board.KindSession,
		Status:    board.StatusRunning,
	}, boardHandleAttempts, func() string {
		return board.NewHandle("build")
	})
	if err != nil {
		return board.Handle{}, err
	}
	s.publishBoardAgent(h)
	slog.Info("board: joined", "session", id, "handle", h.Handle)
	return h, nil
}

// boardTurnStart puts a session on the roster as running before its loop starts.
//
// It is idempotent on purpose: a teammate is registered at spawn and every
// session keeps its handle from its first turn, so calling joinBoard for an
// already-registered session fails the UNIQUE(board_agent.session_id) index
// instead of joining anything — and since Register is what writes
// StatusRunning, that failure left the roster reporting the session idle for
// the whole turn.
func (s *Server) boardTurnStart(ctx context.Context, id session.SessionID, agentName string) error {
	if s.board == nil {
		return nil
	}
	h, err := s.boardHandle(ctx, id, agentName)
	if err != nil {
		return err
	}
	if h.Kind != board.KindSession || h.Status == board.StatusRunning {
		return nil
	}
	if err := s.board.SetStatus(ctx, h.Handle, board.StatusRunning); err != nil {
		return fmt.Errorf("mark handle %s running: %w", h.Handle, err)
	}
	h.Status = board.StatusRunning
	s.publishBoardAgent(h)
	slog.Info("board: turn started", "session", id, "handle", h.Handle)
	return nil
}

// leaveBoard records that a session's loop ended: a teammate is done, a session
// is idle and ready for the next turn. A status that already recorded the truth
// is left alone, so a cancelled teammate does not end up reported as done.
//
// A teammate ending is the report: the spawner is told its child is done and
// what the child's last message said, without the teammate posting anything.
func (s *Server) leaveBoard(id session.SessionID) {
	if s.board == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
	defer cancel()

	h, err := s.board.BySession(ctx, string(id))
	if err != nil || h.Handle == "" {
		return
	}
	status := board.StatusIdle
	if h.Kind == board.KindTeammate || h.Kind == board.KindTask {
		status = board.StatusDone
	}
	// Conditional, not SetStatus: a teammate stopped while its loop was winding
	// down is already terminal, and the stop is the news its spawner is owed.
	// Writing done here would overwrite that and report a completion that never
	// happened — the loop keeps running for a moment after its cancel.
	moved, err := s.board.SetStatusUnlessTerminal(ctx, h.Handle, status)
	if err != nil {
		slog.Warn("board: could not record loop end", "handle", h.Handle, "err", err)
		return
	}
	if !moved {
		return
	}
	h.Status = status
	h.UpdatedAt = time.Now()
	s.publishBoardAgent(h)
	if status != board.StatusDone {
		return
	}
	s.reportTeammateDone(ctx, h, s.lastAssistantText(id))
}

// reportTeammateDone tells a teammate's spawner that the child finished. When
// the spawner is running the report is pushed into the turn it is already in,
// and the row is stamped reported by the step that drains the notice
// (TrackBoardReport → BoardNoticesDelivered), not by this push: a turn that
// ends before its next step takes the turn-scoped notice with it, and a row
// stamped here would say the report was delivered while it never was. When the
// spawner is between turns the row stays unreported, and its next turn start
// hands the report over instead (catchUpTeammates).
func (s *Server) reportTeammateDone(ctx context.Context, h board.Handle, text string) {
	if h.SpawnedBy == "" || h.SessionID == "" {
		return
	}
	parent, err := s.board.Get(ctx, h.SpawnedBy)
	if err != nil || parent.Handle == "" {
		return
	}
	parentID := session.SessionID(parent.SessionID)
	lc := s.loopControl(parentID)
	if lc == nil {
		// Nothing is running to hand it to. The report is still news — its
		// notified_at is behind the row's updated_at — so the parent's next turn
		// delivers it.
		slog.Info("teammate finished while its spawner was idle", "handle", h.Handle, "to", parent.Handle)
		return
	}
	report := teammateReportNotice(h, text)
	// Push first: between the push and the tracking the worst outcome is a
	// report concluded twice (an at-least-once duplicate); tracking first and
	// missing the push could only lose it again.
	lc.PushNotice(agent.Notice{Text: report, Cause: "teammate"})
	if s.loopRunner != nil {
		s.loopRunner.TrackBoardReport(lc, parentID, agent.BoardReport{
			Text: report,
			Mark: func() error {
				// Its own context: the push's timeout has long expired by the
				// time a running turn's step drains the notice.
				markCtx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
				defer cancel()
				return s.board.MarkReported(markCtx, []string{h.Handle})
			},
		})
	}
	slog.Info("board: reported a teammate's completion", "handle", h.Handle, "to", parent.Handle)
}

// teammateReportNotice is the one-paragraph report a spawner receives about a
// finished teammate: its status, where its session is, and what it last said.
func teammateReportNotice(h board.Handle, text string) string {
	verb := "is done"
	switch h.Status {
	case board.StatusStopped:
		verb = "was stopped"
	case board.StatusStale:
		verb = "went stale — its server exited before it finished"
	}
	who := "Teammate"
	if h.Kind == board.KindTask {
		who = "Background task"
	}
	head := fmt.Sprintf("%s %s %s — /session/%s", who, h.Handle, verb, h.SessionID)
	body := strings.TrimSpace(text)
	if body == "" {
		return head + ".\n\nIt left no final message."
	}
	return head + ".\n\nIts last message:\n\n" + body
}

// lastAssistantText returns the newest assistant message's text: a teammate's
// result, or the question it stopped on.
func (s *Server) lastAssistantText(id session.SessionID) string {
	msgs, err := s.store.GetMessages(id, "", 20)
	if err != nil {
		slog.Warn("board: could not read the teammate's last message", "session", id, "err", err)
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Info.Role != session.RoleAssistant {
			continue
		}
		var texts []string
		for _, p := range msgs[i].Parts {
			if p.Type != session.PartText {
				continue
			}
			var data session.TextPartData
			if err := json.Unmarshal(p.Data, &data); err != nil || data.Text == "" {
				continue
			}
			texts = append(texts, data.Text)
		}
		return strings.Join(texts, "\n\n")
	}
	return ""
}

// catchUpTeammates hands a starting turn the completions it has not been told
// about yet, as pending notices. It is the LoopRunner's CatchUpTeammates hook,
// so it runs before the history is loaded and the step loop injects what it
// queued at the tail of the first request.
//
// It reads the caller's own children only: a completion is owed to the agent
// that spawned it, and nobody else's turn has anything to do with it.
//
// A report is stamped delivered only once its text has been handed to a request
// that was actually dispatched. The stamp waits for the drain, exactly like the
// mid-turn push in reportTeammateDone: each pushed report is tracked, and its
// mark runs when a step of this turn carries the notice into a request. A turn
// that dies before its first step stamped nothing, so the row stays unreported
// and the next turn start hands the report over again — at-least-once, never
// lost.
func (s *Server) catchUpTeammates(ctx context.Context, sessionID session.SessionID, dir string) error {
	if s.board == nil || dir != s.dir {
		return nil
	}
	lc := s.loopControl(sessionID)
	if lc == nil {
		return nil
	}
	// The predecessor turn left no queued report alive — its notices died with
	// its LoopControl — so drop the tracker's stale entries for this session;
	// the push below hands their rows over, and nothing later concludes them
	// twice.
	if s.loopRunner != nil {
		s.loopRunner.TrimBoardReports(sessionID, lc)
	}
	h, err := s.board.BySession(ctx, string(sessionID))
	if err != nil {
		return err
	}
	if h.Handle == "" {
		return nil
	}
	children, err := s.board.Children(ctx, h.Handle)
	if err != nil {
		return err
	}
	pushed := 0
	var fallback []string
	for _, child := range children {
		if !child.Unreported() {
			continue
		}
		text := ""
		if child.SessionID != "" {
			text = s.lastAssistantText(session.SessionID(child.SessionID))
		}
		report := teammateReportNotice(child, text)
		// Pushed the way a mid-turn completion is: the tracking follows the
		// push, so the worst outcome in between is a report concluded twice,
		// never one lost.
		lc.PushNotice(agent.Notice{Text: report, Cause: "teammate"})
		pushed++
		if s.loopRunner == nil {
			// Defensive: no runner keeps a tracker, so nothing would ever
			// conclude a tracked report and these rows would sit unreported
			// forever. The old push-time stamp is the only stamp available.
			fallback = append(fallback, child.Handle)
			continue
		}
		name := child.Handle
		s.loopRunner.TrackBoardReport(lc, sessionID, agent.BoardReport{
			Text: report,
			Mark: func() error {
				// Its own context: the catch-up's bound has long expired by the
				// time a running turn's step drains the notice.
				mctx, mcancel := context.WithTimeout(context.Background(), boardOpTimeout)
				defer mcancel()
				return s.board.MarkReported(mctx, []string{name})
			},
		})
	}
	if len(fallback) > 0 {
		if err := s.board.MarkReported(ctx, fallback); err != nil {
			return err
		}
	}
	if pushed > 0 {
		slog.Info("board: caught up a turn on its teammates", "session", sessionID, "teammates", pushed)
	}
	return nil
}

// teammateTool carries out one teammate operation for the build agent.
func (s *Server) teammateTool(ctx context.Context, sessionID string, req tool.TeammateRequest) (tool.TeammateResult, error) {
	if s.board == nil {
		return tool.TeammateResult{Notice: "Teammates are not available in this environment."}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, boardOpTimeout)
	defer cancel()

	id := session.SessionID(sessionID)
	sess, err := s.store.Get(id)
	if err != nil {
		return tool.TeammateResult{}, err
	}
	if sess == nil {
		return tool.TeammateResult{}, fmt.Errorf("session %s not found", sessionID)
	}
	if sess.SessionType != "build" {
		return tool.TeammateResult{Notice: "Teammates are only available to the build agent."}, nil
	}
	h, err := s.boardHandle(ctx, id, sess.SessionType)
	if err != nil {
		return tool.TeammateResult{}, err
	}

	switch req.Op {
	case "spawn":
		return s.spawnTeammate(ctx, sess, h, req)
	case "wait":
		return s.waitTeammate(ctx, h, req)
	case "stop":
		return s.stopTeammate(ctx, req.Handle)
	case "status":
		return s.statusTeammate(ctx, h, req.Handle)
	}
	return tool.TeammateResult{Notice: fmt.Sprintf("Unknown teammate op %q.", req.Op)}, nil
}

// spawnTeammate starts one build session in the same directory, registered as a
// teammate of the caller. Any build session may spawn, a teammate included, and
// no ceiling is put on how many run at once: fan-out is bounded by what the
// caller is willing to spend, not by a constant.
func (s *Server) spawnTeammate(ctx context.Context, parent *session.Session, parentHandle board.Handle, req tool.TeammateRequest) (tool.TeammateResult, error) {
	_, live := s.liveTeammates(ctx)

	handle := ""
	var typedName bool
	if name := strings.TrimSpace(req.Name); name != "" {
		typedName = true
		slug := board.Slug(name)
		if slug == "" {
			return tool.TeammateResult{Notice: "That name has no letters or digits to build a handle from — pick a short one like auth-worker."}, nil
		}
		if board.ReservedHandles[slug] {
			return tool.TeammateResult{Notice: fmt.Sprintf("%q is a reserved handle (user, system and board are the board's own) — pick another name.", slug)}, nil
		}
		// The friendly pre-check for a name the caller chose by hand — the
		// register below refuses a name snatched in the meantime too.
		existing, err := s.board.Get(ctx, slug)
		if err != nil {
			return tool.TeammateResult{}, err
		}
		if existing.Handle != "" {
			return tool.TeammateResult{Notice: fmt.Sprintf("The handle %q is already on the board — pick another name, or wait for it to finish.", slug)}, nil
		}
		handle = slug
	} else {
		// Claim the handle before the child session exists, so the title built
		// from the handle is truthful: a name rolled here belongs to nobody
		// until this row claims it, and a roll that lands on a name a live row
		// already holds is rolled again rather than taken over.
		claimed, err := s.mintFreeHandle(ctx, board.Handle{
			Role:      "build",
			Kind:      board.KindTeammate,
			SpawnedBy: parentHandle.Handle,
			Status:    board.StatusRunning,
		}, boardHandleAttempts, func() string {
			return board.NewHandle("teammate")
		})
		if err != nil {
			return tool.TeammateResult{}, err
		}
		handle = claimed.Handle
	}

	childID := session.NewSessionID()
	child := &session.Session{
		ID:          childID,
		ProjectID:   parent.Directory,
		Directory:   parent.Directory,
		Title:       handle + " — " + truncateTitle(req.Prompt, 48),
		Model:       parent.Model,
		Provider:    parent.Provider,
		Effort:      parent.Effort,
		SessionType: "build",
		// Inherited, not reset: a teammate works in the same tree under the same
		// rules as the agent that spawned it.
		Permission: parent.Permission,
		CreatedAt:  session.Now(),
		UpdatedAt:  session.Now(),
	}
	if err := s.store.Create(child); err != nil {
		return tool.TeammateResult{}, err
	}
	s.bus.Publish("session.created", child)

	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: childID,
		Role:      session.RoleUser,
		Agent:     "build",
		CreatedAt: session.Now(),
	}
	if err := s.store.CreateMessage(userMsg); err != nil {
		return tool.TeammateResult{}, err
	}
	textData, err := json.Marshal(session.TextPartData{
		Text: teammatePrompt(handle, parentHandle.Handle, parent.Directory, req.Prompt),
	})
	if err != nil {
		return tool.TeammateResult{}, err
	}
	part := &session.Part{
		ID:        session.NewPartID(),
		MessageID: userMsg.ID,
		SessionID: childID,
		Type:      session.PartText,
		Data:      textData,
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := s.store.CreatePart(part); err != nil {
		return tool.TeammateResult{}, err
	}
	s.bus.Publish("message.updated", userMsg)

	// Registered before the loop starts, so the teammate's first board call
	// resolves its own handle instead of minting a second one, and so the tree
	// shows the edge while the child is still starting up.
	//
	// The random-name path claimed its row before the child was created, so
	// this re-registers the row it already holds and attaches the session id —
	// the upsert's rejoin work. The typed path claimed nothing, so this is the
	// insert, made fresh: a name snatched between the pre-check and here is
	// refused, never taken over.
	full := board.Handle{
		Handle:    handle,
		SessionID: string(childID),
		Role:      "build",
		Kind:      board.KindTeammate,
		SpawnedBy: parentHandle.Handle,
		Status:    board.StatusRunning,
	}
	var h board.Handle
	var regErr error
	if typedName {
		h, regErr = s.board.RegisterFresh(ctx, full)
	} else {
		h, regErr = s.board.Register(ctx, full)
	}
	if regErr != nil {
		if typedName && errors.Is(regErr, board.ErrHandleTaken) {
			return tool.TeammateResult{Notice: fmt.Sprintf("The handle %q is already on the board — pick another name, or wait for it to finish.", handle)}, nil
		}
		return tool.TeammateResult{}, regErr
	}
	s.publishBoardAgent(h)
	s.startSessionLoop(childID, "build", 0, 0, "")
	slog.Info("board: spawned teammate", "handle", handle, "session", childID, "parent", parentHandle.Handle)

	live++
	return tool.TeammateResult{
		Handle:    handle,
		SessionID: string(childID),
		URL:       "/session/" + string(childID),
		Notice:    fmt.Sprintf("Spawned %s (%d teammates running).", handle, live),
		Live:      live,
	}, nil
}

// teammatePrompt is the task a teammate starts from: its own task, plus what
// its toolset expects of it — it ends by reporting, it may spawn, and the
// questions it asks the user surface in the parent's composer.
func teammatePrompt(handle, parent, dir, prompt string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are teammate %s, a build agent working in %s alongside %s.\n\n", handle, dir, parent)
	fmt.Fprintf(&b, "You report back by finishing. Your last message is what %s receives as your result the moment your turn ends — make it the result: what you changed, what you verified, what is left. If you are blocked and need a decision from the user, ask with `ask_user`: your dialog mirrors into %s's composer tagged with your handle, and the run unblocks the moment they answer — you never have to end the turn first. Do not stop early with a plan and no work.\n", parent, parent)
	b.WriteString("You may spawn teammates of your own when your task splits into parts that can run in parallel on different files, and there is no limit on how many. Use `task` for research, which is cheaper and read-only. Split the work by file: two agents editing one file see a refusal and have to re-read.\n")
	b.WriteString("\nYour task:\n\n")
	b.WriteString(prompt)
	return b.String()
}

// spawnBackgroundTask is the task tool's Spawn hook: it starts one read-only
// sub-agent session on the board as a task child of the caller and returns at
// once, so the caller's turn is not parked behind the investigation. The
// child's answer is delivered through the completion-report pipeline
// (reportTeammateDone when the child's loop ends, catchUpTeammates at the
// parent's next turn start) and can be collected early with teammate wait.
// As with teammates only the build agent may spawn, and no ceiling is put on
// how many run at once.
func (s *Server) spawnBackgroundTask(cctx context.Context, sessionID, title, prompt string) (tool.BackgroundTaskResult, error) {
	if s.board == nil {
		return tool.BackgroundTaskResult{Notice: "Background tasks are not available in this environment (no agent board)."}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
	defer cancel()

	id := session.SessionID(sessionID)
	parent, err := s.store.Get(id)
	if err != nil {
		return tool.BackgroundTaskResult{}, err
	}
	if parent == nil {
		return tool.BackgroundTaskResult{}, fmt.Errorf("session %s not found", sessionID)
	}
	if parent.SessionType != "build" {
		return tool.BackgroundTaskResult{Notice: "Background tasks are only available to the build agent."}, nil
	}
	parentHandle, err := s.boardHandle(ctx, id, parent.SessionType)
	if err != nil {
		return tool.BackgroundTaskResult{}, err
	}

	_, live := s.liveTeammates(ctx)
	// Claim a free task handle before the child session exists — the prompt is
	// built from it — rolling past a name a live row already holds instead of
	// taking that row over.
	claimed, err := s.mintFreeHandle(ctx, board.Handle{
		Role:      "subagent",
		Kind:      board.KindTask,
		SpawnedBy: parentHandle.Handle,
		Status:    board.StatusRunning,
	}, boardHandleAttempts, func() string {
		return board.NewHandle("task")
	})
	if err != nil {
		return tool.BackgroundTaskResult{}, err
	}
	handle := claimed.Handle

	childID := session.NewSessionID()
	child := &session.Session{
		ID:          childID,
		ProjectID:   parent.Directory,
		Directory:   parent.Directory,
		Title:       "Task: " + truncateTitle(title, 60),
		Model:       parent.Model,
		Provider:    parent.Provider,
		Effort:      parent.Effort,
		SessionType: "subagent",
		// Inherited, not reset: the child reads the same tree under the same
		// rules. Its loop is ungated (startSessionLoop skips the flag for a
		// subagent), and its toolset is read-only, so the row is never consulted.
		Permission: parent.Permission,
		CreatedAt:  session.Now(),
		UpdatedAt:  session.Now(),
	}
	if err := s.store.Create(child); err != nil {
		return tool.BackgroundTaskResult{}, err
	}
	s.bus.Publish("session.created", child)

	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: childID,
		Role:      session.RoleUser,
		Agent:     "subagent",
		CreatedAt: session.Now(),
	}
	if err := s.store.CreateMessage(userMsg); err != nil {
		return tool.BackgroundTaskResult{}, err
	}
	textData, err := json.Marshal(session.TextPartData{
		Text: backgroundTaskPrompt(handle, parentHandle.Handle, parent.Directory, title, prompt),
	})
	if err != nil {
		return tool.BackgroundTaskResult{}, err
	}
	part := &session.Part{
		ID:        session.NewPartID(),
		MessageID: userMsg.ID,
		SessionID: childID,
		Type:      session.PartText,
		Data:      textData,
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := s.store.CreatePart(part); err != nil {
		return tool.BackgroundTaskResult{}, err
	}
	s.bus.Publish("message.updated", userMsg)

	// Registered before the loop starts, so the tree shows the edge while the
	// child is still starting up, and its loop end finds its own row. This is
	// the re-register of the row mintFreeHandle claimed above — it attaches
	// the session id and refreshes the stamps.
	h, err := s.board.Register(ctx, board.Handle{
		Handle:    handle,
		SessionID: string(childID),
		Role:      "subagent",
		Kind:      board.KindTask,
		SpawnedBy: parentHandle.Handle,
		Status:    board.StatusRunning,
	})
	if err != nil {
		return tool.BackgroundTaskResult{}, err
	}
	s.publishBoardAgent(h)
	s.startSessionLoop(childID, "subagent", 0, 0, "")
	slog.Info("board: spawned background task", "handle", handle, "session", childID, "parent", parentHandle.Handle)

	live++
	return tool.BackgroundTaskResult{
		Handle:    handle,
		SessionID: string(childID),
		URL:       "/session/" + string(childID),
		Notice:    fmt.Sprintf("Started background task %s (%d working).", handle, live),
		Live:      live,
	}, nil
}

// backgroundTaskPrompt is the task a background task child starts from: the
// same shape the synchronous sub-agent gets — one standalone prompt — plus the
// delivery contract. It reports by finishing: its last message is delivered to
// its caller as the result, so it should make that message the answer itself.
func backgroundTaskPrompt(handle, parent, dir, title, prompt string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are background task %s, a read-only investigation agent working in %s for %s.\n\n", handle, dir, parent)
	if title != "" {
		fmt.Fprintf(&b, "The task is titled: %s.\n\n", title)
	}
	b.WriteString("There is no mid-task channel: you cannot ask for more while you work, and nobody is watching your page for progress. Report by finishing — your last message is delivered to the caller as your result, so make it the answer: findings, evidence (files/lines/URLs) and what you could not verify. A status update instead of an answer is a wasted run.\n")
	b.WriteString("\nYour task:\n\n")
	b.WriteString(prompt)
	return b.String()
}

// waitTeammate blocks up to the timeout until the teammates it names are done,
// then reports each one's status and last message. A timeout is not a failure:
// they are still running, and the notice arrives when they finish.
func (s *Server) waitTeammate(ctx context.Context, h board.Handle, req tool.TeammateRequest) (tool.TeammateResult, error) {
	d := time.Duration(req.Timeout) * time.Second
	if d <= 0 {
		d = boardWaitDefaultSeconds * time.Second
	}
	if d > boardWaitMaxSeconds*time.Second {
		d = boardWaitMaxSeconds * time.Second
	}
	// Its own context: the tool call's own deadline is a fraction of the wait,
	// and a cancelled turn should still get a clean "still running" answer.
	wctx, cancel := context.WithTimeout(context.Background(), d+2*time.Second)
	defer cancel()

	watched := make([]string, 0, len(req.Handles))
	for _, name := range req.Handles {
		if name = strings.TrimSpace(name); name != "" {
			watched = append(watched, name)
		}
	}
	if len(watched) == 0 {
		children, err := s.board.Children(ctx, h.Handle)
		if err != nil {
			return tool.TeammateResult{}, err
		}
		for _, child := range children {
			if !child.Terminal() {
				watched = append(watched, child.Handle)
			}
		}
		if len(watched) == 0 {
			roster, live := s.teammatesOf(ctx, h.Handle)
			return tool.TeammateResult{Notice: "None of your teammates is still running.", Roster: roster, Live: live}, nil
		}
	}

	// Wait in short slices rather than blocking the whole timeout. A notice
	// queued into this turn's LoopControl can only be injected when this call
	// returns — nothing interrupts the current generation to make room for it —
	// so while a pending notice sits here, the model's next step sits behind
	// this tool call. Breaking out on HasPendingNotice() hands the call back to
	// the loop right away and lets the notice ride the next request, instead of
	// making that step wait out the rest of the timeout first.
	lc := agent.LoopControlFromContext(ctx)
	const waitSlice = 250 * time.Millisecond
	for waited := time.Duration(0); waited < d; waited += waitSlice {
		// The caller's context is what an abort, a superseding prompt or a
		// session delete cancels; a cancelled turn must not sit out the wait —
		// break to the partial answer below. DeadlineExceeded is deliberately
		// not a break: that is the tool call's own op bound, and the wait's own
		// context (wctx) is what carries the wait past it.
		if errors.Is(ctx.Err(), context.Canceled) {
			break
		}
		if lc != nil && lc.HasPendingNotice() {
			break
		}
		slice := waitSlice
		if rest := d - waited; rest < slice {
			slice = rest
		}
		found, err := s.board.WaitStatus(wctx, watched, slice)
		if err != nil {
			if wctx.Err() != nil {
				break
			}
			return tool.TeammateResult{}, err
		}
		if allTerminal(found) && len(found) > 0 {
			break
		}
	}

	// The wait can outlast the tool call's own deadline, so the reads after it
	// get a context of their own: with an expired one the roster comes back
	// empty and the caller reads that as "nobody is working".
	opCtx, opCancel := context.WithTimeout(context.Background(), boardOpTimeout)
	defer opCancel()

	roster, live := s.teammatesOf(opCtx, h.Handle)
	res := tool.TeammateResult{Roster: roster, Live: live}
	var running []string
	for _, name := range watched {
		row, err := s.board.Get(opCtx, name)
		if err != nil || row.Handle == "" {
			continue
		}
		if !row.Terminal() {
			running = append(running, row.Handle)
			continue
		}
		text := ""
		if row.SessionID != "" {
			text = s.lastAssistantText(session.SessionID(row.SessionID))
		}
		res.Reports = append(res.Reports, tool.TeammateReport{
			Handle:    row.Handle,
			Status:    row.Status,
			SessionID: row.SessionID,
			Text:      text,
		})
	}
	switch {
	case len(running) > 0:
		res.Notice = fmt.Sprintf("Still running after %s: %s. You will be told when they finish.", d.Round(time.Second), strings.Join(running, ", "))
	case len(res.Reports) > 0:
		res.Notice = "They are done."
	default:
		res.Notice = "Nobody named is on the board."
	}
	return res, nil
}

// allTerminal reports whether every handle in found has finished.
func allTerminal(found []board.Handle) bool {
	for _, h := range found {
		if !h.Terminal() {
			return false
		}
	}
	return true
}

// teammatesOf returns the teammates a handle spawned and how many are still
// working.
func (s *Server) teammatesOf(ctx context.Context, parent string) ([]board.Handle, int) {
	children, err := s.board.Children(ctx, parent)
	if err != nil {
		slog.Warn("board: could not read a session's teammates", "err", err)
		return nil, 0
	}
	live := 0
	for _, child := range children {
		if !child.Terminal() {
			live++
		}
	}
	return children, live
}

// stopTeammate cancels a teammate's loop, records it stopped, and reports the
// stop to its spawner.
func (s *Server) stopTeammate(ctx context.Context, handle string) (tool.TeammateResult, error) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return tool.TeammateResult{Notice: "handle is required to stop a teammate"}, nil
	}
	h, err := s.board.Get(ctx, handle)
	if err != nil {
		return tool.TeammateResult{}, err
	}
	if h.Handle == "" {
		return tool.TeammateResult{Notice: fmt.Sprintf("No handle %q is on this board.", handle)}, nil
	}
	if h.Kind != board.KindTeammate && h.Kind != board.KindTask {
		_, live := s.liveTeammates(ctx)
		return tool.TeammateResult{Notice: fmt.Sprintf("%s is a session, not a teammate — it is stopped from its own page.", handle), Live: live}, nil
	}
	// Claim the row before cancelling. cancelLoop does not wait for the loop to
	// exit, so the loop's own leaveBoard runs concurrently with this call: the
	// claim makes it a no-op instead of letting it write done and push a
	// "finished" report for a teammate that was stopped. A row that is already
	// terminal finished on its own, and stopping it would only re-report a
	// completion the spawner has already been told about.
	moved, err := s.board.SetStatusUnlessTerminal(ctx, h.Handle, board.StatusStopped)
	if err != nil {
		return tool.TeammateResult{}, err
	}
	if !moved {
		cur, gerr := s.board.Get(ctx, h.Handle)
		why := "it is no longer on the board"
		if gerr == nil && cur.Handle != "" {
			why = fmt.Sprintf("it already finished (%s)", cur.Status)
		}
		_, live := s.liveTeammates(ctx)
		return tool.TeammateResult{
			Handle:    h.Handle,
			SessionID: h.SessionID,
			Notice:    fmt.Sprintf("%s was not stopped — %s.", h.Handle, why),
			Live:      live,
		}, nil
	}
	if h.SessionID != "" {
		s.cancelLoop(session.SessionID(h.SessionID))
	}
	h.Status = board.StatusStopped
	s.publishBoardAgent(h)
	// The stop is itself the news a spawner is owed: it is told in the turn it
	// is already in when it has one — "was stopped", with the child's last
	// message — and the row is left unreported for its next turn start when it
	// is idle. The claim above keeps the child's own winding-down leaveBoard
	// from reporting the same stop a second time. stopTeammatesOf pushes
	// nothing: an abort or a delete of the parent is a teardown, and its rows
	// wait for the next catch-up.
	if h.SpawnedBy != "" && h.SessionID != "" {
		s.reportTeammateDone(ctx, h, s.lastAssistantText(session.SessionID(h.SessionID)))
	}
	_, live := s.liveTeammates(ctx)
	return tool.TeammateResult{
		Handle:    h.Handle,
		SessionID: h.SessionID,
		Notice:    fmt.Sprintf("Stopped %s.", h.Handle),
		Live:      live,
	}, nil
}

// statusTeammate reports one teammate's roster row and its last message.
func (s *Server) statusTeammate(ctx context.Context, h board.Handle, handle string) (tool.TeammateResult, error) {
	target := h
	if handle = strings.TrimSpace(handle); handle != "" {
		got, err := s.board.Get(ctx, handle)
		if err != nil {
			return tool.TeammateResult{}, err
		}
		if got.Handle == "" {
			return tool.TeammateResult{Notice: fmt.Sprintf("No handle %q is on this board.", handle)}, nil
		}
		target = got
	}

	res := tool.TeammateResult{Handle: target.Handle, SessionID: target.SessionID}
	text := ""
	if target.SessionID != "" {
		text = s.lastAssistantText(session.SessionID(target.SessionID))
	}
	res.Reports = []tool.TeammateReport{{
		Handle:    target.Handle,
		Status:    target.Status,
		SessionID: target.SessionID,
		Text:      text,
	}}
	res.Roster, res.Live = s.teammatesOf(ctx, h.Handle)
	res.Notice = fmt.Sprintf("%s is %s — session %s.", target.Handle, target.Status, target.SessionID)
	return res, nil
}

// liveTeammates returns every teammate or background task child still working
// in the project, which is what a caller needs to know before and after a
// spawn.
func (s *Server) liveTeammates(ctx context.Context) ([]board.Handle, int) {
	roster, err := s.board.Roster(ctx)
	if err != nil {
		slog.Warn("board: could not read roster", "err", err)
		return nil, 0
	}
	var live []board.Handle
	for _, h := range roster {
		if h.Kind != board.KindTeammate && h.Kind != board.KindTask {
			continue
		}
		if !h.Terminal() {
			live = append(live, h)
		}
	}
	return live, len(live)
}

// spawnedTeammates returns the teammate and background-task handles this
// session spawned, in board join order. A server with no board, or a session
// that never joined one, has none.
func (s *Server) spawnedTeammates(ctx context.Context, id session.SessionID) ([]board.Handle, error) {
	if s.board == nil {
		return nil, nil
	}
	h, err := s.board.BySession(ctx, string(id))
	if err != nil {
		return nil, err
	}
	if h.Handle == "" {
		return nil, nil
	}
	roster, err := s.board.Roster(ctx)
	if err != nil {
		return nil, err
	}
	var out []board.Handle
	for _, child := range roster {
		if (child.Kind != board.KindTeammate && child.Kind != board.KindTask) || child.SpawnedBy != h.Handle || child.SessionID == "" {
			continue
		}
		out = append(out, child)
	}
	return out, nil
}

// parentSessionOf is the LoopRunner's view of the agent tree for permission
// prompts: a teammate's request is surfaced in the user-facing session that
// spawned it, not only on the teammate's own page. It returns that session and
// the teammate's handle, climbing through any nesting to the root session — the
// one a human actually watches. A session that is not a teammate has neither.
func (s *Server) parentSessionOf(id session.SessionID) (session.SessionID, string) {
	if s.board == nil {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
	defer cancel()
	h, err := s.board.BySession(ctx, string(id))
	if err != nil || h.Kind != board.KindTeammate {
		return "", ""
	}
	teammate := h.Handle
	roster, err := s.board.Roster(ctx)
	if err != nil {
		return "", ""
	}
	byHandle := make(map[string]board.Handle, len(roster))
	for _, a := range roster {
		byHandle[a.Handle] = a
	}
	// The depth cap bounds a cyclic roster; a real tree is far shallower.
	for depth := 0; depth < 32 && h.SpawnedBy != ""; depth++ {
		up, ok := byHandle[h.SpawnedBy]
		if !ok {
			return "", ""
		}
		if up.Kind != board.KindTeammate && up.SessionID != "" {
			return session.SessionID(up.SessionID), teammate
		}
		h = up
	}
	return "", ""
}

// stopTeammatesOf stops every live teammate a session spawned. It is called
// when a session is aborted or deleted: a teammate is a loop spending money on
// a task whose coordinator is gone, so it does not outlive a deliberate stop.
func (s *Server) stopTeammatesOf(id session.SessionID) {
	if s.board == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
	defer cancel()

	h, err := s.board.BySession(ctx, string(id))
	if err != nil || h.Handle == "" {
		return
	}
	roster, err := s.board.Roster(ctx)
	if err != nil {
		slog.Warn("board: could not read roster to stop teammates", "err", err)
		return
	}
	for _, child := range roster {
		if (child.Kind != board.KindTeammate && child.Kind != board.KindTask) || child.SpawnedBy != h.Handle {
			continue
		}
		if child.Terminal() {
			continue
		}
		// Claimed before the cancel, for the reason stopTeammate gives: the
		// child's own loop end races this and must find the row terminal.
		moved, err := s.board.SetStatusUnlessTerminal(ctx, child.Handle, board.StatusStopped)
		if err != nil {
			slog.Warn("board: could not stop teammate", "handle", child.Handle, "err", err)
			continue
		}
		if !moved {
			continue
		}
		if child.SessionID != "" {
			s.cancelLoop(session.SessionID(child.SessionID))
		}
		child.Status = board.StatusStopped
		s.publishBoardAgent(child)
		slog.Info("board: stopped teammate with its parent", "handle", child.Handle, "parent", h.Handle)
	}
}

// stepBudgetFor is the LoopRunner's per-session override. A teammate runs with
// no step ceiling — it ends when its task is done, not at a count — and a
// background task child runs what its synchronous sibling runs (RunLoop maps 0
// → DefaultMaxSteps, auto-resume off) instead of the runner's own turn budget;
// every other session leaves the runner's own budget in place.
func (s *Server) stepBudgetFor(id session.SessionID) (int, int) {
	if s.board == nil {
		return 0, 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h, err := s.board.BySession(ctx, string(id))
	if err != nil {
		return 0, 0
	}
	switch h.Kind {
	case board.KindTeammate:
		return agent.MaxStepsUncapped, 0
	case board.KindTask:
		return agent.DefaultMaxSteps, 0
	}
	return 0, 0
}

// reconcileBoard repairs the roster after a restart: a handle still marked
// running has no loop to be running in, because the process that owned it is
// gone. A session is idle again; a teammate is stale.
func (s *Server) reconcileBoard() {
	if s.board == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), boardOpTimeout)
	defer cancel()

	sessions, err := s.store.ListAll()
	if err != nil {
		slog.Warn("board: could not list sessions to reconcile", "err", err)
		return
	}
	live := make(map[string]bool, len(sessions))
	for _, sess := range sessions {
		live[string(sess.ID)] = true
	}
	if err := s.board.Reconcile(ctx, live); err != nil {
		slog.Warn("board: reconcile failed", "err", err)
	}
	roster, err := s.board.Roster(ctx)
	if err != nil {
		slog.Warn("board: could not read roster to reconcile", "err", err)
		return
	}
	for _, h := range roster {
		if h.Status != board.StatusRunning {
			continue
		}
		if s.sessionRunning(session.SessionID(h.SessionID)) {
			continue
		}
		next := board.StatusIdle
		if h.Kind == board.KindTeammate || h.Kind == board.KindTask {
			next = board.StatusStale
		}
		if err := s.board.SetStatus(ctx, h.Handle, next); err != nil {
			slog.Warn("board: could not reconcile handle", "handle", h.Handle, "err", err)
			continue
		}
		h.Status = next
		s.publishBoardAgent(h)
		slog.Info("board: reconciled a handle left running", "handle", h.Handle, "status", next)
	}
}

// sessionRunning reports whether a loop is registered for the session.
func (s *Server) sessionRunning(id session.SessionID) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[id]
	return ok
}

// loopControl is the LoopControl for a session's running loop, or nil when no
// loop is running (a finished turn, a session that never started).
func (s *Server) loopControl(id session.SessionID) *agent.LoopControl {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loopControls[id]
}

func (s *Server) publishBoardAgent(h board.Handle) {
	s.bus.Publish("board.agent", map[string]any{
		"handle":    h.Handle,
		"status":    h.Status,
		"sessionId": h.SessionID,
		"role":      h.Role,
		"kind":      h.Kind,
		"spawnedBy": h.SpawnedBy,
	})
}
