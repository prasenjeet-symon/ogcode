package task

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

type Store struct {
	db *db.DB
}

func NewStore(database *db.DB) *Store {
	return &Store{db: database}
}

func (s *Store) Create(task *Task) error {
	depsJSON, err := json.Marshal(task.Dependencies)
	if err != nil {
		return fmt.Errorf("marshal dependencies: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO task (id, plan_id, session_id, parent_task_id, title, description, effort, complexity, status, dependencies, branch_name, chain_branch, worktree_path, pr_url, pr_number, pr_error, model, provider, order_index, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.PlanID, task.SessionID, task.ParentTaskID, task.Title, task.Description, task.Effort, task.Complexity, task.Status, string(depsJSON), task.BranchName, task.ChainBranch, task.WorktreePath, task.PRURL, task.PRNumber, task.PRError, task.Model, task.Provider, task.OrderIndex, task.CreatedAt, task.UpdatedAt,
	)
	return err
}

func (s *Store) Get(id string) (*Task, error) {
	row := s.db.QueryRow(
		`SELECT id, plan_id, session_id, parent_task_id, title, description, effort, complexity, status, dependencies, branch_name, chain_branch, worktree_path, pr_url, pr_number, pr_error, model, provider, order_index, time_created, time_updated
		 FROM task WHERE id = ?`, id,
	)
	var t Task
	var sessionID, parentTaskID sql.NullString
	var prNumber sql.NullInt64
	var depsJSON string
	err := row.Scan(&t.ID, &t.PlanID, &sessionID, &parentTaskID, &t.Title, &t.Description, &t.Effort, &t.Complexity, &t.Status, &depsJSON, &t.BranchName, &t.ChainBranch, &t.WorktreePath, &t.PRURL, &prNumber, &t.PRError, &t.Model, &t.Provider, &t.OrderIndex, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}

	if sessionID.Valid {
		t.SessionID = &sessionID.String
	}
	if parentTaskID.Valid {
		t.ParentTaskID = &parentTaskID.String
	}
	if prNumber.Valid {
		n := int(prNumber.Int64)
		t.PRNumber = &n
	}

	if err := json.Unmarshal([]byte(depsJSON), &t.Dependencies); err != nil {
		t.Dependencies = []string{}
	}

	return &t, nil
}

func (s *Store) ListByPlan(planID string) ([]*Task, error) {
	rows, err := s.db.Query(
		`SELECT id, plan_id, session_id, parent_task_id, title, description, effort, complexity, status, dependencies, branch_name, chain_branch, worktree_path, pr_url, pr_number, pr_error, model, provider, order_index, time_created, time_updated
		 FROM task WHERE plan_id = ? ORDER BY order_index ASC`, planID,
	)
	if err != nil {
		return nil, fmt.Errorf("list tasks by plan: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var t Task
		var sessionID, parentTaskID sql.NullString
		var prNumber sql.NullInt64
		var depsJSON string
		if err := rows.Scan(&t.ID, &t.PlanID, &sessionID, &parentTaskID, &t.Title, &t.Description, &t.Effort, &t.Complexity, &t.Status, &depsJSON, &t.BranchName, &t.ChainBranch, &t.WorktreePath, &t.PRURL, &prNumber, &t.PRError, &t.Model, &t.Provider, &t.OrderIndex, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		if sessionID.Valid {
			t.SessionID = &sessionID.String
		}
		if parentTaskID.Valid {
			t.ParentTaskID = &parentTaskID.String
		}
		if prNumber.Valid {
			n := int(prNumber.Int64)
			t.PRNumber = &n
		}
		if err := json.Unmarshal([]byte(depsJSON), &t.Dependencies); err != nil {
			t.Dependencies = []string{}
		}
		tasks = append(tasks, &t)
	}
	return tasks, nil
}

func (s *Store) Update(task *Task) error {
	depsJSON, err := json.Marshal(task.Dependencies)
	if err != nil {
		return fmt.Errorf("marshal dependencies: %w", err)
	}
	_, err = s.db.Exec(
		`UPDATE task SET session_id = ?, parent_task_id = ?, title = ?, description = ?, effort = ?, complexity = ?, status = ?, dependencies = ?, branch_name = ?, chain_branch = ?, worktree_path = ?, pr_url = ?, pr_number = ?, pr_error = ?, model = ?, provider = ?, order_index = ?, time_updated = ? WHERE id = ?`,
		task.SessionID, task.ParentTaskID, task.Title, task.Description, task.Effort, task.Complexity, task.Status, string(depsJSON), task.BranchName, task.ChainBranch, task.WorktreePath, task.PRURL, task.PRNumber, task.PRError, task.Model, task.Provider, task.OrderIndex, task.UpdatedAt, task.ID,
	)
	return err
}

func (s *Store) UpdateStatus(id string, status string) error {
	_, err := s.db.Exec(
		`UPDATE task SET status = ?, time_updated = ? WHERE id = ?`,
		status, now(), id,
	)
	return err
}

func (s *Store) GetReadyTasks(planID string) ([]*Task, error) {
	tasks, err := s.ListByPlan(planID)
	if err != nil {
		return nil, err
	}

	statusMap := make(map[string]string)
	for _, t := range tasks {
		statusMap[t.ID] = t.Status
	}

	var ready []*Task
	for _, t := range tasks {
		if t.Status != StatusPending {
			continue
		}
		allDepsCompleted := true
		for _, depID := range t.Dependencies {
			if statusMap[depID] != StatusCompleted {
				allDepsCompleted = false
				break
			}
		}
		if allDepsCompleted {
			ready = append(ready, t)
		}
	}
	return ready, nil
}

func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM task WHERE id = ?`, id)
	return err
}

// Claim atomically transitions a pending task to in_progress.
// It sets the session_id, branch_name, and worktree_path in a single UPDATE
// that only succeeds when the task is still pending with no session assigned.
// Returns false (no error) when another goroutine already claimed the task.
func (s *Store) Claim(taskID, sessionID, branchName, worktreePath string) (bool, error) {
	result, err := s.db.Exec(
		`UPDATE task SET session_id = ?, status = ?, branch_name = ?, worktree_path = ?, time_updated = ?
		 WHERE id = ? AND status = ? AND session_id IS NULL`,
		sessionID, StatusInProgress, branchName, worktreePath, Now(),
		taskID, StatusPending,
	)
	if err != nil {
		return false, fmt.Errorf("claim task: %w", err)
	}
	n, _ := result.RowsAffected()
	return n > 0, nil
}

// ResetFailed atomically clears all runtime state on a failed task and sets it
// back to pending so it can be re-executed cleanly. The UPDATE is conditional on
// status=failed so concurrent calls are safe.
func (s *Store) ResetFailed(id string) error {
	_, err := s.db.Exec(
		`UPDATE task SET status = ?, session_id = NULL, branch_name = '', worktree_path = '',
		  pr_url = '', pr_number = NULL, pr_error = '', time_updated = ?
		 WHERE id = ? AND status = ?`,
		StatusPending, Now(), id, StatusFailed,
	)
	return err
}

// FailStuckTasks marks every in_progress task as failed.
// Called on server startup to recover tasks that were running when the
// server was last shut down or crashed.
// Returns the tasks that were marked as failed (including their branch_name
// and worktree_path) so cleanup can be performed.
func (s *Store) FailStuckTasks() ([]*Task, error) {
	// First get the tasks that will be updated
	rows, err := s.db.Query(
		`SELECT id, branch_name, worktree_path, status FROM task WHERE status = ?`,
		StatusInProgress,
	)
	if err != nil {
		return nil, fmt.Errorf("fail stuck tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var t Task
		var branchName, worktreePath sql.NullString
		var status string
		if err := rows.Scan(&t.ID, &branchName, &worktreePath, &status); err != nil {
			return nil, err
		}
		if branchName.Valid {
			t.BranchName = branchName.String
		}
		if worktreePath.Valid {
			t.WorktreePath = worktreePath.String
		}
		t.Status = status
		tasks = append(tasks, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Now update them to failed status
	_, err = s.db.Exec(
		`UPDATE task SET status = ?, time_updated = ? WHERE status = ?`,
		StatusFailed, Now(), StatusInProgress,
	)
	if err != nil {
		return nil, fmt.Errorf("fail stuck tasks: %w", err)
	}

	return tasks, nil
}
