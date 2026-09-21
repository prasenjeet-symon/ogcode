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
	return "Make one or more search-and-replace edits to a file. Every change goes in the \"edits\" array — a single change is an array of one. The edits apply in order and ALL-OR-NOTHING: if any anchor is missing or ambiguous, nothing is written. Each old_string must match exactly one place unless replace_all is set, so include enough surrounding context to identify the occurrence you mean. The result reports any syntax error the edits introduced, so a broken edit surfaces immediately rather than at the next build."
}

func (EditTool) Parameters() json.RawMessage {
	// There is exactly one way to express an edit here, and that is deliberate.
	// This tool used to accept a top-level old_string/new_string as well as the
	// array, and callers routinely filled both — the flat pair out of habit, the
	// array because the tool asked for it — producing a call that could not be
	// read either way and had to be refused. Documenting the exclusivity did not
	// stop it; removing the second form does.
	//
	// The uniqueness rule is stated here too, because it is the one part of this
	// contract a caller cannot infer from the file: a block that looks unique in
	// the region being read may repeat elsewhere.
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path to edit"},
			"edits": {
				"type": "array",
				"minItems": 1,
				"description": "Every change to this file, in order. ONE change is an array of one entry — there is no other way to pass an edit. They apply all-or-nothing: each is checked first, and if any fails the file is left untouched, so a refactor can never half-apply. Later entries match against the result of earlier ones.",
				"items": {
					"type": "object",
					"properties": {
						"old_string": {"type": "string", "description": "Exact text to find, including indentation. Must match EXACTLY ONE place in the file unless replace_all is true. When a block repeats — the same step in several CI jobs, the same line in several functions — extend it with neighbouring lines until only the intended occurrence matches. Write newlines as plain \\n even in a CRLF file: when every line of the file ends in CRLF, the tool matches and writes the file's own endings."},
						"new_string": {"type": "string", "description": "Text to replace it with. Always send it, even when replacing with nothing: \"\" means delete old_string, and omitting the field is rejected rather than read as a deletion."},
						"replace_all": {"type": "boolean", "description": "Replace every occurrence instead of requiring a unique match. Use when the text genuinely should change everywhere, such as removing one repeated step from every job in a workflow. The result reports how many were replaced."},
						"expected_count": {"type": "integer", "description": "Optional assertion: fail unless old_string matches exactly this many times. Worth setting alongside replace_all, where a miscounted anchor would otherwise rewrite more of the file than intended."}
					},
					"required": ["old_string", "new_string"]
				}
			}
		},
		"required": ["path", "edits"]
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
		Path  string     `json:"path"`
		Edits []editHunk `json:"edits"`
		// Decoded only to recognise the flat shape this tool no longer takes, so
		// a caller reaching for it gets told the exact form to send instead of a
		// puzzling "edits is required". They are never applied: accepting them
		// would restore the two competing forms that made a call ambiguous.
		// All four per-hunk fields are watched, not just the string pair — a
		// top-level replace_all or expected_count used to be dropped without a
		// word, leaving the caller certain an assertion was active when nothing
		// was checking it.
		LegacyOld        *string `json:"old_string"`
		LegacyNew        *string `json:"new_string"`
		LegacyReplaceAll *bool   `json:"replace_all"`
		LegacyExpected   *int    `json:"expected_count"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	sawLegacy := input.LegacyOld != nil || input.LegacyNew != nil ||
		input.LegacyReplaceAll != nil || input.LegacyExpected != nil
	hunks, err := collectHunks(input.Edits, sawLegacy)
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

	if content == string(data) {
		// Every hunk resolved, yet the file would be written back byte-identical:
		// changes that cancel out across entries. Reporting that as an edit is the
		// same lie as the single no-op above, so it fails here too rather than
		// leaving the caller to discover it from an empty diff.
		return Result{}, fmt.Errorf("the edits resolved but cancel out — the file would be unchanged; check that each new_string differs from its old_string")
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
// collectHunks validates the edit list. It takes sawLegacyFields rather than the
// old single-edit values because those are no longer a way to express an edit —
// only a shape worth recognising in order to correct.
func collectHunks(edits []editHunk, sawLegacyFields bool) ([]resolvedHunk, error) {
	if sawLegacyFields {
		return nil, fmt.Errorf(
			"this tool takes every change in the \"edits\" array — top-level old_string/new_string/" +
				"replace_all/expected_count are not read. Resend as edits:[{\"old_string\": \"…\", " +
				"\"new_string\": \"…\"}], one entry per change, with replace_all/expected_count inside " +
				"the entry they apply to")
	}
	if len(edits) == 0 {
		return nil, fmt.Errorf("edits must contain at least one entry, each with old_string and new_string")
	}
	out := make([]resolvedHunk, 0, len(edits))
	for i, h := range edits {
		r, err := resolveHunk(h)
		if err != nil {
			return nil, fmt.Errorf("edits[%d]: %w", i, err)
		}
		out = append(out, r)
	}
	return out, nil
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
	if h.oldStr == h.newStr {
		// Replacing text with itself finds its anchor, writes identical bytes and
		// looks exactly like a successful edit — the tool used to report "replaced
		// 1 occurrence" for it. A caller told its change landed, seeing the file
		// unchanged, has no option but to send it again, which is how this turned
		// into a loop of green ticks that moved nothing.
		return "", 0, fmt.Errorf("new_string is identical to old_string, so this edit would change nothing — send the text you actually want in its place")
	}
	h = normalizeHunkEOL(content, h)
	if h.oldStr == h.newStr {
		// They differed as sent and became equal only under the file's own line
		// endings: the region already reads as new_string. Distinct message from
		// the plain no-op above, because to the caller the two strings look
		// different and "identical" would read as a tool fault.
		return "", 0, fmt.Errorf("once matched against this file's CRLF line endings, new_string is identical to old_string — the file already has this content")
	}
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

// normalizeHunkEOL adapts an LF-authored hunk to a file whose newlines are
// uniformly CRLF.
//
// Models emit \n. In a CRLF file that made every multi-line old_string a
// guaranteed miss — a bare \n cannot occur in content where every \n follows
// a \r — and every multi-line new_string that did land (via a single-line
// anchor) spliced LF lines into a CRLF file, leaving mixed endings that
// nothing flagged. Converting the hunk to the file's own endings fixes both,
// and can never change the outcome of a call that would have succeeded: the
// unconverted form had no way to match.
//
// Only uniformly-CRLF files qualify. In a mixed file an LF anchor may be a
// deliberate reference to one of its LF lines — possibly the caller fixing
// the endings themselves — so its hunks are taken exactly as sent. A hunk
// that carries any \r of its own is likewise left alone: the caller has
// already chosen its endings.
func normalizeHunkEOL(content string, h resolvedHunk) resolvedHunk {
	if !uniformlyCRLF(content) {
		return h
	}
	if strings.Contains(h.oldStr, "\n") && !strings.Contains(h.oldStr, "\r") {
		h.oldStr = strings.ReplaceAll(h.oldStr, "\n", "\r\n")
	}
	if strings.Contains(h.newStr, "\n") && !strings.Contains(h.newStr, "\r") {
		h.newStr = strings.ReplaceAll(h.newStr, "\n", "\r\n")
	}
	return h
}

// uniformlyCRLF reports whether s has newlines and every one of them is a
// CRLF pair. Anything mixed is not this function's business: only a file
// whose convention is unambiguous is safe to adapt a hunk to.
func uniformlyCRLF(s string) bool {
	n := strings.Count(s, "\n")
	return n > 0 && n == strings.Count(s, "\r\n")
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
			// Cut at a rune boundary: this line is quoted into an error message,
			// and a byte slice through a multi-byte character would mangle it.
			s = cutRuneSafe(s, maxLen) + "…"
		}
		return s
	}
	return ""
}

// notFoundHint explains a miss that is really a whitespace mismatch, and — when
// it can identify exactly one candidate — hands back the file's own bytes for
// that region.
//
// Retyping a block is the usual way old_string goes wrong: a tab emitted as
// spaces, an indent level lost, a CRLF region in a mixed-endings file (a
// uniformly-CRLF file never reaches here — normalizeHunkEOL adapts the hunk
// before matching). The bare "not found" sends the caller hunting for text
// that is, in substance, right there. Naming the problem was still not enough
// on its own — knowing the whitespace is wrong does not tell you what it
// should be, so the caller re-reads the file and spends another round trip
// guessing again. Quoting the region ends that: the answer is in the error,
// ready to copy.
func notFoundHint(content, old string) string {
	// An anchor carrying the read tool's truncation marker can never match: the
	// marker replaced the rest of a line longer than read shows, so the bytes
	// the caller anchored on are not the file's. Without this the miss reports
	// as a bare not-found — the indent-blind pass cannot rescue a cut line
	// either — and the caller re-reads and copies the same marker again.
	if strings.Contains(old, lineTruncatedSuffix) {
		return fmt.Sprintf(" — old_string contains %q, which is the read tool's truncation marker, not file"+
			" content: the real line continues past what read displayed. Anchor on a shorter distinctive"+
			" part of that line instead (old_string need not span whole lines), or edit a neighbouring"+
			" region that avoids the long line", lineTruncatedSuffix)
	}
	fileLines := strings.Split(content, "\n")
	oldLines := strings.Split(old, "\n")
	// A trailing newline leaves an empty final element that would never match a
	// line of the file; drop it so the run is what the caller actually anchored on.
	if len(oldLines) > 1 && strings.TrimSpace(oldLines[len(oldLines)-1]) == "" {
		oldLines = oldLines[:len(oldLines)-1]
	}
	starts := indentBlindMatches(fileLines, oldLines)
	switch len(starts) {
	case 0:
		return ""
	case 1:
		excerpt, complete := excerptLines(fileLines, starts[0], len(oldLines))
		instruction := "Send that verbatim as old_string."
		if !complete {
			// A cut excerpt must not carry the "verbatim" instruction: its cut
			// marker is not file content, and a caller obeying literally would
			// anchor on it — the very mistake the marker check above unwinds.
			instruction = fmt.Sprintf("The region is too long to quote in full — re-read lines %d-%d and copy the exact text.",
				starts[0]+1, starts[0]+len(oldLines))
		}
		return fmt.Sprintf(" — but ignoring each line's indentation and line endings it matches exactly one"+
			" place, at line %d, so %s. The file has:\n%s\n%s",
			starts[0]+1, describeWhitespaceDiff(fileLines, oldLines, starts[0]), excerpt, instruction)
	default:
		return fmt.Sprintf(" — but ignoring each line's indentation it matches %d places (lines %s),"+
			" so the anchor is right and the whitespace is not; re-read one of those regions and copy its"+
			" exact leading whitespace", len(starts), formatLineNumbers(starts))
	}
}

// describeWhitespaceDiff names what actually separates the file's region from
// the anchor: leading whitespace, line endings, or both. "Only the leading
// whitespace is wrong" used to be asserted unconditionally, which for a CRLF
// region sent the caller hunting through indentation that was right all along
// while the real difference sat invisibly at the end of every line.
func describeWhitespaceDiff(fileLines, oldLines []string, start int) string {
	leading, endings := false, false
	for j, want := range oldLines {
		got := fileLines[start+j]
		if strings.TrimRight(got, "\r") != strings.TrimRight(want, "\r") {
			leading = true
		}
		if strings.HasSuffix(got, "\r") != strings.HasSuffix(want, "\r") {
			endings = true
		}
	}
	switch {
	case leading && endings:
		return "both the leading whitespace and the line endings differ (this region ends its lines in \\r\\n)"
	case endings:
		return "only the line endings differ — this region ends its lines in \\r\\n (CRLF) while old_string uses \\n"
	default:
		return "only the leading whitespace is wrong"
	}
}

// indentBlindMatches reports every index in fileLines where the run oldLines
// appears, comparing each line only by its trimmed content. Trimming both ends
// takes trailing \r with it, which is what lets this catch a CRLF file matched
// against LF text.
func indentBlindMatches(fileLines, oldLines []string) []int {
	if len(oldLines) == 0 || len(oldLines) > len(fileLines) {
		return nil
	}
	const maxReported = 5
	var starts []int
	for i := 0; i+len(oldLines) <= len(fileLines); i++ {
		match := true
		for j, want := range oldLines {
			if strings.TrimSpace(fileLines[i+j]) != strings.TrimSpace(want) {
				match = false
				break
			}
		}
		if match {
			starts = append(starts, i)
			if len(starts) == maxReported {
				break
			}
		}
	}
	return starts
}

// excerptLines renders n lines of the file from start, exactly as they are on
// disk so the text can be copied straight back. Long blocks are capped: the
// point is to hand over an anchor, not to reprint the file. complete reports
// whether every line made it — a caller telling its reader to copy the excerpt
// verbatim must not do so when part of it is this function's cut marker.
func excerptLines(fileLines []string, start, n int) (excerpt string, complete bool) {
	const maxBytes = 2000
	end := start + n
	if end > len(fileLines) {
		end = len(fileLines)
	}
	var b strings.Builder
	complete = true
	for i := start; i < end; i++ {
		if b.Len()+len(fileLines[i]) > maxBytes {
			fmt.Fprintf(&b, "… (%d more lines)", end-i)
			complete = false
			break
		}
		if i > start {
			b.WriteString("\n")
		}
		b.WriteString(fileLines[i])
	}
	return b.String(), complete
}

// formatLineNumbers renders 0-based indices as 1-based line numbers.
func formatLineNumbers(starts []int) string {
	out := make([]string, len(starts))
	for i, s := range starts {
		out[i] = strconv.Itoa(s + 1)
	}
	return strings.Join(out, ", ")
}
