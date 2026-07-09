package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/git"
	"github.com/prasenjeet-symon/ogcode/internal/id"
	"github.com/prasenjeet-symon/ogcode/internal/plan"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/task"
)

func (s *Server) handleListPlans(w http.ResponseWriter, r *http.Request) {
	directory := r.URL.Query().Get("directory")
	if directory == "" {
		directory = s.dir
	}

	plans, err := s.planStore.List(directory)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if plans == nil {
		plans = []*plan.Plan{}
	}

	// Compute derived (non-persisted) fields for each plan.
	for _, p := range plans {
		s.computePlanDerived(p)
	}

	writeJSON(w, http.StatusOK, plans)
}

// computePlanDerived fills in the derived (non-persisted) fields on a plan so
// that every endpoint returning a plan reports them consistently:
//   - Archived: whether the plan has been archived.
//   - AllTasksCompleted: true once the plan is locked and every created task is
//     completed. A locked plan whose breakdown finished but produced no tasks is
//     treated as vacuously complete so it is not stuck "active" forever; a plan
//     whose breakdown failed or is still in progress is never marked complete.
func (s *Server) computePlanDerived(p *plan.Plan) {
	p.Archived = p.ArchivedAt > 0

	if p.Status != plan.StatusLocked {
		return
	}
	tasks, err := s.taskStore.ListByPlan(p.ID)
	if err != nil {
		return
	}
	if len(tasks) > 0 {
		allDone := true
		for _, t := range tasks {
			if t.Status != task.StatusCompleted {
				allDone = false
				break
			}
		}
		p.AllTasksCompleted = allDone
		return
	}
	// No tasks: only "complete" if the breakdown finished successfully (so a
	// failed/in-progress breakdown isn't silently marked done).
	if p.BreakdownStatus == plan.BreakdownCompleted {
		p.AllTasksCompleted = true
	}
}

func (s *Server) handleCreatePlan(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Directory string `json:"directory"`
		Title     string `json:"title,omitempty"`
		Model     string `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	dir := input.Directory
	if dir == "" {
		dir = s.dir
	}

	// Create a plan-type session for message reuse
	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   dir,
		Directory:   dir,
		Title:       "Plan: " + input.Title,
		Model:       input.Model,
		SessionType: "plan",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := s.store.Create(sess); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	planID := string(id.NewPlanID())
	now := plan.Now()
	p := &plan.Plan{
		ID:        planID,
		SessionID: string(sess.ID),
		ProjectID: dir,
		Directory: dir,
		Title:     input.Title,
		Status:    plan.StatusOpen,
		Model:     input.Model,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.planStore.Create(p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.bus.Publish("plan.created", p)
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planID")
	p, err := s.planStore.Get(planID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if p == nil {
		http.Error(w, "plan not found", http.StatusNotFound)
		return
	}
	s.computePlanDerived(p)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planID")
	p, err := s.planStore.Get(planID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if p == nil {
		http.Error(w, "plan not found", http.StatusNotFound)
		return
	}

	var update struct {
		Title *string `json:"title"`
		Model *string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if update.Title != nil {
		p.Title = *update.Title
	}
	if update.Model != nil {
		p.Model = *update.Model
	}
	p.UpdatedAt = plan.Now()

	if err := s.planStore.Update(p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.bus.Publish("plan.updated", p)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeletePlan(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planID")

	// Clean up any task worktrees before deleting the plan.
	// The DB cascade delete handles task/session rows, but worktree directories
	// and git branches must be cleaned up explicitly.
	tasks, _ := s.taskStore.ListByPlan(planID)
	for _, t := range tasks {
		if t.WorktreePath != "" {
			go func(branch string) {
				if err := git.RemoveTaskWorktree(s.dir, branch); err != nil {
					slog.Warn("delete plan: remove worktree", "plan", planID, "branch", branch, "err", err)
				}
			}(t.BranchName)
		}
	}

	if err := s.planStore.Delete(planID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.bus.Publish("plan.deleted", map[string]string{"id": planID})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLockPlan(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planID")

	p, err := s.planStore.Get(planID)
	if err != nil || p == nil {
		http.Error(w, "plan not found", http.StatusNotFound)
		return
	}
	if p.Status == plan.StatusLocked {
		http.Error(w, "plan is already locked", http.StatusBadRequest)
		return
	}

	// Capture the repo's active branch now — task PRs derived from this plan will
	// target it, so the work merges back into wherever the user is working rather
	// than always the default branch. A detached HEAD yields no usable branch name.
	base := git.GetCurrentBranch(s.dir)
	if base == "HEAD" {
		base = ""
	}
	p.BaseBranch = base
	p.UpdatedAt = plan.Now()
	if err := s.planStore.Update(p); err != nil {
		slog.Warn("persist plan base branch at lock", "plan", p.ID, "err", err)
	}

	// Cancel any in-flight interactive loop before taking over the session.
	s.cancelPlanLoop(p)

	// Ask the LLM to produce a final comprehensive plan document.
	// This must succeed — the breakdown agent relies on it as its sole input.
	// Return an error so the UI can surface it and let the user retry locking.
	if err := s.generateFinalPlanSummary(p); err != nil {
		slog.Error("final plan summary failed, aborting lock", "plan", p.ID, "err", err)
		http.Error(w, "failed to generate final plan summary — please try locking again", http.StatusInternalServerError)
		return
	}

	if err := s.planStore.Lock(planID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p, _ = s.planStore.Get(planID)

	if p != nil {
		go s.runBreakdown(p)
	}

	s.bus.Publish("plan.locked", p)
	writeJSON(w, http.StatusOK, p)
}

const finalPlanPrompt = "The planning phase is now complete. " +
	"Please write a comprehensive final plan document that captures everything discussed: " +
	"goals, requirements, technical approach, key decisions, constraints, and any implementation details. " +
	"This document will be the single reference used during implementation — make it thorough and self-contained."

// generateFinalPlanSummary injects a finalization prompt into the plan session and
// runs the agent loop synchronously (3-minute timeout) so the LLM's response is
// persisted as the last message before the plan is locked.
func (s *Server) generateFinalPlanSummary(p *plan.Plan) error {
	sessionID := session.SessionID(p.SessionID)

	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: sessionID,
		Role:      session.RoleUser,
		Agent:     "plan",
		CreatedAt: session.Now(),
	}
	if err := s.store.CreateMessage(userMsg); err != nil {
		return fmt.Errorf("create finalization message: %w", err)
	}

	textData, _ := json.Marshal(session.TextPartData{Text: finalPlanPrompt})
	userPart := &session.Part{
		ID:        session.NewPartID(),
		MessageID: userMsg.ID,
		SessionID: sessionID,
		Type:      session.PartText,
		Data:      textData,
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := s.store.CreatePart(userPart); err != nil {
		return fmt.Errorf("create finalization message part: %w", err)
	}

	s.bus.Publish("message.updated", userMsg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Register the finalization loop in s.running so it is (a) cancellable via
	// abort and (b) visible to handlePlanPrompt — otherwise a message sent while
	// the (still-unlocked) plan is finalizing would start a second concurrent
	// loop on the same session and interleave writes.
	s.mu.Lock()
	if old, ok := s.running[sessionID]; ok {
		old()
	}
	s.nextToken++
	token := s.nextToken
	s.running[sessionID] = cancel
	s.runningToken[sessionID] = token
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.runningToken[sessionID] == token {
			delete(s.running, sessionID)
			delete(s.runningToken, sessionID)
		}
		s.mu.Unlock()
	}()

	return s.loopRunner.RunLoop(ctx, sessionID, "plan", 0, 0)
}

func (s *Server) handleAbortPlan(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planID")
	p, err := s.planStore.Get(planID)
	if err != nil || p == nil {
		http.Error(w, "plan not found", http.StatusNotFound)
		return
	}

	s.cancelPlanLoop(p)
	w.WriteHeader(http.StatusNoContent)
}

// cancelPlanLoop cancels any running agent loop for the plan's session
// and marks unfinished assistant messages and in-progress tool calls as aborted.
func (s *Server) cancelPlanLoop(p *plan.Plan) {
	sessionID := session.SessionID(p.SessionID)

	// Cancel the running loop
	s.mu.Lock()
	cancel, ok := s.running[sessionID]
	if ok {
		delete(s.running, sessionID)
		delete(s.runningToken, sessionID)
	}
	s.mu.Unlock()

	if ok {
		cancel()
		slog.Info("aborted plan loop", "plan", p.ID, "session", sessionID)
	}

	// Mark unfinished assistant messages as aborted and cancel in-progress tool calls
	messages, err := s.store.GetMessages(sessionID, "", 100)
	if err != nil {
		return
	}
	abortedReason := "aborted"

	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]

		// Mark first unfinished assistant message as aborted
		if m.Info.Role == session.RoleAssistant && m.Info.Finish == nil && m.Info.Error == nil {
			m.Info.Finish = &abortedReason
			if err := s.store.UpdateMessage(&m.Info); err != nil {
				slog.Error("update aborted message", "err", err)
			}
			slog.Info("marked plan message as aborted", "session", sessionID, "message", m.Info.ID)
			s.bus.Publish("message.updated", &m.Info)
		}

		// Cancel all in-progress tool calls
		if len(m.Parts) > 0 {
			for _, part := range m.Parts {
				if part.Type == session.PartTool {
					var toolData session.ToolPartData
					if err := json.Unmarshal(part.Data, &toolData); err == nil {
						if toolData.State.Status == session.ToolPending || toolData.State.Status == session.ToolRunning {
							cancelledErr := "Request cancelled by user"
							toolData.State.Status = session.ToolError
							toolData.State.Error = &cancelledErr
							toolData.State.Time.End = session.Now()

							updatedData, _ := json.Marshal(toolData)
							part.Data = updatedData
							part.UpdatedAt = session.Now()

							if err := s.store.UpdatePart(&part); err != nil {
								slog.Error("update cancelled tool part", "err", err)
							}
							slog.Info("cancelled tool call", "session", sessionID, "tool", toolData.Tool, "callId", toolData.CallID)
							s.bus.Publish("message.part.updated", map[string]string{
								"sessionId": string(sessionID),
								"partId":    string(part.ID),
							})
						}
					}
				}
			}
		}
	}
}

// runBreakdown executes the task breakdown flow for a locked plan.
// It loads the plan conversation, sends it to the breakdown agent, parses the result,
// creates task records, and creates git branches.
func (s *Server) runBreakdown(p *plan.Plan) {
	// Update plan breakdown status to in_progress
	p.BreakdownStatus = plan.BreakdownInProgress
	p.UpdatedAt = plan.Now()
	if err := s.planStore.Update(p); err != nil {
		slog.Error("update plan breakdown status", "plan", p.ID, "err", err)
	}
	s.bus.Publish("plan.breakdown.started", map[string]string{"planId": p.ID})

	// Load the plan conversation
	messages, err := s.store.GetMessages(session.SessionID(p.SessionID), "", 1000)
	if err != nil {
		slog.Error("load plan messages for breakdown", "plan", p.ID, "err", err)
		s.failBreakdown(p, "failed to load plan messages")
		return
	}

	// Pass paths of previous plan archives so the breakdown agent can read them if needed.
	archivePaths := s.planArchivePaths(p.Directory, p.ID)

	// Construct the breakdown prompt
	promptText := agent.BreakdownPrompt(messages, archivePaths)

	// Create a breakdown session
	breakdownSession := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   p.ProjectID,
		Directory:   p.Directory,
		Title:       "Breakdown: " + p.Title,
		Model:       p.Model,
		SessionType: "build",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := s.store.Create(breakdownSession); err != nil {
		slog.Error("create breakdown session", "plan", p.ID, "err", err)
		s.failBreakdown(p, "failed to create breakdown session")
		return
	}

	// Create user message with the breakdown prompt
	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: breakdownSession.ID,
		Role:      session.RoleUser,
		Agent:     "breakdown",
		CreatedAt: session.Now(),
	}
	if err := s.store.CreateMessage(userMsg); err != nil {
		slog.Error("create breakdown message", "plan", p.ID, "err", err)
		s.failBreakdown(p, "failed to create breakdown message")
		return
	}

	textData, _ := json.Marshal(session.TextPartData{Text: promptText})
	userPart := &session.Part{
		ID:        session.NewPartID(),
		MessageID: userMsg.ID,
		SessionID: breakdownSession.ID,
		Type:      session.PartText,
		Data:      textData,
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := s.store.CreatePart(userPart); err != nil {
		slog.Error("create breakdown part", "plan", p.ID, "err", err)
		s.failBreakdown(p, "failed to create breakdown part")
		return
	}

	s.bus.Publish("message.updated", userMsg)

	// Run the breakdown agent loop with a hard timeout so a stuck agent cannot
	// leave the plan permanently in "in_progress" state.
	const breakdownTimeout = 10 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), breakdownTimeout)
	defer cancel()

	slog.Info("starting breakdown agent loop", "plan", p.ID, "session", breakdownSession.ID)
	if err := s.loopRunner.RunLoop(ctx, breakdownSession.ID, "breakdown", 0, 0); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			slog.Error("breakdown agent loop timed out", "plan", p.ID, "timeout", breakdownTimeout)
			s.failBreakdown(p, fmt.Sprintf("breakdown timed out after %s — try locking the plan again", breakdownTimeout))
		} else {
			slog.Error("breakdown agent loop error", "plan", p.ID, "err", err)
			s.failBreakdown(p, "breakdown agent loop failed")
		}
		return
	}

	// Load the breakdown session messages and find the assistant's response
	breakdownMessages, err := s.store.GetMessages(breakdownSession.ID, "", 100)
	if err != nil {
		slog.Error("load breakdown messages", "plan", p.ID, "err", err)
		s.failBreakdown(p, "failed to load breakdown result")
		return
	}

	// Try to parse tasks from the submit_task_breakdown tool call first.
	// Tool-calling guarantees structurally valid JSON from the provider.
	var taskDefs []agent.TaskDefinition
	var toolInput struct {
		Tasks []agent.TaskDefinition `json:"tasks"`
	}
	for i := len(breakdownMessages) - 1; i >= 0 && len(taskDefs) == 0; i-- {
		for _, part := range breakdownMessages[i].Parts {
			if part.Type != session.PartTool {
				continue
			}
			var td session.ToolPartData
			if err := json.Unmarshal(part.Data, &td); err != nil || td.Tool != "submit_task_breakdown" {
				continue
			}
			if err := json.Unmarshal(td.State.Input, &toolInput); err == nil && len(toolInput.Tasks) > 0 {
				taskDefs = toolInput.Tasks
				break
			}
		}
	}

	if len(taskDefs) == 0 {
		// Fallback: parse free-text JSON from the assistant's response
		var responseText string
		for i := len(breakdownMessages) - 1; i >= 0; i-- {
			if breakdownMessages[i].Info.Role == session.RoleAssistant {
				for _, part := range breakdownMessages[i].Parts {
					if part.Type == session.PartText {
						var data session.TextPartData
						if err := json.Unmarshal(part.Data, &data); err == nil {
							responseText = data.Text
							break
						}
					}
				}
				if responseText != "" {
					break
				}
			}
		}
		if responseText == "" {
			slog.Error("no breakdown response found", "plan", p.ID)
			s.failBreakdown(p, "breakdown agent produced no response")
			return
		}
		var parseErr error
		taskDefs, parseErr = agent.ParseTasks(responseText)
		if parseErr != nil {
			slog.Error("parse breakdown tasks", "plan", p.ID, "err", parseErr)
			s.failBreakdown(p, "failed to parse task breakdown")
			return
		}
	}

	slog.Info("parsed breakdown tasks", "plan", p.ID, "count", len(taskDefs))

	// Normalize order indices. The breakdown array is already in intended execution
	// order, so fill in any left-as-zero index (the tool-call path does not default
	// these, unlike the free-text fallback) to keep the board ordering stable.
	for i := range taskDefs {
		if taskDefs[i].OrderIndex == 0 && i > 0 {
			taskDefs[i].OrderIndex = i
		}
	}

	// Detect circular dependencies before creating any task records.
	if breakdownHasCycle(taskDefs) {
		slog.Error("circular dependency detected in breakdown", "plan", p.ID)
		s.failBreakdown(p, "circular dependency detected in task breakdown — the AI-generated task graph contains a cycle and cannot be executed")
		return
	}

	// Create task records.
	// Pre-allocate all task IDs so dependency index resolution uses stable IDs
	// even when a task creation fails (which would shift slice indices).
	taskIDs := make([]string, len(taskDefs))
	for i := range taskDefs {
		taskIDs[i] = string(id.NewTaskID())
	}

	var createdTasks []*task.Task
	var failures int
	now := task.Now()
	for i, td := range taskDefs {
		// Resolve dependency indices to pre-allocated task IDs
		var deps []string
		for _, depIdx := range td.Dependencies {
			if depIdx >= 0 && depIdx < len(taskIDs) {
				deps = append(deps, taskIDs[depIdx])
			} else {
				slog.Warn("breakdown: dependency index out of range, skipping",
					"plan", p.ID, "task", td.Title, "depIdx", depIdx, "totalTasks", len(taskIDs))
			}
		}
		if deps == nil {
			deps = []string{}
		}

		effort := td.Effort
		if effort == "" {
			effort = task.EffortM
		}
		complexity := td.Complexity
		if complexity == "" {
			complexity = task.ComplexityMedium
		}

		newTask := &task.Task{
			ID:           taskIDs[i],
			PlanID:       p.ID,
			Title:        td.Title,
			Description:  td.Description,
			Effort:       effort,
			Complexity:   complexity,
			Status:       task.StatusPending,
			Dependencies: deps,
			OrderIndex:   td.OrderIndex,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := s.taskStore.Create(newTask); err != nil {
			slog.Error("create task", "plan", p.ID, "task", td.Title, "err", err)
			failures++
			continue
		}

		createdTasks = append(createdTasks, newTask)
	}

	// Assign shared chain branches to tasks that form dependency chains.
	// Tasks in the same linear chain share one branch; parallel tasks get none.
	assignChainBranches(createdTasks, p.ID)
	for _, t := range createdTasks {
		if t.ChainBranch != "" {
			if err := s.taskStore.Update(t); err != nil {
				slog.Warn("save chain branch", "task", t.ID, "err", err)
			}
		}
	}

	// Update plan breakdown status
	if failures > 0 && len(createdTasks) == 0 {
		// All tasks failed to create — treat as full failure
		s.failBreakdown(p, fmt.Sprintf("%d task(s) failed to create", failures))
		return
	}

	warningMsg := ""
	if failures > 0 {
		warningMsg = fmt.Sprintf("%d of %d task(s) failed to create and were skipped", failures, len(taskDefs))
		slog.Warn("breakdown completed with partial failures", "plan", p.ID, "created", len(createdTasks), "failures", failures)
	}

	p.BreakdownStatus = plan.BreakdownCompleted
	p.BreakdownWarnings = warningMsg
	p.UpdatedAt = plan.Now()
	if err := s.planStore.Update(p); err != nil {
		slog.Error("update plan breakdown completed", "plan", p.ID, "err", err)
	}

	eventProps := map[string]any{
		"planId":   p.ID,
		"count":    len(createdTasks),
		"warnings": warningMsg,
	}
	s.bus.Publish("plan.breakdown.completed", eventProps)
	slog.Info("breakdown completed", "plan", p.ID, "tasks", len(createdTasks), "failures", failures)
}

// breakdownHasCycle returns true if the dependency graph described by taskDefs contains a cycle.
// Dependencies are expressed as 0-based indices into the taskDefs slice.
func breakdownHasCycle(taskDefs []agent.TaskDefinition) bool {
	n := len(taskDefs)
	// 0 = unvisited, 1 = in current DFS stack, 2 = fully processed
	state := make([]int, n)
	var dfs func(i int) bool
	dfs = func(i int) bool {
		state[i] = 1
		for _, dep := range taskDefs[i].Dependencies {
			if dep < 0 || dep >= n {
				continue
			}
			if state[dep] == 1 {
				return true // back-edge → cycle
			}
			if state[dep] == 0 && dfs(dep) {
				return true
			}
		}
		state[i] = 2
		return false
	}
	for i := range taskDefs {
		if state[i] == 0 && dfs(i) {
			return true
		}
	}
	return false
}

// assignChainBranches stamps a shared ChainBranch on every task that is part of
// a dependency chain. Tasks with no dependencies and no dependents remain
// standalone and get no ChainBranch — they raise their own PRs as before.
//
// All tasks in one linear chain share a single branch named after the chain
// root (the task with no dependency). The assignment is independent of the order
// in which tasks appear in the slice: each member resolves its root by walking
// up the single-parent links, so a dependency that points to a later slice index
// cannot split one chain across two branches.
func assignChainBranches(tasks []*task.Task, planID string) {
	byID := make(map[string]*task.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}

	// hasDependent[id] is true when some task depends on id.
	hasDependent := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		for _, dep := range t.Dependencies {
			hasDependent[dep] = true
		}
	}

	// chainRoot walks up Dependencies[0] to the task with no dependency. It is
	// cycle-guarded defensively, though cycles are rejected before this point.
	chainRoot := func(t *task.Task) *task.Task {
		seen := make(map[string]bool)
		for len(t.Dependencies) > 0 {
			if seen[t.ID] {
				break
			}
			seen[t.ID] = true
			parent := byID[t.Dependencies[0]]
			if parent == nil {
				break
			}
			t = parent
		}
		return t
	}

	for _, t := range tasks {
		// A task is part of a chain if it has a dependency or a dependent.
		if len(t.Dependencies) == 0 && !hasDependent[t.ID] {
			continue
		}
		root := chainRoot(t)
		if root.ChainBranch == "" {
			slug := git.Slugify(root.Title)
			root.ChainBranch = fmt.Sprintf("chain/%s-%s", planID[:8], slug)
		}
		t.ChainBranch = root.ChainBranch
	}
}

// failBreakdown sets the plan's breakdown status to failed and publishes an event.
func (s *Server) failBreakdown(p *plan.Plan, reason string) {
	p.BreakdownStatus = plan.BreakdownFailed
	p.BreakdownWarnings = ""
	p.UpdatedAt = plan.Now()
	if err := s.planStore.Update(p); err != nil {
		slog.Error("update plan breakdown failed status", "plan", p.ID, "err", err)
	}
	s.bus.Publish("plan.breakdown.failed", map[string]string{"planId": p.ID, "reason": reason})
	slog.Error("breakdown failed", "plan", p.ID, "reason", reason)
}

func (s *Server) handlePlanPrompt(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planID")
	p, err := s.planStore.Get(planID)
	if err != nil || p == nil {
		http.Error(w, "plan not found", http.StatusNotFound)
		return
	}
	if p.Status == plan.StatusLocked {
		http.Error(w, "plan is locked", http.StatusBadRequest)
		return
	}

	var input struct {
		Content        string `json:"content"`
		Model          string `json:"model,omitempty"`
		ViewportWidth  int    `json:"viewportWidth,omitempty"`
		ViewportHeight int    `json:"viewportHeight,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	sessionID := session.SessionID(p.SessionID)

	// Update session and plan model if provided and different
	if input.Model != "" {
		slog.Info("updating plan model", "plan", p.ID, "newModel", input.Model)
		sess, err := s.store.Get(sessionID)
		if err == nil && sess != nil && sess.Model != input.Model {
			slog.Info("updating session model", "session", sessionID, "oldModel", sess.Model, "newModel", input.Model)
			sess.Model = input.Model
			sess.UpdatedAt = session.Now()
			if err := s.store.Update(sess); err != nil {
				slog.Error("update plan session model", "err", err)
			}
		}
		// Also update the plan's model to keep them in sync
		if p.Model != input.Model {
			slog.Info("updating plan object model", "plan", p.ID, "oldModel", p.Model, "newModel", input.Model)
			p.Model = input.Model
			p.UpdatedAt = plan.Now()
			if err := s.planStore.Update(p); err != nil {
				slog.Error("update plan model", "err", err)
			}
		}
	}

	// Create user message on the plan's session
	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: sessionID,
		Role:      session.RoleUser,
		Agent:     "plan",
		CreatedAt: session.Now(),
	}
	if err := s.store.CreateMessage(userMsg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	textData, _ := json.Marshal(session.TextPartData{Text: input.Content})
	userPart := &session.Part{
		ID:        session.NewPartID(),
		MessageID: userMsg.ID,
		SessionID: sessionID,
		Type:      session.PartText,
		Data:      textData,
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := s.store.CreatePart(userPart); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.bus.Publish("message.updated", userMsg)

	// Auto-generate a plan title from the first user message
	slog.Info("plan prompt: title check", "plan", p.ID, "title", p.Title)
	if p.Title == "" || p.Title == "Untitled" {
		go s.autoNamePlan(p, input.Content)
	} else {
		slog.Info("plan prompt: skipping auto-name, title already set", "plan", p.ID, "title", p.Title)
	}

	// Mark any unfinished assistant messages as aborted
	if orphans, err := s.store.GetMessages(sessionID, "", 100); err == nil {
		abortedReason := "aborted"
		for _, m := range orphans {
			if m.Info.Role == session.RoleAssistant && m.Info.Finish == nil && m.Info.Error == nil {
				m.Info.Finish = &abortedReason
				if updateErr := s.store.UpdateMessage(&m.Info); updateErr == nil {
					s.bus.Publish("message.updated", &m.Info)
				}
			}
		}
	}

	// Start agent loop with PlanAgent
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if old, ok := s.running[sessionID]; ok {
		old()
		slog.Info("cancelled previous running loop", "session", sessionID)
	}
	s.nextToken++
	token := s.nextToken
	s.running[sessionID] = cancel
	s.runningToken[sessionID] = token
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			if s.runningToken[sessionID] == token {
				delete(s.running, sessionID)
				delete(s.runningToken, sessionID)
			}
			s.mu.Unlock()
		}()
		if err := s.loopRunner.RunLoop(ctx, sessionID, "plan", input.ViewportWidth, input.ViewportHeight); err != nil {
			slog.Error("plan agent loop error", "plan", planID, "err", err)
		}
	}()

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetPlanMessages(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planID")
	p, err := s.planStore.Get(planID)
	if err != nil || p == nil {
		http.Error(w, "plan not found", http.StatusNotFound)
		return
	}

	sessionID := session.SessionID(p.SessionID)
	before := session.MessageID(r.URL.Query().Get("before"))
	limit := 300

	messages, err := s.store.GetMessages(sessionID, before, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if messages == nil {
		messages = []*session.MessageWithParts{}
	}
	writeJSON(w, http.StatusOK, messages)
}

// generatePlanMarkdown renders a plan archive: title, dates, the final plan
// summary, and a rich task list with branch, PR, and description per task.
func (s *Server) generatePlanMarkdown(p *plan.Plan) (string, error) {
	messages, err := s.store.GetMessages(session.SessionID(p.SessionID), "", 1000)
	if err != nil {
		return "", fmt.Errorf("get messages: %w", err)
	}
	tasks, err := s.taskStore.ListByPlan(p.ID)
	if err != nil {
		return "", fmt.Errorf("list tasks: %w", err)
	}

	var md strings.Builder

	// Header
	md.WriteString(fmt.Sprintf("# %s\n\n", p.Title))
	md.WriteString(fmt.Sprintf("**Created**: %s", time.UnixMilli(p.CreatedAt).Format("2006-01-02 15:04")))

	// Use the latest task UpdatedAt as the completion timestamp.
	var completedAt int64
	for _, t := range tasks {
		if t.UpdatedAt > completedAt {
			completedAt = t.UpdatedAt
		}
	}
	if completedAt > 0 {
		md.WriteString(fmt.Sprintf("  \n**Completed**: %s", time.UnixMilli(completedAt).Format("2006-01-02 15:04")))
	}
	md.WriteString("\n\n")

	// Plan summary — the last assistant message written at lock time.
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Info.Role != session.RoleAssistant {
			continue
		}
		for _, part := range msg.Parts {
			if part.Type != session.PartText {
				continue
			}
			var data session.TextPartData
			if err := json.Unmarshal(part.Data, &data); err == nil && data.Text != "" {
				md.WriteString("## Plan Summary\n\n")
				md.WriteString(data.Text)
				md.WriteString("\n\n")
			}
		}
		break
	}

	// Task outcomes
	if len(tasks) > 0 {
		md.WriteString("## Tasks\n\n")
		statusIcons := map[string]string{
			task.StatusPending:    "⬜",
			task.StatusInProgress: "🔵",
			task.StatusCompleted:  "✅",
			task.StatusFailed:     "❌",
		}
		for _, t := range tasks {
			icon := statusIcons[t.Status]
			if icon == "" {
				icon = "⬜"
			}

			// Title line
			md.WriteString(fmt.Sprintf("### %s %s\n\n", icon, t.Title))

			// Metadata line: effort, complexity, branch
			md.WriteString(fmt.Sprintf("**Effort**: %s · **Complexity**: %s", t.Effort, t.Complexity))
			if t.BranchName != "" {
				md.WriteString(fmt.Sprintf("  \n**Branch**: `%s`", t.BranchName))
			}

			// PR outcome
			if t.PRURL != "" {
				prLabel := t.PRURL
				if t.PRNumber != nil && *t.PRNumber > 0 {
					prLabel = fmt.Sprintf("#%d", *t.PRNumber)
				}
				md.WriteString(fmt.Sprintf("  \n**PR**: [%s](%s)", prLabel, t.PRURL))
			} else if t.PRError != "" {
				md.WriteString(fmt.Sprintf("  \n**PR**: _%s_", t.PRError))
			}
			md.WriteString("\n\n")

			// Description (implementation summary from breakdown)
			if t.Description != "" {
				md.WriteString(t.Description)
				md.WriteString("\n\n")
			}

			md.WriteString("---\n\n")
		}
	}

	return md.String(), nil
}

func (s *Server) handleExportPlan(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planID")
	p, err := s.planStore.Get(planID)
	if err != nil || p == nil {
		http.Error(w, "plan not found", http.StatusNotFound)
		return
	}

	content, err := s.generatePlanMarkdown(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	filename := strings.ToLower(strings.ReplaceAll(p.Title, " ", "-"))
	filename = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, filename)
	if filename == "" {
		filename = "plan"
	}
	if len(filename) > 50 {
		filename = filename[:50]
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.md"`, filename))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(content))
}

// tryArchivePlan checks whether all tasks in the plan are completed and, if so,
// writes the plan as a markdown file to <projectDir>/.ogcode/archives/<planID>.md.
// The file is written only once — subsequent calls are no-ops when the file exists.
func (s *Server) tryArchivePlan(planID string) error {
	p, err := s.planStore.Get(planID)
	if err != nil || p == nil || p.Status != plan.StatusLocked {
		return err
	}

	tasks, err := s.taskStore.ListByPlan(planID)
	if err != nil || len(tasks) == 0 {
		return err
	}
	for _, t := range tasks {
		if t.Status != task.StatusCompleted {
			return nil
		}
	}

	archiveDir := filepath.Join(p.Directory, ".ogcode", "archives")
	// Use "<title-slug>-<planID>.md" so files are human-readable but still unique.
	slug := git.Slugify(p.Title)
	archivePath := filepath.Join(archiveDir, slug+"-"+planID+".md")

	// Skip if already archived (use DB gate, not filesystem).
	if p.ArchivedAt > 0 {
		return nil
	}

	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		slog.Error("create archive dir", "plan", planID, "err", err)
		return err
	}

	content, err := s.generatePlanMarkdown(p)
	if err != nil {
		slog.Error("generate plan markdown for archive", "plan", planID, "err", err)
		return err
	}
	if err := os.WriteFile(archivePath, []byte(content), 0o644); err != nil {
		slog.Error("write plan archive", "plan", planID, "path", archivePath, "err", err)
		return err
	}
	// Mark as archived in DB only after file write succeeds.
	if err := s.planStore.Archive(planID); err != nil {
		slog.Error("set archived flag", "plan", planID, "err", err)
	}
	slog.Info("plan archived", "plan", planID, "path", archivePath)
	s.bus.Publish("plan.archived", map[string]string{"planId": planID, "path": archivePath})
	return nil
}

// planArchivePaths returns absolute paths to all completed plan archives in
// <directory>/.ogcode/archives/, excluding the current plan's own file.
func (s *Server) planArchivePaths(directory, excludePlanID string) []string {
	// Use the DB to filter only plans that are locked and have been archived.
	plans, err := s.planStore.ListArchived(directory)
	if err != nil || len(plans) == 0 {
		return nil
	}

	archiveDir := filepath.Join(directory, ".ogcode", "archives")
	var paths []string
	for _, p := range plans {
		if p.ID == excludePlanID {
			continue
		}
		// Filename matches the archive format: "<slug>-<planID>.md"
		slug := git.Slugify(p.Title)
		archivePath := filepath.Join(archiveDir, slug+"-"+p.ID+".md")
		paths = append(paths, archivePath)
	}
	return paths
}

// autoNamePlan generates a short descriptive title for the plan from the user's
// first message, then updates the plan and publishes an event.
func (s *Server) autoNamePlan(p *plan.Plan, userMessage string) {
	slog.Info("auto-name plan: starting", "plan", p.ID, "model", p.Model)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Resolve the provider from the plan's model
	var pr provider.Provider
	modelID := p.Model
	if modelID != "" {
		pr = s.registry.ResolveProvider(modelID)
	}
	if pr == nil {
		pr = s.defaultProvider
	}
	if pr == nil {
		slog.Warn("auto-name plan: no provider available, skipping", "plan", p.ID)
		return
	}
	// If modelID is still empty, resolve the provider's default model so we
	// never send an empty model ID to the API.
	if modelID == "" && pr != nil {
		for _, m := range pr.Models() {
			if m.Default {
				modelID = m.ID
				break
			}
		}
		if modelID == "" {
			if models := pr.Models(); len(models) > 0 {
				modelID = models[0].ID
			}
		}
	}
	slog.Info("auto-name plan: using provider/model", "plan", p.ID, "provider", pr.ID(), "model", modelID)

	systemPrompt := "Generate a short, concise title (maximum 7 words) that captures the goal of the request below. Return ONLY the title, no quotes, no extra text, no markdown."
	text, err := collectStreamText(ctx, pr, modelID, systemPrompt, userMessage)
	if err != nil {
		slog.Warn("auto-name plan: stream failed", "plan", p.ID, "err", err)
		return
	}
	slog.Info("auto-name plan: raw response", "plan", p.ID, "text", text, "len", len(text))

	title := strings.TrimSpace(text)
	// Clean up common artifacts from LLM output
	title = strings.Trim(title, "\"'`*#- \n")
	if title == "" {
		slog.Warn("auto-name plan: title empty after trim, falling back to message excerpt", "plan", p.ID, "raw", text)
		title = excerptTitle(userMessage, 7)
	}
	if title == "" {
		slog.Warn("auto-name plan: could not derive title", "plan", p.ID)
		return
	}
	// Cap length at 100 chars to keep it display-friendly
	if len(title) > 100 {
		title = title[:100]
	}

	p.Title = title
	p.UpdatedAt = plan.Now()
	if err := s.planStore.Update(p); err != nil {
		slog.Error("auto-name plan: update failed", "plan", p.ID, "err", err)
		return
	}

	// Also update the session title
	sessionID := session.SessionID(p.SessionID)
	sess, err := s.store.Get(sessionID)
	if err == nil && sess != nil {
		sess.Title = "Plan: " + title
		sess.UpdatedAt = session.Now()
		if err := s.store.Update(sess); err != nil {
			slog.Error("auto-name plan: update session title", "plan", p.ID, "err", err)
		}
	} else if err != nil {
		slog.Error("auto-name plan: get session for title", "plan", p.ID, "err", err)
	}

	s.bus.Publish("plan.updated", p)
	slog.Info("auto-named plan", "plan", p.ID, "title", title)
}

// excerptTitle returns the first maxWords words of s, capped at 80 chars,
// used as a fallback title when the LLM returns empty.
func excerptTitle(s string, maxWords int) string {
	s = strings.TrimSpace(s)
	words := strings.Fields(s)
	if len(words) > maxWords {
		words = words[:maxWords]
	}
	title := strings.Join(words, " ")
	if len(title) > 80 {
		title = title[:80]
	}
	return title
}

// collectStreamText sends a simple system+user prompt to the provider and
// collects all text deltas into a single string. Used for short generations
// like title naming.
//
// MaxTokens is set high enough for thinking/reasoning models (e.g. MiniMax,
// DeepSeek-R1, Qwen3-thinking) that spend tokens on internal reasoning before
// producing visible content. If the model produces no content text but does
// produce reasoning text, we fall back to the reasoning so the title is never
// empty just because the model is a thinker.
func collectStreamText(ctx context.Context, pr provider.Provider, modelID, systemPrompt, userMessage string) (string, error) {
	userParts, _ := json.Marshal([]provider.ContentPart{{Type: "text", Text: userMessage}})

	req := provider.StreamRequest{
		Model:  modelID,
		System: []string{systemPrompt},
		Messages: []provider.ModelMessage{
			{Role: "user", Content: userParts},
		},
		Temperature: 0.3,
		MaxTokens:   500, // enough budget for thinking models to reason + answer
		Abort:       ctx,
	}

	ch, err := pr.StreamChat(ctx, req)
	if err != nil {
		return "", fmt.Errorf("stream chat: %w", err)
	}

	var text strings.Builder
	var reasoning strings.Builder
	for ev := range ch {
		if ev.Type == provider.EventTextDelta {
			text.WriteString(ev.Text)
		}
		if ev.Type == provider.EventReasoning {
			reasoning.WriteString(ev.Text)
		}
		if ev.Type == provider.EventError {
			return text.String(), fmt.Errorf("stream error: %s", ev.Error)
		}
	}
	// Thinking models emit reasoning but sometimes produce empty content.
	// Use the reasoning text as a fallback so we always have something to work with.
	if text.Len() == 0 && reasoning.Len() > 0 {
		return reasoning.String(), nil
	}
	return text.String(), nil
}
