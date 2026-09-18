package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type EditTool struct{}

func (EditTool) ID() string { return "edit" }
func (EditTool) Description() string {
	return "Make one or more search-and-replace edits to a file. Each old_string must match exactly one place unless replace_all is set, so include enough surrounding context to identify the occurrence you mean. Several edits to one file belong in a single call via \"edits\": they apply together or not at all, so a failure part-way cannot leave the file half-changed. The result reports any syntax error the edits introduced, so a broken edit surfaces immediately rather than at the next build."
}

func (EditTool) Parameters() json.RawMessage {
	// The uniqueness rule is stated here, in the schema, because it is the one
	// part of this tool's contract a caller cannot infer from the file: a block
	// that looks unique in the region being read may repeat elsewhere. Left
	// undocumented, the rule is discovered only by tripping over it, which costs
	// a failed call on exactly the repetitive files where edits matter most.
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path to edit"},
			"old_string": {"type": "string", "description": "Exact text to find, including indentation. Must match EXACTLY ONE place in the file unless replace_all is true. When a block repeats — the same step in several CI jobs, the same line in several functions — extend it with neighbouring lines until only the intended occurrence matches."},
			"new_string": {"type": "string", "description": "Text to replace it with. Always send it, even when replacing with nothing: \"\" means delete old_string, and omitting the field is rejected rather than read as a deletion."},
			"replace_all": {"type": "boolean", "description": "Replace every occurrence instead of requiring a unique match. Use when the text genuinely should change everywhere, such as removing one repeated step from every job in a workflow. The result reports how many were replaced."},
			"expected_count": {"type": "integer", "description": "Optional assertion: fail unless old_string matches exactly this many times. Worth setting alongside replace_all, where a miscounted anchor would otherwise rewrite more of the file than intended."},
			"edits": {
				"type": "array",
				"description": "Several edits to this one file, applied in order and ALL-OR-NOTHING: every one is checked first, and if any fails the file is left untouched. Prefer this over separate edit calls to the same path — it is one round trip and cannot half-apply a refactor. Each entry takes the same old_string / new_string / replace_all / expected_count as above. Later entries match against the result of earlier ones.",
				"items": {
					"type": "object",
					"properties": {
						"old_string": {"type": "string"},
						"new_string": {"type": "string"},
						"replace_all": {"type": "boolean"},
						"expected_count": {"type": "integer"}
					},
					"required": ["old_string", "new_string"]
				}
			}
		},
		"required": ["path"]
	}`)
}

// editHunk is one search-and-replace as it arrives on the wire.
//
// NewString is a pointer so that an omitted field is distinguishable from an
// explicitly empty one. The distinction is not pedantic: "" is a deletion,
// which is legitimate and must keep working, while a MISSING new_string is a
// caller that forgot the replacement — and treating that as "" deletes their
// code and reports success. Nothing upstream catches it either; DecodeArgs is a
// plain json.Unmarshal, so the schema's "required" is advice to the model
// rather than a check, and a missing field simply arrives as the zero value.
type editHunk struct {
	OldString     string  `json:"old_string"`
	NewString     *string `json:"new_string"`
	ReplaceAll    bool    `json:"replace_all"`
	ExpectedCount int     `json:"expected_count"`
}

// resolvedHunk is a hunk that has passed validation, with its replacement text
// settled so the apply path never has to ask whether it was supplied.
type resolvedHunk struct {
	oldStr, newStr string
	replaceAll     bool
	expectedCount  int
}

func (EditTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Path          string     `json:"path"`
		OldString     string     `json:"old_string"`
		NewString     *string    `json:"new_string"`
		ReplaceAll    bool       `json:"replace_all"`
		ExpectedCount int        `json:"expected_count"`
		Edits         []editHunk `json:"edits"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	hunks, err := collectHunks(input.Edits, editHunk{
		OldString:     input.OldString,
		NewString:     input.NewString,
		ReplaceAll:    input.ReplaceAll,
		ExpectedCount: input.ExpectedCount,
	})
	if err != nil {
		return Result{}, err
	}

	path := input.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	// Serialize the read-modify-write with any concurrent write/edit to the same
	// file. The agent loop runs a turn's tool calls in parallel; without this an
	// interleaved write could make edit operate on stale content or clobber it.
	unlock := lockPath(path)
	defer unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read file: %w", err)
	}

	// Apply every hunk in memory first. Nothing reaches the disk until all of
	// them have succeeded, which is what makes a multi-hunk edit all-or-nothing:
	// a refactor that fails on its fourth change leaves the file as it was,
	// rather than in a state no one asked for and no one is looking at.
	content := string(data)
	replaced := 0
	for i, h := range hunks {
		next, n, err := applyHunk(content, h, path)
		if err != nil {
			return Result{}, labelHunkError(err, i, len(hunks))
		}
		content, replaced = next, replaced+n
	}

	newContent := []byte(content)
	// Atomic: an edit that fails to write must not consume the file it was
	// editing. The original is still on disk, untouched, if this returns an
	// error — which matters more here than anywhere else, since the only other
	// copy of it is `data`, in memory, about to go out of scope.
	if err := writeFileAtomic(path, newContent); err != nil {
		return Result{}, fmt.Errorf("write file: %w", err)
	}

	// A replaced block that drops a brace or breaks an indent leaves a file that
	// still writes fine and only fails much later, in a build the agent may not
	// run for several turns. The bytes on both sides are already in hand here,
	// so the check costs one parse and reports the damage while the change that
	// caused it is still the last thing that happened.
	note, check := syntaxNote(path, data, newContent)

	return applySyntaxNote(Result{
		Title:  filepath.Base(path),
		Output: summarize(path, len(hunks), replaced),
	}, note, check), nil
}

// collectHunks reduces the two request shapes to one list. The single-edit form
// stays the common case and is simply a one-hunk list; mixing the two forms in
// one call is rejected rather than guessed at, because either reading of that
// intent could silently skip an edit the caller believed they had made.
func collectHunks(edits []editHunk, single editHunk) ([]resolvedHunk, error) {
	hasSingle := single.OldString != "" || single.NewString != nil
	switch {
	case len(edits) > 0 && hasSingle:
		return nil, fmt.Errorf("pass either edits or a single old_string/new_string, not both")
	case len(edits) > 0:
		out := make([]resolvedHunk, 0, len(edits))
		for i, h := range edits {
			r, err := resolveHunk(h)
			if err != nil {
				return nil, fmt.Errorf("edits[%d]: %w", i, err)
			}
			out = append(out, r)
		}
		return out, nil
	case single.OldString == "" && single.NewString == nil:
		// An empty old_string matches everywhere (Go's Count treats it as
		// occurring once between every rune), so it either falls into the
		// "appears N times" ambiguity error with a confusing count, or — on an
		// empty file, where that count is exactly 1 — silently "succeeds" by
		// inserting new_string into a file whose content was never actually
		// matched against anything. Reject it up front with a clear reason
		// instead of either of those.
		return nil, fmt.Errorf("old_string must not be empty")
	default:
		r, err := resolveHunk(single)
		if err != nil {
			return nil, err
		}
		return []resolvedHunk{r}, nil
	}
}

// resolveHunk validates one hunk and settles its replacement text.
func resolveHunk(h editHunk) (resolvedHunk, error) {
	if h.OldString == "" {
		return resolvedHunk{}, fmt.Errorf("old_string must not be empty")
	}
	if h.NewString == nil {
		return resolvedHunk{}, fmt.Errorf("new_string is missing — send it explicitly, using \"\" if deleting old_string is what you meant")
	}
	return resolvedHunk{
		oldStr:        h.OldString,
		newStr:        *h.NewString,
		replaceAll:    h.ReplaceAll,
		expectedCount: h.ExpectedCount,
	}, nil
}

// applyHunk resolves one hunk against the current content, returning the new
// content and how many occurrences it replaced.
func applyHunk(content string, h resolvedHunk, path string) (string, int, error) {
	count := strings.Count(content, h.oldStr)
	switch {
	case count == 0:
		return "", 0, fmt.Errorf("old_string not found in %s%s", path, notFoundHint(content, h.oldStr))
	case h.expectedCount > 0 && count != h.expectedCount:
		// Checked before the uniqueness rule: when both would fire, the broken
		// assumption is the more useful thing to report.
		return "", 0, fmt.Errorf("old_string matches %d times in %s but expected_count is %d (%s) — the anchor is not selecting what you think it is",
			count, path, h.expectedCount, describeMatches(content, h.oldStr))
	case count > 1 && !h.replaceAll:
		// Name where the matches are, what distinguishes them, and both ways
		// out. Without that the caller has to guess which occurrences it hit and
		// re-read blindly, and cannot tell that changing all of them is even an
		// option — which is often what it actually wanted.
		return "", 0, fmt.Errorf(
			"old_string appears %d times in %s (%s) — edit requires a unique match. "+
				"Extend old_string with neighbouring lines until it matches only the occurrence you mean, "+
				"or pass replace_all:true to change all %d",
			count, path, describeMatches(content, h.oldStr), count)
	}

	n := 1
	if h.replaceAll {
		n = count
	}
	return strings.Replace(content, h.oldStr, h.newStr, n), n, nil
}

// labelHunkError attributes a failure to its hunk, so a caller sending six
// edits learns which one to fix rather than which file to re-read. A
// single-hunk call needs no label: there is only one thing it could be.
func labelHunkError(err error, i, total int) error {
	if total == 1 {
		return err
	}
	return fmt.Errorf("edits[%d] of %d failed, no changes were written: %w", i, total, err)
}

func summarize(path string, hunks, replaced int) string {
	if hunks == 1 && replaced == 1 {
		return fmt.Sprintf("Edited %s (replaced 1 occurrence)", path)
	}
	if hunks == 1 {
		return fmt.Sprintf("Edited %s (replaced %d occurrences)", path, replaced)
	}
	return fmt.Sprintf("Edited %s (%d edits, replaced %d occurrences)", path, hunks, replaced)
}

// describeMatches renders where old occurs and what sits above each occurrence.
// The line numbers alone are weak help here: every match is byte-identical by
// definition, so what the caller needs in order to pick one is the line ABOVE
// it — the job name, the function signature, the case label. The list is
// capped: a caller that matched sixty places needs to know it went badly wrong,
// not to read sixty numbers.
func describeMatches(content, old string) string {
	const maxListed = 6
	lines := strings.Split(content, "\n")
	var out []string
	for offset, shown := 0, 0; shown < maxListed; shown++ {
		i := strings.Index(content[offset:], old)
		if i < 0 {
			break
		}
		abs := offset + i
		lineNo := strings.Count(content[:abs], "\n") + 1
		entry := "line " + strconv.Itoa(lineNo)
		if ctx := precedingContext(lines, lineNo); ctx != "" {
			entry += " after " + strconv.Quote(ctx)
		}
		out = append(out, entry)
		// Advance past the match, matching strings.Count's non-overlapping
		// semantics so these line numbers agree with the count in the message.
		offset = abs + len(old)
	}
	if len(out) == 0 {
		return "no line numbers available"
	}
	joined := strings.Join(out, "; ")
	if len(out) == maxListed {
		joined += "; …"
	}
	return joined
}

// precedingContext returns the nearest non-blank line above a 1-based line
// number, trimmed and shortened — the distinguishing text a caller can graft
// onto old_string to make it unique. Empty when there is nothing above.
func precedingContext(lines []string, lineNo int) string {
	const maxLen = 48
	for i := lineNo - 2; i >= 0 && i > lineNo-8; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" {
			continue
		}
		if len(s) > maxLen {
			s = s[:maxLen] + "…"
		}
		return s
	}
	return ""
}

// notFoundHint explains a miss that is really a whitespace mismatch. Retyping a
// block by hand is the usual way old_string goes wrong — a tab rendered as
// spaces, an indent lost, a CRLF file read as LF — and the bare "not found"
// sends the caller looking for the wrong thing entirely. Returns "" when the
// text is genuinely absent.
func notFoundHint(content, old string) string {
	n := strings.Count(trimEachLine(content), trimEachLine(old))
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" — but it matches %d place(s) once the indentation of each line is ignored, so the anchor is right and the whitespace is not; re-read the region and copy its exact leading whitespace", n)
}

// trimEachLine strips the leading and trailing whitespace of every line, so two
// texts can be compared on their content alone. Trailing \r goes with it, which
// is what makes this catch a CRLF file matched against LF text.
func trimEachLine(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.Join(lines, "\n")
}
