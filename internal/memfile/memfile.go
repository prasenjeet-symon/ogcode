// Package memfile is ogcode's per-turn markdown memory: after a turn completes,
// a structured summary of that turn is written as one dated markdown file under
// the project's .ogcode/memory/, and an incremental index of that folder lets a
// read-only recall agent find and read the right file cheaply.
//
// It replaces the removed graph/embedding agentic-memory system. The whole
// subsystem is gated by TurnMemoryEnabled (env
// OGCODE_TURN_MEMORY): off by default, so a build with these types linked in
// behaves exactly as before until the flag is set.
package memfile

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// subDir is the folder under a project's .ogcode/ that holds turn summaries. It
// sits beside notes/ and archives/, so a project's memory is inspectable and
// version-controllable like any other .ogcode artifact.
const subDir = "memory"

// TurnMemoryEnabled reports whether the per-turn markdown memory subsystem is
// switched on. It is the project's memory by default — a single process-wide env
// gate turns it OFF (OGCODE_TURN_MEMORY=0/false/off) for the rare case that
// wants the legacy graph route or no memory at all. Any other value, or an unset
// variable, leaves it on. It is deliberately env-only rather than a settings-DB
// field so memory does not depend on the UI toggle.
func TurnMemoryEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OGCODE_TURN_MEMORY"))) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// Meta is the identifying context for one turn summary, carried through both the
// file's frontmatter and the index row so recall can scope and attribute a
// summary without opening its body.
type Meta struct {
	SessionID   string
	ProjectID   string
	SessionType string
	Title       string
	CreatedAt   time.Time
}

// MemoryDir returns the absolute path to a project's turn-summary folder. It
// does not create the directory — Write does that on demand.
func MemoryDir(projectDir string) string {
	return filepath.Join(projectDir, ".ogcode", subDir)
}

// Filename builds a turn summary's filename. The UTC timestamp leads so a
// lexical sort of the folder is chronological; a session tag groups a
// conversation's turns and lets session-scoped recall filter by name; the slug
// makes the file recognisable at a glance. Example:
//
//	2026-09-09T143005Z--ses01M269J8TM53--wire-the-recall-agent.md
func Filename(createdAt time.Time, sessionID, title string) string {
	ts := createdAt.UTC().Format("2006-01-02T150405Z")
	tag := sessionTag(sessionID)
	slug := slugify(title)
	if slug == "" {
		slug = "turn"
	}
	return ts + "--" + tag + "--" + slug + ".md"
}

// tagLen is how many characters of a session key a file name's tag keeps. For
// ogcode's "ses_" + ULID ids that is the prefix, the whole millisecond
// timestamp and two characters of randomness, so two conversations share a tag
// only if they began in the same millisecond and drew the same ten random bits.
// It was 8 — the prefix and the first five timestamp characters, which change
// only every 2.3 hours — so every conversation started in the same afternoon
// shared one.
const tagLen = 15

// sessionTag reduces a session id to a filename-safe grouping token: the first
// tagLen characters of its key.
func sessionTag(sessionID string) string {
	key := SessionKey(sessionID)
	if len(key) > tagLen {
		key = key[:tagLen]
	}
	return key
}

// SessionKey is a session id reduced to its ASCII letters and digits:
// "ses_01M269J8TM53…" becomes "ses01M269J8TM53…". Every tag for a session — the
// one in a summary's file name, the shorter one memory_map shows — is a prefix
// of its key, which is what lets a tag be resolved back to the one
// conversation it names. "session" for an id with no such characters.
func SessionKey(sessionID string) string {
	var b strings.Builder
	for _, r := range sessionID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

// SessionTag is the exported form of the grouping token Filename embeds in a
// summary's name: the tag one conversation's turns share. memory_map groups by
// the full SessionKey instead, and shows the shortest prefix of it that is
// unique among the conversations it lists.
func SessionTag(sessionID string) string {
	return sessionTag(sessionID)
}

// slugify turns a human title into a lowercase, dash-separated slug capped at a
// length that keeps the whole filename comfortably within filesystem limits.
func slugify(title string) string {
	const maxLen = 50
	var b strings.Builder
	lastDash := true // leading state: suppress a leading dash
	for _, r := range strings.ToLower(title) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= maxLen {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
