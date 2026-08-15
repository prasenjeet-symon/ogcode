package tool

import (
	"fmt"
	"strings"
)

// Tool-output size caps. Every tool result that would otherwise flood the model
// context is truncated to these limits. The values mirror opencode's shared
// truncation service (50 KB / 2000 lines): a single large tool output (a build
// log, a big file, a verbose test run) is the dominant source of wasted tokens
// in an agentic loop, because it is re-sent on every subsequent step of the turn
// until compaction. Capping it here bounds that cost at the source.
const (
	// MaxToolOutputBytes caps the byte length of any single tool result.
	MaxToolOutputBytes = 50 * 1024 // 50 KB
	// MaxToolOutputLines caps the line count of any single tool result.
	MaxToolOutputLines = 2000
	// MaxLineLength caps one very long line (e.g. minified JS, a base64 blob) so
	// a single line cannot consume the whole budget on its own.
	MaxLineLength = 2000
)

// TruncateDirection selects which end of the output to keep when it exceeds the
// caps. Most tools keep the head — the start of a file or listing is usually the
// useful part. Shell keeps the tail, because the end of a build/test log is
// where the error and summary live.
type TruncateDirection int

const (
	KeepHead TruncateDirection = iota
	KeepTail
)

// lineTruncatedSuffix is appended to any individual line trimmed to MaxLineLength.
const lineTruncatedSuffix = "… (line truncated)"

// TruncateOutput caps s to MaxToolOutputLines and MaxToolOutputBytes, keeping the
// requested end, and inserts a one-line notice describing what was dropped. It
// returns the (possibly unchanged) text and whether any truncation happened.
//
// It is the shared backstop applied to every tool result that does not cap its
// own output. Tools that truncate themselves (read, bash, grep, glob) set
// Result.Truncated so the agent loop skips this pass.
func TruncateOutput(s string, dir TruncateDirection) (string, bool) {
	lines := strings.Split(s, "\n")

	// 1. Per-line cap: trim pathologically long single lines.
	lineCut := false
	for i, ln := range lines {
		if len(ln) > MaxLineLength {
			lines[i] = ln[:MaxLineLength] + lineTruncatedSuffix
			lineCut = true
		}
	}

	// 2. Line-count cap.
	droppedLines := 0
	if len(lines) > MaxToolOutputLines {
		droppedLines = len(lines) - MaxToolOutputLines
		if dir == KeepTail {
			lines = lines[len(lines)-MaxToolOutputLines:]
		} else {
			lines = lines[:MaxToolOutputLines]
		}
	}
	out := strings.Join(lines, "\n")

	// 3. Byte cap. Slicing can land mid-rune, so repair to valid UTF-8 after.
	droppedBytes := 0
	if len(out) > MaxToolOutputBytes {
		droppedBytes = len(out) - MaxToolOutputBytes
		if dir == KeepTail {
			out = out[len(out)-MaxToolOutputBytes:]
		} else {
			out = out[:MaxToolOutputBytes]
		}
		out = strings.ToValidUTF8(out, "")
	}

	if droppedLines == 0 && droppedBytes == 0 && !lineCut {
		return s, false
	}

	notice := truncationNotice(droppedLines, droppedBytes)
	if dir == KeepTail {
		// Keep the end: the notice explains what was dropped from the start.
		return notice + "\n\n" + out, true
	}
	// Keep the start: the notice explains what was dropped from the end.
	return out + "\n\n" + notice, true
}

// truncationNotice renders the human-readable notice inserted into a truncated
// result, summarising how much was dropped and how to see more.
func truncationNotice(droppedLines, droppedBytes int) string {
	var parts []string
	if droppedLines > 0 {
		parts = append(parts, fmt.Sprintf("%d lines", droppedLines))
	}
	if droppedBytes > 0 {
		parts = append(parts, fmt.Sprintf("%d KB", droppedBytes/1024))
	}
	what := strings.Join(parts, " / ")
	if what == "" {
		what = "some content"
	}
	return fmt.Sprintf(
		"[output truncated: %s dropped. Re-run more narrowly (grep, a targeted path, or read with offset/limit) to see the rest.]",
		what,
	)
}
