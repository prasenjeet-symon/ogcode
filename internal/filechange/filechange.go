// Package filechange is the read-bookmark gate over file mutations: what each
// session last read of a file, every change that has landed since, and the
// durable history of those changes.
//
// Two halves, both mirrored in the project database:
//
//   - a bookmark per (file, session) — the revision that session last read it
//     at, in memory for the gate and in file_bookmark so a restart keeps it;
//   - a change log per file — one permanent row per write/edit in file_change,
//     plus a bounded ring in memory that a refusal renders.
//
// The gate lives in Log; the tables in Store. Nothing here imports
// internal/tool: the tools call FileKey to name a path and pass it in as an
// opaque string, exactly as the board treats a handle.
package filechange

import (
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

const (
	// RingPerFile bounds the in-memory history one file keeps, and therefore
	// what a refusal can list. Past this the refusal says the list is
	// truncated and tells the caller to re-read; the table keeps every row.
	RingPerFile = 50
	// NoteMaxBytes caps the caller's description in a row and in the ring, so
	// one verbose description cannot bloat the history.
	NoteMaxBytes = 2 * 1024
	// DiffMaxBytes caps one stored diff. A longer diff sets DiffOmitted, so a
	// reader knows it is looking at a truncated hunk rather than a short one.
	DiffMaxBytes = 8 * 1024
)

// Change is one mutation that landed: who made it, what they said they were
// doing, and the diff it produced.
type Change struct {
	// ID is the row id in file_change, 0 for a change that is only in the
	// ring. It is the cursor the history route pages with.
	ID int64 `json:"id"`
	// Path is the FileKey the gate and the table are keyed on: absolute,
	// cleaned, symlink-resolved (internal/tool.FileKey), never a display path.
	Path string `json:"path"`
	// Directory is the project the row belongs to, filled in by the store.
	Directory string `json:"-"`
	// Rev is the file's revision after this mutation. It only ever increases,
	// so "changed since" is an integer comparison and never a clock one.
	Rev int64 `json:"rev"`
	// Session is the session that made the change; empty for rows written
	// before a caller had one (a bare test).
	Session session.SessionID `json:"session,omitempty"`
	// Agent is the agent id that made it ("build", "task", …).
	Agent string `json:"agent,omitempty"`
	// Tool is "write", "edit", or "bash" for a change a shell command made.
	Tool string `json:"tool"`
	// Note is the caller's description, verbatim.
	Note string `json:"note,omitempty"`
	// At is when it landed. Recorded and shown, never compared.
	At time.Time `json:"at"`
	// Diff is the line diff from before to after; DiffOmitted marks that it
	// was capped.
	Diff        string `json:"diff,omitempty"`
	DiffOmitted bool   `json:"diffOmitted,omitempty"`
}

// Bookmark is the revision one session last read a file at.
type Bookmark struct {
	Path    string
	Session session.SessionID
	Rev     int64
	At      time.Time
}

// capped makes every copy of a change — the ring, the refusal, the row — the
// same text: whatever the caller produced is truncated here, once.
func (c Change) capped() Change {
	if len(c.Note) > NoteMaxBytes {
		c.Note = truncate(c.Note, NoteMaxBytes)
	}
	if len(c.Diff) > DiffMaxBytes {
		c.Diff = truncate(c.Diff, DiffMaxBytes)
		c.DiffOmitted = true
	}
	return c
}

// truncate cuts s to at most n bytes without splitting a rune, and marks the
// cut so a reader can tell.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], "\n") + "\n… (truncated)"
}

// utf8Start reports whether b is not a continuation byte, i.e. a safe place to
// cut a UTF-8 string.
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
