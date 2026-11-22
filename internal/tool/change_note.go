package tool

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// A description is the only record of WHY a change happened, and a stale
// refusal shows it in place of the caller's intent. Its reader is an agent
// catching up on a diff, so it has to stay a few lines: a paragraph pushes the
// diff the reader actually needs out of view. The cap is enforced here rather
// than in the schema alone, because "required" is advice the caller can
// overshoot; cutting the note costs the caller nothing, unlike a refusal.
const (
	// descriptionMaxLines and descriptionMaxBytes bound what a description may
	// contribute to a history row and to a stale refusal.
	descriptionMaxLines = 4
	descriptionMaxBytes = 400
)

// generatedNote is the history note recorded for a mutation whose caller sent
// no description. The note is what a stale refusal shows the next agent, so it
// says what landed and, plainly, that the intent is missing — a reader can
// still tell this row apart from one the author explained.
//
// It exists because the refusal it replaced cost the caller its whole payload:
// a write is the entire file body and an edit is every hunk, so making the
// model resend it to add one sentence was the most expensive way to collect a
// description. The description is still asked for in the schema; it is just no
// longer enforced by throwing the change away.
func generatedNote(tool, displayPath string, hunks int) string {
	if tool == "edit" {
		return fmt.Sprintf("edit %s — %d hunk(s), no description sent", displayPath, hunks)
	}
	return fmt.Sprintf("write %s — no description sent", displayPath)
}

// noDescriptionNote closes a result whose call carried no description: the
// change landed, so nothing needs retrying, but the caller is told what the
// history is missing and how to fill it in next time.
func noDescriptionNote(tool string) string {
	return fmt.Sprintf(" — no description was sent, so this %s is recorded with a generated note. Send \"description\" next time: three or four lines at most, saying what this change does and why.", tool)
}

// clampDescription trims a description and cuts an over-long one down to the
// few lines the history row and a stale refusal can show. The cut is marked
// with an ellipsis so a reader can tell the text was shortened rather than
// written that way, and it never splits a rune.
func clampDescription(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	clipped := false
	if lines := strings.Split(s, "\n"); len(lines) > descriptionMaxLines {
		s = strings.Join(lines[:descriptionMaxLines], "\n")
		clipped = true
	}
	if len(s) > descriptionMaxBytes {
		cut := descriptionMaxBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
		clipped = true
	}
	if !clipped {
		return s
	}
	return strings.TrimRight(s, " \t\n") + " …"
}
