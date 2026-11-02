package memfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DeepSearchHeading is the section the agent loop writes a line into for every
// deep search a turn ran. Session memory is otherwise the agent's own prose, so
// the heading is exported: the prompt names it and tells the agent the section
// is maintained for it, which is what keeps a rewrite from deleting it.
const DeepSearchHeading = "## Deep searches"

const (
	// deepSearchNoteCap bounds one session's record. The newest notes survive
	// and the oldest drop: the point is to tell a later turn which research
	// already happened, not to keep an exhaustive log of the conversation.
	deepSearchNoteCap = 20
	// deepSearchQueryCap bounds the question quoted in one line, so a query the
	// size of a paragraph cannot turn a one-line note into one.
	deepSearchQueryCap = 160
)

// DeepSearchNote is one deep search a turn ran. Query is the question the agent
// asked (the tool's own query, not the contextualised prompt it sent the search
// agent), Cached says the answer came from the web cache rather than a fresh
// pipeline run, and At is when it happened.
type DeepSearchNote struct {
	Query  string
	Cached bool
	At     time.Time
}

// AppendDeepSearches records notes about the deep searches a turn ran in the
// session memory file for sessionID under projectDir, creating the file and the
// section when neither exists yet. It reports whether the file changed: a query
// already listed is skipped, so a repeated search does not grow the file.
//
// Lines are appended under DeepSearchHeading in the memory file's own style —
// one bullet per search, newest last, oldest dropped past the cap. Everything
// outside that section is left byte-for-byte as it was, which is what makes it
// safe to call on a file the agent rewrites turn after turn.
func AppendDeepSearches(projectDir, sessionID string, notes []DeepSearchNote) (bool, error) {
	if len(notes) == 0 {
		return false, nil
	}
	path := SessionMemoryPath(projectDir, sessionID)

	body := ""
	switch b, err := os.ReadFile(path); {
	case err == nil:
		body = string(b)
	case os.IsNotExist(err):
	default:
		return false, fmt.Errorf("memfile: read session memory: %w", err)
	}

	var lines []string
	if body != "" {
		lines = strings.Split(strings.TrimRight(body, "\n"), "\n")
	}

	head := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == DeepSearchHeading {
			head = i
			break
		}
	}

	var before, section, after []string
	if head >= 0 {
		before = lines[:head]
		end := len(lines)
		for i := head + 1; i < len(lines); i++ {
			t := strings.TrimSpace(lines[i])
			if strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "## ") {
				end = i
				break
			}
		}
		section = lines[head+1 : end]
		after = lines[end:]
	}

	// Bullets are the record; anything else the agent put under the heading is
	// kept ahead of them rather than dropped.
	var listed, extras []string
	for _, line := range section {
		switch {
		case strings.HasPrefix(line, "- "):
			listed = append(listed, line)
		case strings.TrimSpace(line) != "":
			extras = append(extras, line)
		}
	}

	known := strings.Join(listed, "\n")
	var added []string
	for _, n := range notes {
		query := singleLine(n.Query, deepSearchQueryCap)
		if query == "" {
			continue
		}
		marker := `"` + query + `"`
		if strings.Contains(known, marker) {
			continue
		}
		known += "\n" + marker
		at := n.At
		if at.IsZero() {
			at = time.Now()
		}
		source := "live research"
		if n.Cached {
			source = "cached answer"
		}
		added = append(added, fmt.Sprintf("- %s — %s (%s)", at.Format("2006-01-02 15:04"), marker, source))
	}
	if len(added) == 0 {
		return false, nil
	}

	listed = append(listed, added...)
	if len(listed) > deepSearchNoteCap {
		listed = listed[len(listed)-deepSearchNoteCap:]
	}

	out := make([]string, 0, len(lines)+len(added)+4)
	if head < 0 {
		existing := trimBlank(lines)
		if len(existing) == 0 {
			out = append(out, "# Session "+SessionKey(sessionID), "")
		} else {
			out = append(out, existing...)
			out = append(out, "")
		}
	} else {
		// The heading is appended below, so what comes before it must not carry
		// it a second time.
		if rest := trimBlank(before); len(rest) > 0 {
			out = append(out, rest...)
			out = append(out, "")
		}
	}
	out = append(out, DeepSearchHeading, "")
	out = append(out, extras...)
	out = append(out, listed...)
	if rest := trimBlank(after); len(rest) > 0 {
		out = append(out, "")
		out = append(out, rest...)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("memfile: create session memory dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o644); err != nil {
		return false, fmt.Errorf("memfile: write session memory: %w", err)
	}
	return true, nil
}

// singleLine collapses whitespace — a query the model wrote across several
// lines would otherwise break the one-line note — and caps it at max runes.
func singleLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max])) + "…"
	}
	return s
}

// trimBlank drops leading and trailing blank lines.
func trimBlank(lines []string) []string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}
