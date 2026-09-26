package permission

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// Mode values for how mutating tool calls are gated. The session row stores
// these same strings in its permission column.
const (
	ModeAsk  = "ask"
	ModeAuto = "auto"
	// ModeYolo runs every call the rules would have prompted for without asking
	// and without a risk classification. It is the deliberate no-guardrails
	// choice; an explicit Deny rule still denies.
	ModeYolo = "yolo"
)

// Store persists the two permission decisions that outlive the session they
// were made in: the "always allow" grants a user gave, and the Ask/Auto/Yolo
// mode a new session starts in.
//
// It reads and writes the GLOBAL config DB (~/.ogcode/config.db), so a decision
// follows the user across every project on the machine — the same scope
// provider credentials and the OGX account have. Both methods tolerate a nil
// store or a nil database handle, so a headless run or a test that constructs
// no store behaves exactly as it did before the store existed.
type Store struct{ db *db.DB }

// NewStore wraps the global config DB. A nil database yields a usable Store
// that persists nothing.
func NewStore(database *db.DB) *Store { return &Store{db: database} }

// Grants returns every stored "always allow" grant, most recent first.
func (s *Store) Grants() (Ruleset, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT tool, pattern, action FROM permission_grant ORDER BY time_created DESC`)
	if err != nil {
		return nil, fmt.Errorf("list permission grants: %w", err)
	}
	defer rows.Close()

	var out Ruleset
	for rows.Next() {
		var r Rule
		var action string
		if err := rows.Scan(&r.Permission, &r.Pattern, &action); err != nil {
			return nil, fmt.Errorf("scan permission grant: %w", err)
		}
		r.Action = Action(action)
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddGrant stores one grant. The same target approved twice is one row rather
// than two, so the table stays a set of decisions rather than a log.
func (s *Store) AddGrant(r Rule) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`
		INSERT INTO permission_grant (tool, pattern, action, time_created)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(tool, pattern) DO UPDATE SET
			action       = excluded.action,
			time_created = excluded.time_created`,
		r.Permission, r.Pattern, string(r.Action), time.Now().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("store permission grant: %w", err)
	}
	return nil
}

// DefaultMode returns the Ask/Auto/Yolo mode new sessions start in. A database
// with no stored choice reads as Ask — the behaviour that predates the choice
// being stored — and so does an unrecognised value, so the safest mode is the
// one a corrupt row falls back to.
func (s *Store) DefaultMode() string {
	if s == nil || s.db == nil {
		return ModeAsk
	}
	var mode string
	err := s.db.QueryRow(`SELECT mode FROM permission_mode WHERE id = 1`).Scan(&mode)
	if err != nil {
		if err != sql.ErrNoRows {
			slog.Warn("failed to read default permission mode; using ask", "err", err)
		}
		return ModeAsk
	}
	switch mode {
	case ModeAuto:
		return ModeAuto
	case ModeYolo:
		return ModeYolo
	default:
		return ModeAsk
	}
}

// SetDefaultMode records the mode new sessions will start in. "Last toggle
// wins": whichever way the user last flipped the switch in any session is what
// the next new session inherits.
func (s *Store) SetDefaultMode(mode string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if mode != ModeAuto && mode != ModeYolo {
		mode = ModeAsk
	}
	_, err := s.db.Exec(`
		INSERT INTO permission_mode (id, mode, time_updated)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			mode         = excluded.mode,
			time_updated = excluded.time_updated`,
		mode, time.Now().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("store default permission mode: %w", err)
	}
	return nil
}
