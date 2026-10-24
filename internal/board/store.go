package board

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// Store is one project directory's board. Every query is scoped to that
// directory, so a server in one project never sees another's agents.
type Store struct {
	database *db.DB
	dir      string

	mu   sync.Mutex
	wait chan struct{}
}

// Open returns the board for dir. The schema is created by migration 065 and
// reshaped by 067, which run when the project database opens.
func Open(database *db.DB, dir string) *Store {
	return &Store{database: database, dir: dir, wait: make(chan struct{})}
}

// Dir is the project directory this board covers.
func (s *Store) Dir() string { return s.dir }

// signal wakes every waiter. A closed channel is a broadcast, so the channel is
// replaced rather than sent on: one status change wakes all waiters, and a
// waiter that arrives afterwards waits on the new channel and still sees the
// change in its next read.
func (s *Store) signal() {
	s.mu.Lock()
	close(s.wait)
	s.wait = make(chan struct{})
	s.mu.Unlock()
}

func (s *Store) waitCh() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wait
}

// Register joins a handle to the board, or refreshes the row of one that is
// already there. Re-registering keeps the row's id, creation time and reported
// stamp: the tree edge and the fact that a completion was already reported both
// outlive a restart.
func (s *Store) Register(ctx context.Context, h Handle) (Handle, error) {
	if err := ValidateHandle(h.Handle); err != nil {
		return Handle{}, err
	}
	kind := h.Kind
	if kind == "" {
		kind = KindSession
	}
	status := h.Status
	if status == "" {
		status = StatusIdle
	}
	id := h.ID
	if id == "" {
		id = newID()
	}
	now := time.Now().Unix()
	_, err := s.database.ExecContext(ctx,
		`INSERT INTO board_agent (id, directory, handle, session_id, role, kind, spawned_by, status, notified_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		 ON CONFLICT(directory, handle) DO UPDATE SET
		   session_id = excluded.session_id,
		   role       = excluded.role,
		   kind       = excluded.kind,
		   spawned_by = excluded.spawned_by,
		   status     = excluded.status,
		   updated_at = excluded.updated_at`,
		id, s.dir, h.Handle, h.SessionID, h.Role, kind, h.SpawnedBy, status, now, now,
	)
	if err != nil {
		return Handle{}, fmt.Errorf("register handle %s: %w", h.Handle, err)
	}
	h, err = s.Get(ctx, h.Handle)
	if err != nil {
		return Handle{}, err
	}
	s.signal()
	return h, nil
}

// ErrHandleTaken reports that a handle is already on the board: RegisterFresh
// never reuses or overwrites a row, so a fresh registration of a taken name
// comes back with this instead of an upsert.
var ErrHandleTaken = errors.New("handle already registered")

// RegisterFresh joins a handle to the board only if nobody holds its name. It
// is an insert, not the upsert Register is: a fresh registration must never
// overwrite an existing row, whose session keeps running and whose eventual
// report would land on the newcomer's row instead. The check and the insert
// share the store mutex, so a second registration of one name in this process
// waits and then sees the first one's row rather than racing it; the UNIQUE
// keys on board_agent still stand behind it, and a UNIQUE violation the driver
// reports anyway — another process got there first — is mapped to
// ErrHandleTaken.
func (s *Store) RegisterFresh(ctx context.Context, h Handle) (Handle, error) {
	if err := ValidateHandle(h.Handle); err != nil {
		return Handle{}, err
	}
	kind := h.Kind
	if kind == "" {
		kind = KindSession
	}
	status := h.Status
	if status == "" {
		status = StatusIdle
	}

	s.mu.Lock()
	existing, gerr := s.Get(ctx, h.Handle)
	if gerr != nil {
		s.mu.Unlock()
		return Handle{}, gerr
	}
	if existing.Handle != "" {
		s.mu.Unlock()
		return Handle{}, fmt.Errorf("register handle %s: %w", h.Handle, ErrHandleTaken)
	}
	id := h.ID
	if id == "" {
		id = newID()
	}
	now := time.Now().Unix()
	_, werr := s.database.ExecContext(ctx,
		`INSERT INTO board_agent (id, directory, handle, session_id, role, kind, spawned_by, status, notified_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		id, s.dir, h.Handle, h.SessionID, h.Role, kind, h.SpawnedBy, status, now, now,
	)
	s.mu.Unlock()
	if werr != nil {
		if strings.Contains(werr.Error(), "UNIQUE constraint failed") {
			return Handle{}, fmt.Errorf("register handle %s: %w", h.Handle, ErrHandleTaken)
		}
		return Handle{}, fmt.Errorf("register handle %s: %w", h.Handle, werr)
	}
	h, gerr = s.Get(ctx, h.Handle)
	if gerr != nil {
		return Handle{}, gerr
	}
	s.signal()
	return h, nil
}

// Get returns one handle, or the zero Handle when it is not on the board.
func (s *Store) Get(ctx context.Context, handle string) (Handle, error) {
	row := s.database.QueryRowContext(ctx,
		`SELECT id, directory, handle, session_id, role, kind, spawned_by, status, notified_at, created_at, updated_at
		 FROM board_agent WHERE directory = ? AND handle = ?`, s.dir, handle)
	h, err := scanHandle(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Handle{}, nil
	}
	if err != nil {
		return Handle{}, fmt.Errorf("get handle %s: %w", handle, err)
	}
	return h, nil
}

// BySession returns the handle a session is registered as, or the zero Handle.
func (s *Store) BySession(ctx context.Context, sessionID string) (Handle, error) {
	if sessionID == "" {
		return Handle{}, nil
	}
	row := s.database.QueryRowContext(ctx,
		`SELECT id, directory, handle, session_id, role, kind, spawned_by, status, notified_at, created_at, updated_at
		 FROM board_agent WHERE directory = ? AND session_id = ?`, s.dir, sessionID)
	h, err := scanHandle(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Handle{}, nil
	}
	if err != nil {
		return Handle{}, fmt.Errorf("get handle for session %s: %w", sessionID, err)
	}
	return h, nil
}

// SetStatus records what an agent is doing, which is what the tree shows. A
// status change is the board's only event, so it wakes any waiter.
func (s *Store) SetStatus(ctx context.Context, handle, status string) error {
	if _, err := s.database.ExecContext(ctx,
		`UPDATE board_agent SET status = ?, updated_at = ? WHERE directory = ? AND handle = ?`,
		status, time.Now().Unix(), s.dir, handle); err != nil {
		return fmt.Errorf("set status of %s: %w", handle, err)
	}
	s.signal()
	return nil
}

// SetStatusUnlessTerminal records a status the way SetStatus does, but only
// while the row is not terminal: done, stopped and stale are final, and the
// spawner is owed exactly one report about them. It reports whether the row
// moved, which is also whether the caller still owns that report.
//
// The condition lives in the UPDATE rather than in a read-then-write above it:
// stopping a teammate and the teammate's loop winding up are two goroutines
// that race by design, and whichever writes second must not overwrite the
// other. A stop that lands first turns the loop's own completion into a no-op;
// a completion that lands first leaves the stop nothing to do.
func (s *Store) SetStatusUnlessTerminal(ctx context.Context, handle, status string) (bool, error) {
	res, err := s.database.ExecContext(ctx,
		`UPDATE board_agent SET status = ?, updated_at = ?
		 WHERE directory = ? AND handle = ? AND status NOT IN (?, ?, ?)`,
		status, time.Now().Unix(), s.dir, handle,
		StatusDone, StatusStopped, StatusStale)
	if err != nil {
		return false, fmt.Errorf("set status of %s: %w", handle, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("set status of %s: %w", handle, err)
	}
	if n > 0 {
		s.signal()
	}
	return n > 0, nil
}

// Roster returns every handle on the board, in join order.
func (s *Store) Roster(ctx context.Context) ([]Handle, error) {
	rows, err := s.database.QueryContext(ctx,
		`SELECT id, directory, handle, session_id, role, kind, spawned_by, status, notified_at, created_at, updated_at
		 FROM board_agent WHERE directory = ? ORDER BY created_at ASC, handle ASC`, s.dir)
	if err != nil {
		return nil, fmt.Errorf("list roster: %w", err)
	}
	defer rows.Close()

	handles := []Handle{}
	for rows.Next() {
		h, err := scanHandle(rows)
		if err != nil {
			return nil, fmt.Errorf("scan handle: %w", err)
		}
		handles = append(handles, h)
	}
	return handles, rows.Err()
}

// Children returns the handles spawned by parent, in join order.
func (s *Store) Children(ctx context.Context, parent string) ([]Handle, error) {
	if parent == "" {
		return nil, nil
	}
	rows, err := s.database.QueryContext(ctx,
		`SELECT id, directory, handle, session_id, role, kind, spawned_by, status, notified_at, created_at, updated_at
		 FROM board_agent WHERE directory = ? AND spawned_by = ? ORDER BY created_at ASC, handle ASC`, s.dir, parent)
	if err != nil {
		return nil, fmt.Errorf("list children of %s: %w", parent, err)
	}
	defer rows.Close()

	handles := []Handle{}
	for rows.Next() {
		h, err := scanHandle(rows)
		if err != nil {
			return nil, fmt.Errorf("scan handle: %w", err)
		}
		handles = append(handles, h)
	}
	return handles, rows.Err()
}

// MarkReported records that the spawner has been told these handles' current
// status, by moving each row's notified_at up to its updated_at. Stamping the
// status time rather than the wall clock keeps "reported" and "changed since"
// comparable even when both land in the same second.
func (s *Store) MarkReported(ctx context.Context, handles []string) error {
	for _, handle := range handles {
		if _, err := s.database.ExecContext(ctx,
			`UPDATE board_agent SET notified_at = updated_at WHERE directory = ? AND handle = ?`,
			s.dir, handle); err != nil {
			return fmt.Errorf("mark %s reported: %w", handle, err)
		}
	}
	return nil
}

// WaitStatus blocks until every named handle is terminal (or gone from the
// roster) or the timeout passes, and returns the rows it watched. It re-reads
// on every wake, so a handle that finishes between two checks is never missed.
func (s *Store) WaitStatus(ctx context.Context, handles []string, timeout time.Duration) ([]Handle, error) {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		found, allTerminal, err := s.readStatus(ctx, handles)
		if err != nil {
			return nil, err
		}
		if allTerminal {
			return found, nil
		}
		wait := s.waitCh()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wait:
		case <-tick.C:
		case <-time.After(time.Until(deadline)):
			found, _, err := s.readStatus(ctx, handles)
			return found, err
		}
	}
}

// readStatus reads every named handle and reports whether all of them are
// terminal. A handle that is not on the roster counts as terminal: there is
// nothing left to wait for.
func (s *Store) readStatus(ctx context.Context, handles []string) ([]Handle, bool, error) {
	found := make([]Handle, 0, len(handles))
	allTerminal := true
	for _, handle := range handles {
		h, err := s.Get(ctx, handle)
		if err != nil {
			return nil, false, err
		}
		if h.Handle == "" {
			continue
		}
		found = append(found, h)
		if !h.Terminal() {
			allTerminal = false
		}
	}
	return found, allTerminal, nil
}

// Reconcile marks handles whose session no longer exists as stale, so the tree
// does not advertise agents from a previous run as running. live is the set of
// session ids that still exist in the project.
func (s *Store) Reconcile(ctx context.Context, live map[string]bool) error {
	roster, err := s.Roster(ctx)
	if err != nil {
		return err
	}
	for _, h := range roster {
		if h.Terminal() {
			continue
		}
		if h.SessionID != "" && live[h.SessionID] {
			continue
		}
		if err := s.SetStatus(ctx, h.Handle, StatusStale); err != nil {
			return err
		}
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanHandle(row rowScanner) (Handle, error) {
	var (
		h                          Handle
		notified, created, updated int64
	)
	err := row.Scan(&h.ID, &h.Directory, &h.Handle, &h.SessionID, &h.Role, &h.Kind, &h.SpawnedBy, &h.Status, &notified, &created, &updated)
	if err != nil {
		return Handle{}, err
	}
	h.notifiedAt = time.Unix(notified, 0)
	h.CreatedAt = time.Unix(created, 0)
	h.UpdatedAt = time.Unix(updated, 0)
	return h, nil
}
