package tool

import (
	"fmt"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/filechange"
)

// staleRefusal is what a mutation returns instead of writing when another
// session changed the file after this one last read it. Nothing has been
// written: the refusal IS the catch-up, so it has to carry everything the
// caller needs to redo the change on current content — which file, that
// nothing happened, how far behind the bookmark is, and every change in the
// window with who made it, what they said they were doing and what the diff
// was.
//
// It is a Result rather than an error, like the board-claim refusal: the model
// reads the reason and acts on it, where an error reads as a tool failure
// worth retrying unchanged.
func staleRefusal(displayPath string, log *filechange.Log, seen, rev int64, changes []filechange.Change, dropped int) Result {
	var b strings.Builder
	fmt.Fprintf(&b, "File changed by another agent since you read it: %s\n\n", displayPath)
	b.WriteString("Nothing was written. Your edits were NOT applied.\n\n")
	fmt.Fprintf(&b, "Your last read was revision %d. The file is now at revision %d.\n\n", seen, rev)

	for i, c := range changes {
		fmt.Fprintf(&b, "[%d] rev %d, %s, %s (%s)\n",
			i+1, c.Rev, c.At.Local().Format("15:04:05"), changeAuthor(log, c), c.Tool)
		if note := strings.TrimSpace(c.Note); note != "" {
			fmt.Fprintf(&b, "    note: %s\n", strings.ReplaceAll(note, "\n", "\n    "))
		}
		// The diff already carries its own marker when it was capped (in the
		// ring, in the row and here alike), so it is rendered as stored. An
		// omitted diff is the other case: a file too large, or no baseline.
		if diff := strings.TrimRight(c.Diff, "\n"); diff != "" {
			fmt.Fprintf(&b, "    %s\n", strings.ReplaceAll(diff, "\n", "\n    "))
		} else if c.DiffOmitted {
			b.WriteString("    (diff omitted)\n")
		}
		b.WriteString("\n")
	}

	if dropped > 0 {
		fmt.Fprintf(&b, "%d earlier change(s) are not listed — the log keeps the most recent %d per file.\n\n",
			dropped, filechange.RingPerFile)
	}

	b.WriteString("Re-read the file (read with ranges) and re-apply your change against the current content.")

	return Result{
		Title:  "File changed by another agent",
		Output: b.String(),
	}
}

// changeAuthor names the session that made a change: the board handle when the
// server resolved one, the session id otherwise, so a refusal can always say
// who to talk to. The agent id ("build", "task") is deliberately not shown:
// the handle is the name the other sessions know it by.
func changeAuthor(log *filechange.Log, c filechange.Change) string {
	if c.Session == "" {
		return "an unattributed session"
	}
	if log != nil && log.Name != nil {
		if name := strings.TrimSpace(log.Name(c.Session)); name != "" {
			return name
		}
	}
	return string(c.Session)
}
