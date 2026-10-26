// Package timer is the durable record of one-shot reminders an agent
// schedules for itself. The server claims the timers whose deadline has
// passed and delivers each note back into the owning session's loop as a
// system notice; the rows live in the project database, so a reminder
// scheduled before a restart still fires after it.
package timer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// Timer states. pending is waiting for its fire_at; due has been claimed by a
// claim scan and is waiting to be delivered into its session; delivered has
// reached the session; cancelled was withdrawn before it fired. delivered and
// cancelled are terminal.
const (
	StatusPending   = "pending"
	StatusDue       = "due"
	StatusDelivered = "delivered"
	StatusCancelled = "cancelled"
)

// Timer is one scheduled reminder.
type Timer struct {
	ID        string
	SessionID string
	Note      string
	FireAt    time.Time
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrNotFound reports that a timer is not there to act on: absent, owned by
// another session, or already terminal.
var ErrNotFound = errors.New("timer not found")

// Store is the project database's set of agent timers. The schema is created
// by migration 068, which runs when the project database opens.
type Store struct {
	database *db.DB
}

// Open returns the timer store over the project database.
func Open(database *db.DB) *Store {
	return &Store{database: database}
}

// Create schedules note for delivery into sessionID's loop at fireAt.
func (s *Store) Create(ctx context.Context, sessionID, note string, fireAt time.Time) (Timer, error) {
	id := newTimerID()
	now := time.Now().Unix()
	_, err := s.database.ExecContext(ctx,
		`INSERT INTO agent_timer (id, session_id, note, fire_at, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, sessionID, note, fireAt.Unix(), StatusPending, now, now,
	)
	if err != nil {
		return Timer{}, fmt.Errorf("create timer: %w", err)
	}
	return s.get(ctx, id)
}

// get reads one timer by id, whether or not it exists.
func (s *Store) get(ctx context.Context, id string) (Timer, error) {
	row := s.database.QueryRowContext(ctx,
		`SELECT id, session_id, note, fire_at, status, created_at, updated_at
		 FROM agent_timer WHERE id = ?`, id)
	return scanTimer(row)
}

// List returns the session's timers that are still live to fire or to be
// delivered — pending and due — in fire_at order. Terminal ones are history.
func (s *Store) List(ctx context.Context, sessionID string) ([]Timer, error) {
	rows, err := s.database.QueryContext(ctx,
		`SELECT id, session_id, note, fire_at, status, created_at, updated_at
		 FROM agent_timer WHERE session_id = ? AND status IN (?, ?)
		 ORDER BY fire_at ASC`, sessionID, StatusPending, StatusDue)
	if err != nil {
		return nil, fmt.Errorf("list timers of %s: %w", sessionID, err)
	}
	defer rows.Close()

	timers := []Timer{}
	for rows.Next() {
		t, err := scanTimer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan timer: %w", err)
		}
		timers = append(timers, t)
	}
	return timers, rows.Err()
}

// Cancel withdraws one of the session's own timers. Only a pending or due
// timer can be cancelled: an absent id, another session's id, or an already
// delivered or cancelled one all come back as ErrNotFound, and a cancelled
// timer is returned.
func (s *Store) Cancel(ctx context.Context, sessionID, id string) (Timer, error) {
	res, err := s.database.ExecContext(ctx,
		`UPDATE agent_timer SET status = ?, updated_at = ?
		 WHERE id = ? AND session_id = ? AND status IN (?, ?)`,
		StatusCancelled, time.Now().Unix(), id, sessionID, StatusPending, StatusDue)
	if err != nil {
		return Timer{}, fmt.Errorf("cancel timer %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Timer{}, fmt.Errorf("cancel timer %s: %w", id, err)
	}
	if n == 0 {
		return Timer{}, fmt.Errorf("cancel timer %s: %w", id, ErrNotFound)
	}
	return s.get(ctx, id)
}

// ClaimDue moves every pending timer whose deadline has passed to due and
// returns exactly the rows it moved. The condition lives in the UPDATE, not a
// read-then-write above it, and RETURNING is part of the same statement: a
// second claimant's scan touches only rows still pending, so two claimants —
// two servers or a retry after a crash — can never both return one timer and
// fire its note twice. Rows already marked due stay out of the claim, so an
// unreported delivery is handed over by Undelivered instead.
func (s *Store) ClaimDue(ctx context.Context) ([]Timer, error) {
	rows, err := s.database.QueryContext(ctx,
		`UPDATE agent_timer SET status = ?, updated_at = ?
		 WHERE status = ? AND fire_at <= ?
		 RETURNING id, session_id, note, fire_at, status, created_at, updated_at`,
		StatusDue, time.Now().Unix(), StatusPending, time.Now().Unix())
	if err != nil {
		return nil, fmt.Errorf("claim due timers: %w", err)
	}
	defer rows.Close()

	claimed := []Timer{}
	for rows.Next() {
		t, err := scanTimer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan claimed timer: %w", err)
		}
		claimed = append(claimed, t)
	}
	return claimed, rows.Err()
}

// Undelivered returns the session's claimed-but-not-yet-delivered timers: a
// delivery the server saw through the claim but crashed before reporting is
// still owed to its session after the restart.
func (s *Store) Undelivered(ctx context.Context, sessionID string) ([]Timer, error) {
	rows, err := s.database.QueryContext(ctx,
		`SELECT id, session_id, note, fire_at, status, created_at, updated_at
		 FROM agent_timer WHERE session_id = ? AND status = ? ORDER BY fire_at ASC`,
		sessionID, StatusDue)
	if err != nil {
		return nil, fmt.Errorf("list undelivered timers of %s: %w", sessionID, err)
	}
	defer rows.Close()

	timers := []Timer{}
	for rows.Next() {
		t, err := scanTimer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan timer: %w", err)
		}
		timers = append(timers, t)
	}
	return timers, rows.Err()
}

// MarkDelivered records that these claimed timers reached their sessions. A
// delivered timer is terminal and never reclaimed. An empty list is a no-op.
func (s *Store) MarkDelivered(ctx context.Context, ids []string) error {
	for _, id := range ids {
		res, err := s.database.ExecContext(ctx,
			`UPDATE agent_timer SET status = ?, updated_at = ? WHERE id = ? AND status = ?`,
			StatusDelivered, time.Now().Unix(), id, StatusDue)
		if err != nil {
			return fmt.Errorf("mark timer %s delivered: %w", id, err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return fmt.Errorf("mark timer %s delivered: %w", id, ErrNotFound)
		}
	}
	return nil
}

// Prune deletes the terminal timers — delivered and cancelled — whose
// updated_at predates olderThan, and returns how many rows went. Pending and
// due rows are never touched: a due row is still owed to its session (the
// next turn's catch-up delivers it), so a reminder the agent asked for is
// never silenced by housekeeping, even when its session can never run again.
func (s *Store) Prune(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := s.database.ExecContext(ctx,
		`DELETE FROM agent_timer
		 WHERE status IN (?, ?) AND updated_at < ?`,
		StatusDelivered, StatusCancelled, olderThan.Unix())
	if err != nil {
		return 0, fmt.Errorf("prune timers: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune timers: %w", err)
	}
	return n, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTimer(row rowScanner) (Timer, error) {
	var (
		t                        Timer
		fireAt, created, updated int64
	)
	err := row.Scan(&t.ID, &t.SessionID, &t.Note, &fireAt, &t.Status, &created, &updated)
	if err != nil {
		return Timer{}, err
	}
	t.FireAt = time.Unix(fireAt, 0)
	t.CreatedAt = time.Unix(created, 0)
	t.UpdatedAt = time.Unix(updated, 0)
	return t, nil
}

// newTimerID is "t" plus six random hex characters, in the board store's
// random-hex style, so a client can address a reminder without a session key.
func newTimerID() string {
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand does not fail on a supported platform; fall back to the
		// clock rather than panic in a tool call.
		return "t" + fmt.Sprintf("%0*x", 6, time.Now().UnixNano()&0xffffff)
	}
	return "t" + hex.EncodeToString(buf)
}
