package session

import (
	"errors"
	"fmt"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrSessionDeleted marks a store write rejected because the session — or the
// message the write belongs to — was deleted underneath the caller. Deleting a
// session cascades to its messages and parts, so a running agent loop's next
// INSERT fails the foreign-key constraint instead of finding its parent.
var ErrSessionDeleted = errors.New("session deleted")

// markDeleted turns a foreign-key violation into ErrSessionDeleted. The
// schema's only foreign keys hang off session and message (both ON DELETE
// CASCADE), so a violation on a message or part insert means one of those rows
// is gone and the caller's transcript can no longer be written.
func markDeleted(err error) error {
	if err == nil || !isForeignKeyViolation(err) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrSessionDeleted, err)
}

// isForeignKeyViolation reports whether err is SQLite's extended foreign-key
// constraint code (787). The modernc driver returns *sqlite.Error; database/sql
// passes it through unchanged, so errors.As finds it even when wrapped.
func isForeignKeyViolation(err error) bool {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return false
	}
	return se.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
}
