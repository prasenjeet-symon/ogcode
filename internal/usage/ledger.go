// Package usage keeps the ledger of model spend and prices it.
//
// Every agent step and every utility call (title, risk check, compaction,
// turn-memory summary, deep-search ranking and synthesis) is written to one
// table in the global config DB, tagged with the project, session, provider and
// model it ran on. That is what lets a single view total spend across every
// project and model: each project keeps its sessions in its own DB, so the
// sessions alone could never answer "what have I spent this month".
//
// Prices are never stored. A row is priced when it is read, from the provider's
// own listing or the model catalogue, so a price correction reaches history too.
package usage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// Kinds of ledger row.
const (
	// KindStep is one agent step, keyed by its assistant message id.
	KindStep = "step"
	// KindUtility is one utility call made on a session's behalf.
	KindUtility = "utility"
)

// Ledger writes model spend to the global config DB for one project. A nil
// *Ledger records nothing, so a caller without a global DB (tests, one-off
// tools) needs no guard. Recording never fails a turn: errors are logged.
type Ledger struct {
	db      *db.DB
	project string
	// hosts names the endpoint host a provider id is calling now (see
	// provider.EndpointHost), recorded with each row. nil records no host.
	hosts func(providerID string) string
}

// NewLedger returns a ledger that records into global under project, the
// workspace directory the spend happened in. It returns nil when there is no
// global DB.
func NewLedger(global *db.DB, project string) *Ledger {
	if global == nil {
		return nil
	}
	return &Ledger{db: global, project: project}
}

// SetHosts installs the lookup that names the host a provider calls, so each
// row records where the work actually ran. Set it before the first turn.
func (l *Ledger) SetHosts(hosts func(providerID string) string) {
	if l != nil {
		l.hosts = hosts
	}
}

func (l *Ledger) hostOf(providerID string) string {
	if l.hosts == nil || providerID == "" {
		return ""
	}
	return l.hosts(providerID)
}

// DB is the global database the ledger writes to.
func (l *Ledger) DB() *db.DB {
	if l == nil {
		return nil
	}
	return l.db
}

const upsertStep = `INSERT INTO usage_event
	(id, kind, project, session_id, provider, model, input, output, reasoning, cache_read, cache_write, time_created, host)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		provider = excluded.provider, model = excluded.model, host = excluded.host,
		input = excluded.input, output = excluded.output, reasoning = excluded.reasoning,
		cache_read = excluded.cache_read, cache_write = excluded.cache_write`

const insertRow = `INSERT OR IGNORE INTO usage_event
	(id, kind, project, session_id, provider, model, input, output, reasoning, cache_read, cache_write, time_created, host)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// RecordStep writes one agent step's usage under its assistant message id. A
// step recorded again replaces its row rather than adding a second one, so a
// step's final counts are what stays.
func (l *Ledger) RecordStep(sessionID, messageID, providerID, modelID string, tc session.TokenCounts, at int64) {
	if l == nil || messageID == "" || tc.Consumed() == 0 {
		return
	}
	if _, err := l.db.Exec(upsertStep,
		messageID, KindStep, l.project, sessionID, providerID, modelID,
		tc.Input, tc.Output, tc.Reasoning, tc.CacheRead, tc.CacheWrite, at, l.hostOf(providerID),
	); err != nil {
		slog.Warn("usage ledger: record step", "session", sessionID, "err", err)
	}
}

// RecordUtility appends one utility call's usage.
func (l *Ledger) RecordUtility(sessionID, providerID, modelID string, tc session.TokenCounts, at int64) {
	if l == nil || tc.Consumed() == 0 {
		return
	}
	if _, err := l.db.Exec(insertRow,
		"u_"+randomID(), KindUtility, l.project, sessionID, providerID, modelID,
		tc.Input, tc.Output, tc.Reasoning, tc.CacheRead, tc.CacheWrite, at, l.hostOf(providerID),
	); err != nil {
		slog.Warn("usage ledger: record utility", "session", sessionID, "err", err)
	}
}

// Backfill copies the project's usage from before the ledger existed, once per
// project: every assistant step that reported usage, at the model its message
// names or else its session's, and each session's utility total as one row at
// the session's model (the calls behind it were never itemised). Rows already
// in the ledger are left alone, so running it twice, or from two servers on the
// same project at once, adds nothing. Usage from sessions deleted before the
// ledger existed is gone and stays uncounted.
func (l *Ledger) Backfill(store *session.Store) (steps, utility int, err error) {
	if l == nil || store == nil {
		return 0, 0, nil
	}
	var done int
	switch err := l.db.QueryRow(`SELECT 1 FROM usage_backfill WHERE project = ?`, l.project).Scan(&done); {
	case err == nil:
		return 0, 0, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, 0, fmt.Errorf("check backfill: %w", err)
	}

	sessions, err := store.ListAll()
	if err != nil {
		return 0, 0, fmt.Errorf("list sessions: %w", err)
	}
	bySession := make(map[session.SessionID]*session.Session, len(sessions))
	for _, s := range sessions {
		bySession[s.ID] = s
	}
	rows, err := store.ListStepUsage("")
	if err != nil {
		return 0, 0, err
	}

	tx, err := l.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("begin backfill: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	stmt, err := tx.Prepare(insertRow)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare backfill: %w", err)
	}
	defer stmt.Close()

	for _, r := range rows {
		providerID, modelID := r.Provider, r.Model
		if s := bySession[r.SessionID]; s != nil && modelID == "" {
			providerID, modelID = s.Provider, s.Model
		}
		t := r.Tokens
		// The host these steps ran on was never recorded; the summary falls back
		// to where the provider points now.
		if _, err := stmt.Exec(string(r.MessageID), KindStep, l.project, string(r.SessionID), providerID, modelID,
			t.Input, t.Output, t.Reasoning, t.CacheRead, t.CacheWrite, r.CreatedAt, ""); err != nil {
			return 0, 0, fmt.Errorf("backfill step: %w", err)
		}
		steps++
	}
	for _, s := range sessions {
		u := s.UtilityTokens
		if u == nil || u.Consumed() == 0 {
			continue
		}
		if _, err := stmt.Exec("backfill:"+string(s.ID), KindUtility, l.project, string(s.ID), s.Provider, s.Model,
			u.Input, u.Output, u.Reasoning, u.CacheRead, u.CacheWrite, s.UpdatedAt, ""); err != nil {
			return 0, 0, fmt.Errorf("backfill utility: %w", err)
		}
		utility++
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO usage_backfill (project, time_done) VALUES (?, ?)`,
		l.project, session.Now()); err != nil {
		return 0, 0, fmt.Errorf("mark backfill: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit backfill: %w", err)
	}
	return steps, utility, nil
}

// randomID is a 16-byte hex id for rows that have no natural key.
func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
