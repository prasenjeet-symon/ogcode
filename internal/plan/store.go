package plan

import (
	"database/sql"
	"fmt"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// Store provides persistence operations for Plan entities.
type Store struct {
	db *db.DB
}

// NewStore creates a new Plan store backed by the given database.
func NewStore(database *db.DB) *Store {
	return &Store{db: database}
}

// Create inserts a new plan into the database.
func (s *Store) Create(plan *Plan) error {
	_, err := s.db.Exec(
		`INSERT INTO plan (id, session_id, project_id, directory, title, status, model, compaction_summary, breakdown_status, breakdown_warnings, archived_at, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		plan.ID, plan.SessionID, plan.ProjectID, plan.Directory, plan.Title, plan.Status, plan.Model, plan.CompactionSummary, plan.BreakdownStatus, plan.BreakdownWarnings, plan.ArchivedAt, plan.CreatedAt, plan.UpdatedAt,
	)
	return err
}

// Get retrieves a plan by ID. Returns nil if not found.
func (s *Store) Get(id string) (*Plan, error) {
	row := s.db.QueryRow(
		`SELECT id, session_id, project_id, directory, title, status, model, compaction_summary, breakdown_status, breakdown_warnings, archived_at, time_created, time_updated
		 FROM plan WHERE id = ?`, id,
	)
	var p Plan
	err := row.Scan(&p.ID, &p.SessionID, &p.ProjectID, &p.Directory, &p.Title, &p.Status, &p.Model, &p.CompactionSummary, &p.BreakdownStatus, &p.BreakdownWarnings, &p.ArchivedAt, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get plan: %w", err)
	}
	return &p, nil
}

// List returns all plans for a given directory, ordered by most recently updated.
func (s *Store) List(directory string) ([]*Plan, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, project_id, directory, title, status, model, compaction_summary, breakdown_status, breakdown_warnings, archived_at, time_created, time_updated
		 FROM plan WHERE directory = ? ORDER BY time_updated DESC`, directory,
	)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	defer rows.Close()

	var plans []*Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.SessionID, &p.ProjectID, &p.Directory, &p.Title, &p.Status, &p.Model, &p.CompactionSummary, &p.BreakdownStatus, &p.BreakdownWarnings, &p.ArchivedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		plans = append(plans, &p)
	}
	return plans, nil
}

// Update modifies a plan's mutable fields (title, model, status, compaction_summary, breakdown_status, breakdown_warnings, archived_at).
func (s *Store) Update(plan *Plan) error {
	_, err := s.db.Exec(
		`UPDATE plan SET title = ?, model = ?, status = ?, compaction_summary = ?, breakdown_status = ?, breakdown_warnings = ?, archived_at = ?, time_updated = ? WHERE id = ?`,
		plan.Title, plan.Model, plan.Status, plan.CompactionSummary, plan.BreakdownStatus, plan.BreakdownWarnings, plan.ArchivedAt, plan.UpdatedAt, plan.ID,
	)
	return err
}

// Lock sets a plan's status to "locked", preventing further modifications.
// Returns an error if the plan is already locked.
func (s *Store) Lock(id string) error {
	// First check if the plan is already locked
	plan, err := s.Get(id)
	if err != nil {
		return fmt.Errorf("lock plan: %w", err)
	}
	if plan == nil {
		return fmt.Errorf("lock plan: not found")
	}
	if plan.Status == StatusLocked {
		return fmt.Errorf("plan is already locked")
	}

	plan.Status = StatusLocked
	plan.UpdatedAt = now()
	return s.Update(plan)
}

// Archive marks a plan as archived by setting archived_at and time_updated to the current timestamp.
func (s *Store) Archive(id string) error {
	now := Now()
	_, err := s.db.Exec(
		`UPDATE plan SET archived_at = ?, time_updated = ? WHERE id = ?`,
		now, now, id,
	)
	return err
}

// Delete removes a plan by ID.
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM plan WHERE id = ?`, id)
	return err
}

// ListArchived returns all archived plans for a given directory (status = locked AND archived_at > 0).
func (s *Store) ListArchived(directory string) ([]*Plan, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, project_id, directory, title, status, model, compaction_summary, breakdown_status, breakdown_warnings, archived_at, time_created, time_updated
		 FROM plan WHERE directory = ? AND status = ? AND archived_at > 0 ORDER BY time_updated DESC`,
		directory, StatusLocked,
	)
	if err != nil {
		return nil, fmt.Errorf("list archived plans: %w", err)
	}
	defer rows.Close()

	var plans []*Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.SessionID, &p.ProjectID, &p.Directory, &p.Title, &p.Status, &p.Model, &p.CompactionSummary, &p.BreakdownStatus, &p.BreakdownWarnings, &p.ArchivedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		plans = append(plans, &p)
	}
	return plans, nil
}