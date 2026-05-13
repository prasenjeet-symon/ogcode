package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/git"
	"github.com/prasenjeet-symon/ogcode/internal/id"
	"github.com/prasenjeet-symon/ogcode/internal/plan"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/task"
)

const taskExecutionTimeout = 30 * time.Minute

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planID")
	tasks, err := s.taskStore.ListByPlan(planID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tasks == nil {
		tasks = []*task.Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleCreateTasks(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planID")

	p, err := s.planStore.Get(planID)
	if err != nil || p == nil {
		http.Error(w, "plan not found", http.StatusNotFound)
		return
	}

	var input struct {
		Tasks []struct {
			Title        string   `json:"title"`
			Description  string   `json:"description"`
			Effort       string   `json:"effort"`
			Complexity   string   `json:"complexity"`
			Dependencies []string `json:"dependencies"`
			OrderIndex   int      `json:"orderIndex"`
		} `json:"tasks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var created []*task.Task
	now := task.Now()
	for _, t := range input.Tasks {
		effort := t.Effort
		if effort == "" {
			effort = task.EffortM
		}
		complexity := t.Complexity
		if complexity == "" {
			complexity = task.ComplexityMedium
		}

		newTask := &task.Task{
			ID:           string(id.NewTaskID()),
			PlanID:       planID,
			Title:        t.Title,
			Description:  t.Description,
			Effort:       effort,
			Complexity:   complexity,
			Status:       task.StatusPending,
			Dependencies: t.Dependencies,
			OrderIndex:   t.OrderIndex,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := s.taskStore.Create(newTask); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		created = append(created, newTask)
	}

	s.bus.Publish("task.created", map[string]any{"planId": planID, "count": len(created)})
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	t, err := s.taskStore.Get(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if t == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	t, err := s.taskStore.Get(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if t == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	var update struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Effort      *string `json:"effort"`
		Complexity  *string `json:"complexity"`
		Status      *string `json:"status"`
		BranchName  *string `json:"branchName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if update.Title != nil {
		t.Title = *update.Title
	}
	if update.Description != nil {
		t.Description = *update.Description
	}
	if update.Effort != nil {
		switch *update.Effort {
		case task.EffortS, task.EffortM, task.EffortL, task.EffortXL:
			t.Effort = *update.Effort
		default:
			http.Error(w, "invalid effort: "+*update.Effort, http.StatusBadRequest)
			return
		}
	}
	if update.Complexity != nil {
		switch *update.Complexity {
		case task.ComplexityLow, task.ComplexityMedium, task.ComplexityHigh:
			t.Complexity = *update.Complexity
		default:
			http.Error(w, "invalid complexity: "+*update.Complexity, http.StatusBadRequest)
			return
		}
	}
	if update.Status != nil {
		newStatus := *update.Status
		validTransitions := map[string][]string{
			task.StatusPending:    {task.StatusInProgress},
			task.StatusInProgress: {task.StatusCompleted, task.StatusFailed},
			task.StatusCompleted:  {},
			task.StatusFailed:     {task.StatusPending},
		}
		allowed, ok := validTransitions[t.Status]
		if !ok {
			http.Error(w, "invalid current status: "+t.Status, http.StatusBadRequest)
			return
		}
		valid := false
		for _, s := range allowed {
			if s == newStatus {
				valid = true
				break
			}
		}
		if !valid {
			http.Error(w, fmt.Sprintf("cannot transition from %s to %s", t.Status, newStatus), http.StatusBadRequest)
			return
		}
		t.Status = newStatus
	}
	if update.BranchName != nil {
		t.BranchName = *update.BranchName
	}
	t.UpdatedAt = task.Now()

	if err := s.taskStore.Update(t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.bus.Publish("task.updated", t)
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleStartTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	t, err := s.taskStore.Get(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if t == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	if t.SessionID != nil {
		http.Error(w, "task already started", http.StatusBadRequest)
		return
	}

	p, _ := s.planStore.Get(t.PlanID)
	if p == nil {
		http.Error(w, "plan not found", http.StatusNotFound)
		return
	}
	if p.Status != plan.StatusLocked {
		http.Error(w, "plan is not locked", http.StatusBadRequest)
		return
	}

	for _, depID := range t.Dependencies {
		dep, err := s.taskStore.Get(depID)
		if err != nil {
			http.Error(w, "dependency not found", http.StatusBadRequest)
			return
		}
		if dep != nil && dep.Status != task.StatusCompleted {
			http.Error(w, fmt.Sprintf("dependency %s not completed (status: %s)", depID, dep.Status), http.StatusBadRequest)
			return
		}
	}

	if err := s.executeTask(t, p); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// executeTask creates a worktree, session, and starts an agent loop for the task.
// It uses an atomic DB claim to prevent double-starts under concurrent auto-start.
func (s *Server) executeTask(t *task.Task, p *plan.Plan) error {
	// In-memory fast guard (reduces DB load in the common case)
	if t.SessionID != nil {
		return fmt.Errorf("task already has a session")
	}
	if t.Status != task.StatusPending {
		return fmt.Errorf("task is not pending (status: %s)", t.Status)
	}

	// Resolve the base branch. Chain tasks branch from the shared chain branch
	// so each task in a sequence sees all prior work. Standalone tasks branch
	// from HEAD.
	baseBranch := t.ChainBranch

	// Serialize repo-level git operations. git worktree add/prune/branch are
	// not safe under concurrent access — they write to .git/worktrees/ and
	// .git/config without holding git's own lock for the full sequence.
	s.gitMu.Lock()
	if baseBranch != "" {
		// Create chain branch from current HEAD on first use; no-op if it exists.
		if err := git.CreateChainBranch(s.dir, baseBranch); err != nil {
			slog.Warn("could not create chain branch, falling back to HEAD",
				"branch", baseBranch, "err", err)
			baseBranch = ""
		}
	}
	slug := git.Slugify(t.Title)
	wt, wtErr := git.CreateTaskWorktree(s.dir, t.ID, slug, baseBranch)
	s.gitMu.Unlock()

	if wtErr != nil {
		return fmt.Errorf("create worktree: %w", wtErr)
	}

	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   s.dir,
		Directory:   wt.Path,
		Title:       "Task: " + t.Title,
		Model:       "",
		SessionType: "build",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if p.Model != "" {
		sess.Model = p.Model
	}
	if err := s.store.Create(sess); err != nil {
		s.gitMu.Lock()
		_ = git.RemoveTaskWorktreeKeepBranch(s.dir, wt.BranchName)
		s.gitMu.Unlock()
		return fmt.Errorf("create session: %w", err)
	}

	// Atomic DB claim — only one goroutine wins when auto-start races with itself.
	claimed, err := s.taskStore.Claim(t.ID, string(sess.ID), wt.BranchName, wt.Path)
	if err != nil {
		s.gitMu.Lock()
		_ = git.RemoveTaskWorktreeKeepBranch(s.dir, wt.BranchName)
		s.gitMu.Unlock()
		_ = s.store.Delete(sess.ID)
		return fmt.Errorf("claim task: %w", err)
	}
	if !claimed {
		s.gitMu.Lock()
		_ = git.RemoveTaskWorktreeKeepBranch(s.dir, wt.BranchName)
		s.gitMu.Unlock()
		_ = s.store.Delete(sess.ID)
		slog.Info("task already claimed by another goroutine, skipping", "task", t.ID)
		return fmt.Errorf("task already started")
	}

	// Update in-memory struct to reflect DB state
	sessionIDStr := string(sess.ID)
	t.SessionID = &sessionIDStr
	t.BranchName = wt.BranchName
	t.WorktreePath = wt.Path
	t.Status = task.StatusInProgress
	t.UpdatedAt = task.Now()

	promptContent := fmt.Sprintf(
		"**Task**: %s\n\n"+
			"**Branch**: `%s`\n\n"+
			"---\n\n%s\n\n---\n\n"+
			"You are already checked out on branch `%s` in the task worktree. "+
			"Implement the task, then commit ALL your changes before finishing:\n"+
			"```\n"+
			"git add -A && git commit -m 'your descriptive message'\n"+
			"```\n\n"+
			"Focus only on what this task requires.",
		t.Title, wt.BranchName, t.Description, wt.BranchName)

	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: sess.ID,
		Role:      session.RoleUser,
		CreatedAt: session.Now(),
	}
	if err := s.store.CreateMessage(userMsg); err != nil {
		return fmt.Errorf("create message: %w", err)
	}

	textData, _ := json.Marshal(session.TextPartData{Text: promptContent})
	userPart := &session.Part{
		ID:        session.NewPartID(),
		MessageID: userMsg.ID,
		SessionID: sess.ID,
		Type:      session.PartText,
		Data:      textData,
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := s.store.CreatePart(userPart); err != nil {
		return fmt.Errorf("create part: %w", err)
	}

	s.bus.Publish("message.updated", userMsg)

	// 30-minute timeout per task; user abort uses context.Canceled,
	// timeout uses context.DeadlineExceeded — handled separately below.
	ctx, cancel := context.WithTimeout(context.Background(), taskExecutionTimeout)

	s.mu.Lock()
	if old, ok := s.running[sess.ID]; ok {
		old()
	}
	s.nextToken++
	token := s.nextToken
	s.running[sess.ID] = cancel
	s.runningToken[sess.ID] = token
	s.mu.Unlock()

	taskSnapshot := *t
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("task agent loop panicked", "task", taskSnapshot.ID, "panic", r)
				func() {
					defer func() { _ = recover() }()
					s.autoFailTask(&taskSnapshot)
				}()
			}
		}()
		defer func() {
			s.mu.Lock()
			if s.runningToken[sess.ID] == token {
				delete(s.running, sess.ID)
				delete(s.runningToken, sess.ID)
			}
			s.mu.Unlock()
			cancel()
		}()

		loopErr := s.loopRunner.RunLoop(ctx, sess.ID, "build")

		switch ctx.Err() {
		case context.Canceled:
			slog.Info("task agent loop cancelled by user", "task", taskSnapshot.ID)
			return
		case context.DeadlineExceeded:
			slog.Warn("task agent loop timed out", "task", taskSnapshot.ID, "timeout", taskExecutionTimeout)
			s.autoFailTask(&taskSnapshot)
			return
		}

		if loopErr != nil {
			slog.Error("task agent loop error", "task", taskSnapshot.ID, "err", loopErr)
			s.autoFailTask(&taskSnapshot)
		} else {
			s.autoCompleteTask(&taskSnapshot)
		}
	}()

	s.bus.Publish("task.started", t)
	return nil
}

// completeTaskWithPR handles the post-completion steps shared by autoCompleteTask
// and handleCompleteTask: auto-commit any leftover changes, push the branch,
// open a PR via gh, and clean up the worktree.
// Any reason a PR was not created is stored in t.PRError so the UI can surface it.
func (s *Server) completeTaskWithPR(t *task.Task) {
	// Auto-commit any changes the agent left uncommitted.
	if t.WorktreePath != "" {
		msg := fmt.Sprintf("chore: complete task %q", t.Title)
		if err := git.CommitAllChanges(t.WorktreePath, msg); err != nil {
			slog.Warn("auto-commit before push", "task", t.ID, "err", err)
		}
	}

	// Bound all network operations (push + PR creation) to 3 minutes total.
	// Without a deadline, a slow or unresponsive GitHub can stall this goroutine
	// indefinitely and block dependent task auto-start.
	const prNetworkTimeout = 3 * time.Minute
	prCtx, prCancel := context.WithTimeout(context.Background(), prNetworkTimeout)
	defer prCancel()

	prErr := ""
	pushed := false
	if t.BranchName == "" {
		prErr = "no branch name assigned to task"
	} else {
		var pushErr error
		pushed, pushErr = git.PushBranch(prCtx, s.dir, t.BranchName)
		switch {
		case pushErr != nil:
			prErr = fmt.Sprintf("push failed: %s", pushErr.Error())
			slog.Error("push branch", "task", t.ID, "branch", t.BranchName, "err", pushErr)
		case !pushed:
			prErr = "no remote configured — add a GitHub remote (git remote add origin <url>) to enable automatic PR creation"
			slog.Warn("push skipped: no remote configured", "task", t.ID, "branch", t.BranchName)
		}
	}

	if pushed {
		prTitle := t.Title
		prBody := standalonePRBody(t)
		pr, err := git.CreatePR(prCtx, s.dir, t.BranchName, prTitle, prBody, "")
		if err != nil {
			prErr = fmt.Sprintf("PR creation failed: %s", err.Error())
			slog.Warn("create PR failed", "task", t.ID, "err", err)
		} else if pr != nil {
			t.PRURL = pr.URL
			t.PRNumber = &pr.Number
		}
	}

	// Persist PR outcome (URL on success, error reason on failure).
	t.PRError = prErr
	t.UpdatedAt = task.Now()
	if err := s.taskStore.Update(t); err != nil {
		slog.Error("save PR info", "task", t.ID, "err", err)
	}

	// Remove worktree. Keep the branch if not pushed (work stays accessible locally).
	// Run in a goroutine to avoid blocking the completion path, but serialize
	// through gitMu so it doesn't race with concurrent worktree-add calls.
	if t.WorktreePath != "" {
		branchName := t.BranchName
		go func() {
			s.gitMu.Lock()
			defer s.gitMu.Unlock()
			var err error
			if pushed {
				err = git.RemoveTaskWorktree(s.dir, branchName)
			} else {
				err = git.RemoveTaskWorktreeKeepBranch(s.dir, branchName)
			}
			if err != nil {
				slog.Warn("remove worktree", "task", t.ID, "err", err)
			}
		}()
	}
}

// autoStartDependentTasks finds all pending tasks whose dependencies are all
// completed and starts them automatically. Called after a task completes.
func (s *Server) autoStartDependentTasks(planID string) {
	ready, err := s.taskStore.GetReadyTasks(planID)
	if err != nil {
		slog.Error("get ready tasks for auto-start", "plan", planID, "err", err)
		return
	}
	if len(ready) == 0 {
		return
	}

	p, err := s.planStore.Get(planID)
	if err != nil || p == nil || p.Status != plan.StatusLocked {
		return
	}

	slog.Info("auto-starting dependent tasks", "plan", planID, "count", len(ready))
	for _, t := range ready {
		taskCopy := t
		if err := s.executeTask(taskCopy, p); err != nil {
			slog.Error("auto-start task failed", "task", t.ID, "title", t.Title, "err", err)
		}
	}
}

func (s *Server) autoCompleteTask(t *task.Task) {
	if err := s.taskStore.UpdateStatus(t.ID, task.StatusCompleted); err != nil {
		slog.Error("auto-complete: update task status", "task", t.ID, "err", err)
		return
	}
	t.Status = task.StatusCompleted
	t.UpdatedAt = task.Now()

	// Start dependent tasks BEFORE the worktree is cleaned up. Dependent tasks
	// that share a chain branch need this task's branch ref to still exist locally
	// when CreateTaskWorktree runs. The cleanup goroutine inside completeTask will
	// block on gitMu until after the dependent's worktree is created.
	s.autoStartDependentTasks(t.PlanID)

	p, _ := s.planStore.Get(t.PlanID)
	s.completeTask(t, p)

	fresh, err := s.taskStore.Get(t.ID)
	if err == nil && fresh != nil {
		t = fresh
	}
	s.bus.Publish("task.completed", t)

	if err := s.tryArchivePlan(t.PlanID); err != nil {
		slog.Error("archive plan failed", "plan", t.PlanID, "err", err)
		s.bus.Publish("plan.archive.failed", map[string]any{"planId": t.PlanID, "error": err.Error()})
	}
}

// completeTask routes post-completion work based on whether the task belongs to
// a dependency chain. Chain tasks merge into the shared chain branch and open
// one PR only when the whole chain is done. Standalone tasks open a PR immediately,
// preserving the original behaviour.
func (s *Server) completeTask(t *task.Task, p *plan.Plan) {
	if t.WorktreePath != "" {
		msg := fmt.Sprintf("chore: complete task %q", t.Title)
		if err := git.CommitAllChanges(t.WorktreePath, msg); err != nil {
			slog.Warn("auto-commit before merge/push", "task", t.ID, "err", err)
		}
	}

	if t.ChainBranch == "" {
		// Standalone task — raise a PR immediately, same as before.
		s.completeTaskWithPR(t)
		return
	}

	// Chain task — merge into the shared branch and clean up the task worktree.
	s.gitMu.Lock()
	mergeErr := git.MergeTaskBranch(s.dir, t.ChainBranch, t.BranchName, t.Title)
	s.gitMu.Unlock()

	if mergeErr != nil {
		slog.Error("merge task into chain branch", "task", t.ID, "chain", t.ChainBranch, "err", mergeErr)
		t.PRError = fmt.Sprintf("chain merge failed: %s", mergeErr.Error())
		t.UpdatedAt = task.Now()
		_ = s.taskStore.Update(t)
	}

	if t.WorktreePath != "" {
		branchName := t.BranchName
		go func() {
			s.gitMu.Lock()
			defer s.gitMu.Unlock()
			_ = git.RemoveTaskWorktree(s.dir, branchName)
		}()
	}

	// Open one PR for the whole chain once every task in it has completed.
	if p != nil && s.isChainTail(t, p) {
		go s.openChainPR(t.ChainBranch, p)
	}
}

// isChainTail returns true when t is the last incomplete task in its chain,
// i.e. all other tasks sharing the same ChainBranch are already completed.
func (s *Server) isChainTail(t *task.Task, p *plan.Plan) bool {
	allTasks, err := s.taskStore.ListByPlan(p.ID)
	if err != nil {
		return false
	}
	for _, other := range allTasks {
		if other.ChainBranch != t.ChainBranch || other.ID == t.ID {
			continue
		}
		if other.Status != task.StatusCompleted {
			return false
		}
	}
	return true
}

// openChainPR pushes the shared chain branch and opens a single PR for the
// entire dependency chain. Called once after the last task in the chain completes.
func (s *Server) openChainPR(chainBranch string, p *plan.Plan) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pushed, err := git.PushBranch(ctx, s.dir, chainBranch)
	if err != nil {
		slog.Warn("chain PR: push failed", "branch", chainBranch, "plan", p.ID, "err", err)
		return
	}
	if !pushed {
		slog.Warn("chain PR: no remote configured", "branch", chainBranch, "plan", p.ID)
		return
	}

	// Re-fetch the plan so we get the latest title — autoNamePlan may have
	// updated it asynchronously after the snapshot passed to this function
	// was taken in autoCompleteTask.
	if fresh, err := s.planStore.Get(p.ID); err == nil && fresh != nil {
		p = fresh
	}

	// Collect tasks in this chain in order so the PR body lists them correctly.
	allTasks, _ := s.taskStore.ListByPlan(p.ID)
	var chainTasks []*task.Task
	for _, t := range allTasks {
		if t.ChainBranch == chainBranch {
			chainTasks = append(chainTasks, t)
		}
	}

	title := chainPRTitle(p.Title, chainBranch)
	body := chainPRBody(title, chainTasks)

	pr, err := git.CreatePR(ctx, s.dir, chainBranch, title, body, "")
	if err != nil {
		slog.Warn("chain PR: create failed", "branch", chainBranch, "plan", p.ID, "err", err)
		return
	}
	slog.Info("chain PR opened", "plan", p.ID, "branch", chainBranch, "pr", pr.URL)
}

// standalonePRBody builds a professional PR description for a single standalone task.
func standalonePRBody(t *task.Task) string {
	var b strings.Builder
	b.WriteString("## Summary\n\n")
	if desc := strings.TrimSpace(t.Description); desc != "" {
		b.WriteString(desc)
	} else {
		b.WriteString(t.Title)
	}
	b.WriteString("\n\n---\n\n")
	b.WriteString("*Generated by [ogcode](https://github.com/prasenjeet-symon/ogcode) — agentic coding assistant*")
	return b.String()
}

// chainPRTitle returns a non-empty PR title. It uses the plan title when set;
// otherwise it derives a human-readable title from the chain branch slug.
// e.g. "chain/pln_01KR-create-christmas-route-module" → "Create Christmas Route Module"
func chainPRTitle(planTitle, chainBranch string) string {
	if t := strings.TrimSpace(planTitle); t != "" {
		return t
	}
	// Strip "chain/" prefix, then strip the "<planID8>-" prefix to get the slug.
	slug := chainBranch
	if idx := strings.LastIndex(slug, "/"); idx >= 0 {
		slug = slug[idx+1:]
	}
	if idx := strings.Index(slug, "-"); idx >= 0 {
		slug = slug[idx+1:]
	}
	words := strings.Split(slug, "-")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	if title := strings.Join(words, " "); title != "" {
		return title
	}
	return "Chained tasks"
}

// chainPRBody builds a professional PR description listing each task in the chain.
func chainPRBody(title string, chainTasks []*task.Task) string {
	var b strings.Builder
	b.WriteString("## Summary\n\n")
	if len(chainTasks) == 0 {
		b.WriteString(title)
	} else {
		for _, t := range chainTasks {
			b.WriteString(fmt.Sprintf("- **%s**", t.Title))
			if desc := strings.TrimSpace(t.Description); desc != "" {
				// Use only the first sentence for brevity in the bullet.
				sentence := desc
				if idx := strings.IndexAny(desc, ".\n"); idx > 0 {
					sentence = desc[:idx+1]
				}
				b.WriteString(fmt.Sprintf(" — %s", sentence))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n## Changes\n\n")
	for _, t := range chainTasks {
		b.WriteString(fmt.Sprintf("### %s\n\n", t.Title))
		if desc := strings.TrimSpace(t.Description); desc != "" {
			b.WriteString(desc)
			b.WriteString("\n\n")
		}
	}
	b.WriteString("---\n\n")
	b.WriteString("*Generated by [ogcode](https://github.com/prasenjeet-symon/ogcode) — agentic coding assistant*")
	return b.String()
}

func (s *Server) autoFailTask(t *task.Task) {
	if err := s.taskStore.UpdateStatus(t.ID, task.StatusFailed); err != nil {
		slog.Error("auto-fail: update task status", "task", t.ID, "err", err)
		return
	}
	t.Status = task.StatusFailed
	t.UpdatedAt = task.Now()

	if t.WorktreePath != "" {
		go func() {
			if err := git.RemoveTaskWorktreeKeepBranch(s.dir, t.BranchName); err != nil {
				slog.Warn("auto-fail: remove worktree", "task", t.ID, "err", err)
			}
		}()
	}

	fresh, err := s.taskStore.Get(t.ID)
	if err == nil && fresh != nil {
		t = fresh
	}
	s.bus.Publish("task.failed", t)
}

func (s *Server) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	t, err := s.taskStore.Get(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if t == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if t.Status != task.StatusInProgress {
		http.Error(w, fmt.Sprintf("task is %s, not in_progress", t.Status), http.StatusBadRequest)
		return
	}

	if err := s.taskStore.UpdateStatus(taskID, task.StatusCompleted); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	t.Status = task.StatusCompleted
	t.UpdatedAt = task.Now()

	p, _ := s.planStore.Get(t.PlanID)
	s.completeTask(t, p)

	fresh, dbErr := s.taskStore.Get(t.ID)
	if dbErr == nil && fresh != nil {
		t = fresh
	}
	s.bus.Publish("task.completed", t)
	writeJSON(w, http.StatusOK, t)

	go s.autoStartDependentTasks(t.PlanID)
	if err := s.tryArchivePlan(t.PlanID); err != nil {
		slog.Error("archive plan failed", "plan", t.PlanID, "err", err)
		s.bus.Publish("plan.archive.failed", map[string]any{"planId": t.PlanID, "error": err.Error()})
	}
}

func (s *Server) handleFailTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	t, err := s.taskStore.Get(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if t == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if t.Status != task.StatusInProgress {
		http.Error(w, fmt.Sprintf("task is %s, not in_progress", t.Status), http.StatusBadRequest)
		return
	}

	if err := s.taskStore.UpdateStatus(taskID, task.StatusFailed); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	t.Status = task.StatusFailed
	t.UpdatedAt = task.Now()

	if t.WorktreePath != "" {
		go func() {
			if err := git.RemoveTaskWorktreeKeepBranch(s.dir, t.BranchName); err != nil {
				slog.Warn("fail: remove worktree", "task", t.ID, "err", err)
			}
		}()
	}

	fresh, dbErr := s.taskStore.Get(t.ID)
	if dbErr == nil && fresh != nil {
		t = fresh
	}
	s.bus.Publish("task.failed", t)
	writeJSON(w, http.StatusOK, t)
}

// handleRetryTask performs a clean retry of a failed task: deletes the stale
// branch, resets all runtime fields in the DB, then re-executes immediately if
// the plan is locked and all dependencies are still completed.
func (s *Server) handleRetryTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	t, err := s.taskStore.Get(taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if t == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if t.Status != task.StatusFailed {
		http.Error(w, fmt.Sprintf("task is %s, not failed", t.Status), http.StatusBadRequest)
		return
	}

	// Remove the stale branch so the next execution starts from a clean slate.
	// Serialize through gitMu to prevent races with concurrent git operations.
	if t.BranchName != "" {
		s.gitMu.Lock()
		err := git.DeleteBranch(s.dir, t.BranchName)
		s.gitMu.Unlock()
		if err != nil {
			slog.Warn("retry: delete stale branch", "task", t.ID, "branch", t.BranchName, "err", err)
		}
	}

	if err := s.taskStore.ResetFailed(t.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	t.Status = task.StatusPending
	t.SessionID = nil
	t.BranchName = ""
	t.WorktreePath = ""
	t.PRURL = ""
	t.PRNumber = nil

	p, _ := s.planStore.Get(t.PlanID)
	if p != nil && p.Status == plan.StatusLocked {
		depsOK := true
		for _, depID := range t.Dependencies {
			dep, _ := s.taskStore.Get(depID)
			if dep == nil || dep.Status != task.StatusCompleted {
				depsOK = false
				break
			}
		}
		if depsOK {
			if err := s.executeTask(t, p); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	fresh, _ := s.taskStore.Get(t.ID)
	if fresh != nil {
		t = fresh
	}
	s.bus.Publish("task.retried", t)
	writeJSON(w, http.StatusOK, t)
}
