package filechange

import (
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines a hunk shows around the change. Three
// is the usual amount and enough to tell where in the file the change landed
// without paying for the whole neighbourhood on every refusal.
const diffContext = 3

// diffMaxChangedLines bounds how many changed lines one mutation shows. A
// refusal is read by a model mid-task, so a thousand-line rewrite should say
// "this whole region changed", not paste the region: the model re-reads the
// file either way.
const diffMaxChangedLines = 64

// DiffLines renders a bounded, unified-style diff of one edit: a single hunk
// between the common prefix and suffix, context lines around it, and one
// header. The bool reports that the change was too large to show in full.
//
// It is not a general-purpose diff. One mutation at a time, one hunk, no
// splitting: the caller already knows the two versions of one file, and every
// consumer (refusal, ring, stored row) shows the same truncated text.
func DiffLines(oldText, newText string) (string, bool) {
	a, b := splitLines(oldText), splitLines(newText)

	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix &&
		a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	if prefix == len(a) && prefix == len(b) {
		if oldText != newText {
			// Only the final newline differs: no line changed, but the
			// bytes did, and reporting an empty diff would say nothing
			// was different.
			return "… (only the trailing newline changed)\n", false
		}
		return "", false
	}

	removed := a[prefix : len(a)-suffix]
	added := b[prefix : len(b)-suffix]

	// Cap the changed lines shown, removals first: the lines that went away
	// explain the conflict better than the ones that replaced them.
	truncated := false
	budget := diffMaxChangedLines
	if len(removed) > budget {
		removed, truncated = removed[:budget], true
	}
	budget -= len(removed)
	if len(added) > budget {
		added, truncated = added[:budget], true
	}

	before := prefix
	if before > diffContext {
		before = diffContext
	}
	after := suffix
	if after > diffContext {
		after = diffContext
	}
	start := prefix - before + 1

	var out strings.Builder
	fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n",
		start, before+len(removed)+after, start, before+len(added)+after)
	for _, line := range a[prefix-before : prefix] {
		out.WriteString(" " + line + "\n")
	}
	for _, line := range removed {
		out.WriteString("- " + line + "\n")
	}
	for _, line := range added {
		out.WriteString("+ " + line + "\n")
	}
	for _, line := range a[len(a)-after:] {
		out.WriteString(" " + line + "\n")
	}
	if truncated {
		out.WriteString("… (diff truncated)\n")
	}
	return out.String(), truncated
}

// splitLines splits text into lines. An empty file has none, and a trailing
// newline is not a line of its own, so a file's diff lines match the numbers an
// editor shows.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
