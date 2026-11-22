package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/filechange"
)

type EditTool struct{}

func (EditTool) ID() string { return "edit" }
func (EditTool) Description() string {
	return "Make one or more edits to a file. Every change goes in the \"edits\" array — a single change is an array of one — and an entry is EITHER positioned (\"at\" plus \"find\"/\"replace\") OR anchored (\"old_string\"/\"new_string\"). Prefer positioned: \"at\" is the line number a read showed you, so \"find\" covers only the lines that change rather than the whole region it sits in, and no unique-match hunt is needed. The call applies all-or-nothing, and the result reports any syntax error the edits introduced, so a broken edit surfaces immediately rather than at the next build."
}

func (EditTool) Parameters() json.RawMessage {
	// One call, one array of entries, and only one of the two entry shapes per
	// call. This tool used to accept a top-level old_string/new_string as well as
	// the array, and callers routinely filled both — the flat pair out of habit,
	// the array because the tool asked for it — producing a call that could not
	// be read either way and had to be refused. Documenting the exclusivity did
	// not stop it; removing the second form does. The same failure appears one
	// level down when a call mixes positioned and anchored entries, which is why
	// collectHunks refuses it rather than guessing.
	//
	// Both forms are described here in full, because a caller sees this schema
	// before it sees the file: the uniqueness rule of the anchored form is the
	// one part of that contract it cannot infer, since a block that looks unique
	// in the region being read may repeat elsewhere.
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path to edit"},
			"description": {"type": "string", "description": "Required. Three or four short lines at most: what this change does and why — a stale read shows it to the next agent. Never restate the diff; longer text is cut to four lines when recorded."},
			"edits": {
				"type": "array",
				"minItems": 1,
				"description": "Every change to this file. ONE change is an array of one entry. Two entry shapes, and one call uses only one of them: POSITIONED {at, find, replace} is the cheap default — at is a line number a read showed you, so find needs only the lines that change; ANCHORED {old_string, new_string} needs a unique match and makes you echo the whole region. They apply all-or-nothing: each is checked first, and if any fails the file is left untouched, so a refactor can never half-apply.",
				"items": {
					"type": "object",
					"properties": {
						"at": {"type": "integer", "minimum": 1, "description": "Positioned form: the 1-based line number to edit, taken from the read this change is based on. Entries are applied bottom-up, so at always means the line you read, never one shifted by another entry. If find has drifted from it, the nearest occurrence wins and the result says how far."},
						"find": {"type": "string", "description": "Positioned form: exact text to replace, copied from the read — only the part that changes, not the whole region, and it need not be unique: at says which occurrence. Omit it to insert replace before line at. Write newlines as plain \\n even in a CRLF file."},
						"replace": {"type": "string", "description": "Positioned form: text to put where find was (\"\" deletes it), or the text to insert before line at when find is omitted. Always send it."},
						"old_string": {"type": "string", "description": "Anchored form: exact text to find, including indentation. Copy it from what a read of this file shows — no line-number prefix, no truncation marker — and never retype it from memory. Must match EXACTLY ONE place unless replace_all is true; when it repeats, extend it with neighbouring lines instead. Prefer the positioned form: it needs neither the unique match nor the whole block."},
						"new_string": {"type": "string", "description": "Anchored form: text to replace it with. Always send it, even when replacing with nothing: \"\" means delete old_string, and omitting the field is rejected rather than read as a deletion."},
						"replace_all": {"type": "boolean", "description": "Anchored form only: replace every occurrence instead of requiring a unique match. The result reports how many were replaced."},
						"expected_count": {"type": "integer", "description": "Anchored form only: fail unless old_string matches exactly this many times. Must be a non-negative integer — 0 (the default) checks nothing."}
					}
				}
			}
		},
		"required": ["path", "edits", "description"]
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

	// The positioned form: at, an optional find, and a replace. It exists
	// because the anchored form charges the caller for its location: the whole
	// region it edits comes back as old_string, and uniqueness has to be bought
	// with even more neighbouring lines. Both costs vanish once the line number
	// from the read is supplied — at names the occurrence, so find only has to
	// carry the text that actually changes. That is also what the calls in this
	// repo's own log were failing on: retyped long anchors (42% of failures) and
	// anchors that could not be made unique (5%).
	At      int     `json:"at"`
	Find    *string `json:"find"`
	Replace *string `json:"replace"`
}

// resolvedHunk is a hunk that has passed validation, with its replacement text
// settled so the apply path never has to ask whether it was supplied.
type resolvedHunk struct {
	oldStr, newStr string
	replaceAll     bool
	expectedCount  int
	// positioned selects the at-based apply path. A positioned hunk's oldStr
	// is its find text and may be empty, which means "insert before at".
	positioned bool
	at         int
	// anchoredFallback marks an entry that arrived as find/replace with no at
	// and was therefore applied as anchored. The result says so: the caller
	// learns that its find text had to be unique, and that at is what makes
	// the same entry cheap.
	anchoredFallback bool
}

func (EditTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Path        string     `json:"path"`
		Edits       []editHunk `json:"edits"`
		Description string     `json:"description"`
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
	// A stringified array usually reads back cleanly, and refusing every one of
	// them cost a full round trip: it was the second largest cause of failed
	// edits in this project's own call log (27% of them), and the caller's
	// payload was correct apart from its quoting. Recover it here; the refusal
	// below stays for the payloads whose escaping cannot be parsed.
	args, recovered := unwrapStringifiedEdits(args)
	if stringifiedEdits(args) {
		// A stringified array is a shape some models emit for an array param:
		// the hunk list arrives as a JSON string HOLDING the array rather than
		// the array itself. The plain decode below then fails with "cannot
		// unmarshal string into Go struct field .edits", which names neither
		// the fix nor the field as the caller wrote it, and the model retries
		// the same call. Intercept it and say what to send.
		//
		// The string is deliberately not unwrapped: the escaping that made the
		// inner text a string also collapses its \n and \t escapes into raw
		// control characters, so an unwrap would repair some payloads and
		// silently mangle others. See edit.go's Parameters for the one form.
		return Result{}, fmt.Errorf(
			"edits arrived as a string containing a JSON array, not as an array — the hunks " +
				"could not be read. Resend them as array entries: " +
				"\"edits\": [{\"old_string\": \"…\", \"new_string\": \"…\"}], one entry per change")
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	// The description is the only record of WHY an edit happened, and it is
	// what a refused agent is shown: without it the catch-up is a diff with no
	// intent. The schema asks for it on every call, and that is where it
	// normally comes from — but a missing one must never cost the caller its
	// payload. Refusing here meant generating every hunk again (264 of this
	// project's own edits), so the edit lands and the row records a generated
	// note that says the description was missing. A description that
	// overshoots the few lines a refusal can show is cut to
	// descriptionMaxLines (clampDescription) rather than refused.
	description := clampDescription(input.Description)
	noDescription := description == ""

	// The schema's "required" is advice (DecodeArgs is a plain unmarshal), so a
	// model that drops the path still reaches here: the join below resolves an
	// empty path to the session directory itself, and the read that follows
	// fails with "read <project dir>: is a directory" — an error naming no
	// parameter, which leaves the model guessing. Land the refusal on the field.
	if strings.TrimSpace(input.Path) == "" {
		return Result{Title: "Path required", Output: "path is required — the edit has no target. Resend the call with the file's path beside \"edits\" and \"description\"."}, nil
	}
	sawLegacy := input.LegacyOld != nil || input.LegacyNew != nil ||
		input.LegacyReplaceAll != nil || input.LegacyExpected != nil
	hunks, err := collectHunks(input.Edits, sawLegacy)
	if err != nil {
		return Result{}, err
	}
	if noDescription {
		description = generatedNote("edit", input.Path, len(hunks))
	}
	if recovered {
		// An insert has no anchor, so nothing would catch text the decoding
		// mangled on the way in. Refuse that one shape rather than write it.
		for i, h := range hunks {
			if h.positioned && h.oldStr == "" {
				return Result{}, fmt.Errorf("edits[%d]: this call's edits arrived as a JSON string, and an insert has no anchor to check the decoded text against — resend them as real array entries", i)
			}
		}
	}

	path := input.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}
	if tctx.SessionDir != "" && path == tctx.SessionDir {
		return Result{Title: "Path required", Output: "path points at the session directory itself — name the file inside it instead. Resend the call with the file's path beside \"edits\" and \"description\"."}, nil
	}

	// Serialize the read-modify-write with any concurrent write/edit to the same
	// file. The agent loop runs a turn's tool calls in parallel; without this an
	// interleaved write could make edit operate on stale content or clobber it.
	log, key := tctx.fileLog(), tctx.fileKey(path)
	unlock := lockFileKey(key)
	defer unlock()

	// Refuse rather than apply the hunks: another session changed this file
	// after this one last read it, so the anchors were chosen against content
	// that is no longer there — and an anchor that still matches would cut a
	// region its author never saw. The check runs under the lock, where every
	// writer records its change, so the revision cannot move between the check
	// and the write below.
	seen := log.Seen(key, tctx.SessionID)
	if changes, dropped := log.Stale(ctx, key, tctx.SessionID, seen); len(changes) > 0 || dropped > 0 {
		return staleRefusal(input.Path, log, seen, log.Rev(key), changes, dropped), nil
	}

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
	relocated, drift := 0, 0
	anchoredNoAt := 0
	order := make([]int, len(hunks))
	for i := range order {
		order[i] = i
	}
	if hunks[0].positioned {
		// Every positioned hunk carries a line number from the same read, so
		// applying them bottom-up keeps each of those numbers valid: a change
		// at line 400 cannot move line 42. Anchored hunks keep their given
		// order, where each matches the result of the one before it.
		sort.SliceStable(order, func(a, b int) bool { return hunks[order[a]].at > hunks[order[b]].at })
	}
	for _, i := range order {
		next, n, moved, err := applyHunk(content, hunks[i], path)
		if err != nil {
			if recovered {
				err = fmt.Errorf("%w (these edits arrived as a JSON string and were decoded from it, which can mangle escapes — if the text above looks wrong, resend them as real array entries)", err)
			}
			return Result{}, labelHunkError(err, i, len(hunks))
		}
		content, replaced = next, replaced+n
		if moved != 0 {
			relocated++
			drift = max(drift, absInt(moved))
		}
		if hunks[i].anchoredFallback {
			anchoredNoAt++
		}
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

	// Recorded after the bytes are on disk: the log describes what happened, so
	// it must not claim a change a failed write never made. data is the file as
	// it was read above, which is exactly what the agent's anchors matched.
	if log != nil {
		diff, truncated := filechange.DiffLines(string(data), content)
		log.Record(ctx, key, filechange.Change{
			Session:     tctx.SessionID,
			Agent:       tctx.Agent,
			Tool:        "edit",
			Note:        description,
			Diff:        diff,
			DiffOmitted: truncated,
		})
	}

	// A replaced block that drops a brace or breaks an indent leaves a file that
	// still writes fine and only fails much later, in a build the agent may not
	// run for several turns. The bytes on both sides are already in hand here,
	// so the check costs one parse and reports the damage while the change that
	// caused it is still the last thing that happened.
	note, check := syntaxNote(path, input.Path, data, newContent)

	out := summarize(path, len(hunks), replaced)
	if relocated > 0 {
		// Say so rather than pretend the line numbers were right: a caller that
		// sees the move can trust the rest, and one that did not mean it gets
		// the chance to look before building on the result.
		out += fmt.Sprintf(" — %d of %d landed away from the line given (up to %d lines off: that region had shifted), so check the result",
			relocated, len(hunks), drift)
	}
	if noDescription {
		out += noDescriptionNote("edit")
	}
	if anchoredNoAt > 0 {
		// The entry worked, but only because its find text happened to be
		// unique. Say so, or the caller keeps paying that cost and never
		// learns that at is what makes the same entry cheap.
		noun := "entries"
		if anchoredNoAt == 1 {
			noun = "entry"
		}
		out += fmt.Sprintf(" — %d of %d %s carried find/replace without at and had to match exactly one place; send at, taken from your read, for the positioned form",
			anchoredNoAt, len(hunks), noun)
	}
	return applySyntaxNote(Result{
		Title:  filepath.Base(path),
		Output: out,
	}, note, check), nil
}

// stringifiedEdits reports whether the call's "edits" value is a JSON string
// rather than the array the schema asks for. Some models quote an array param
// they cannot express directly; this recognises the shape so the caller can be
// told what to send. Unreadable args are left for the decoder to report.
func stringifiedEdits(args json.RawMessage) bool {
	var probe struct {
		Edits json.RawMessage `json:"edits"`
	}
	if json.Unmarshal(args, &probe) != nil {
		return false
	}
	trimmed := strings.TrimSpace(string(probe.Edits))
	return strings.HasPrefix(trimmed, `"`)
}

// unwrapStringifiedEdits rewrites a call whose "edits" is a JSON string holding
// the array into the array it was meant to be, and reports whether it did.
//
// It rewrites rather than decodes so the result still passes through DecodeArgs,
// which is what coerces a line number sent as 42.0. Anything it cannot read back
// — including a payload whose escapes collapsed into raw control characters —
// is returned untouched, so the refusal in Execute still fires with its
// explanation instead of guessing at the intent.
func unwrapStringifiedEdits(args json.RawMessage) (json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(args, &fields) != nil {
		return args, false
	}
	raw, ok := fields["edits"]
	if !ok || !strings.HasPrefix(strings.TrimSpace(string(raw)), `"`) {
		return args, false
	}
	var inner string
	if json.Unmarshal(raw, &inner) != nil {
		return args, false
	}
	var entries []json.RawMessage
	if json.Unmarshal([]byte(inner), &entries) != nil || len(entries) == 0 {
		return args, false
	}
	for _, e := range entries {
		if !json.Valid(e) {
			return args, false
		}
	}
	array, err := json.Marshal(entries)
	if err != nil {
		return args, false
	}
	fields["edits"] = array
	out, err := json.Marshal(fields)
	if err != nil {
		return args, false
	}
	return out, true
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
		return nil, fmt.Errorf("edits must contain at least one entry, each with old_string and new_string, or with at and replace")
	}
	positioned := 0
	for _, h := range edits {
		if h.At != 0 {
			positioned++
		}
	}
	if positioned > 0 && positioned < len(edits) {
		// The two shapes disagree about what "later" means: an anchored hunk
		// matches the result of the one before it, while a positioned one
		// names lines from a read taken before any of them ran. A mixed call
		// would have to guess which of those the caller meant. Only at
		// counts as positioned: find/replace without it is anchored in
		// substance — located by content, not by line — so it belongs on
		// the anchored side of this check.
		return nil, fmt.Errorf("this call mixes positioned entries (at/find/replace) with anchored ones " +
			"(old_string/new_string) — send one shape for the whole file: either at with find/replace, or " +
			"old_string with new_string")
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
	if h.At != 0 || h.Find != nil || h.Replace != nil {
		return resolvePositioned(h)
	}
	if h.OldString == "" {
		return resolvedHunk{}, fmt.Errorf("old_string must not be empty")
	}
	if h.NewString == nil {
		return resolvedHunk{}, fmt.Errorf("new_string is missing — send it explicitly, using \"\" if deleting old_string is what you meant")
	}
	if h.ExpectedCount < 0 {
		// The apply path only consults the value when it is > 0, so a
		// negative one silently passed for "no assertion" — an argument the
		// caller got wrong while believing an assertion was active.
		return resolvedHunk{}, fmt.Errorf("expected_count %d is negative — it must be a non-negative integer asserting how many times old_string matches; omit it (or pass 0) for no assertion", h.ExpectedCount)
	}
	return resolvedHunk{
		oldStr:        h.OldString,
		newStr:        *h.NewString,
		replaceAll:    h.ReplaceAll,
		expectedCount: h.ExpectedCount,
	}, nil
}

// resolvePositioned validates an entry of the at-based form.
func resolvePositioned(h editHunk) (resolvedHunk, error) {
	if h.OldString != "" || h.NewString != nil {
		return resolvedHunk{}, fmt.Errorf("this entry carries at/find/replace as well as old_string/new_string — " +
			"they are two ways of locating the same change, so send one: at with find/replace, or old_string with new_string")
	}
	if h.At < 1 {
		// No line number: this is the anchored form spelled with the
		// positioned field names, the shape a caller reaches for before
		// anything told it at is required. Its payload is a correct anchor,
		// so read it that way rather than charging it a round trip.
		return resolveWithoutAt(h)
	}
	if h.Replace == nil {
		return resolvedHunk{}, fmt.Errorf("replace is missing — send it explicitly, using \"\" if deleting the find text is what you meant")
	}
	if h.ReplaceAll || h.ExpectedCount != 0 {
		return resolvedHunk{}, fmt.Errorf("replace_all and expected_count belong to the anchored form: a positioned entry already names the one region at edits, so there is no count to assert")
	}
	find := ""
	if h.Find != nil {
		find = *h.Find
		if find == "" {
			return resolvedHunk{}, fmt.Errorf("find is empty — omit the field to insert replace before line at, or send the text to replace")
		}
		if find == *h.Replace {
			return resolvedHunk{}, fmt.Errorf("replace is identical to find, so this edit would change nothing — send the text you actually want in its place")
		}
	}
	if find == "" && *h.Replace == "" {
		return resolvedHunk{}, fmt.Errorf("this entry neither replaces nor inserts anything — omit find only when replace has text")
	}
	return resolvedHunk{oldStr: find, newStr: *h.Replace, positioned: true, at: h.At}, nil
}

// resolveWithoutAt reads an entry that carries find/replace but no at. It is
// the anchored form under the positioned field names — find is the anchor and
// has to match exactly one place — and a caller that never learned at was
// required should not lose the call over it. replace alone has no anchor at
// all, so there at is genuinely missing and the refusal says so.
func resolveWithoutAt(h editHunk) (resolvedHunk, error) {
	if h.At < 0 {
		// A negative line number is a bad at, not a missing one: falling back
		// would silently drop the position the caller asked for.
		return resolvedHunk{}, fmt.Errorf("at must be the 1-based line number to edit, taken from the read this change is based on — got %d", h.At)
	}
	if h.Replace == nil {
		return resolvedHunk{}, fmt.Errorf("replace is missing — send it explicitly, using \"\" if deleting the find text is what you meant")
	}
	if h.Find == nil || *h.Find == "" {
		return resolvedHunk{}, fmt.Errorf("at is missing — replace with no find inserts before a line, so it needs the 1-based line number a read showed you; send at, or send find with the text to replace")
	}
	if *h.Find == *h.Replace {
		return resolvedHunk{}, fmt.Errorf("replace is identical to find, so this edit would change nothing — send the text you actually want in its place")
	}
	if h.ExpectedCount < 0 {
		return resolvedHunk{}, fmt.Errorf("expected_count %d is negative — it must be a non-negative integer asserting how many times find matches; omit it (or pass 0) for no assertion", h.ExpectedCount)
	}
	return resolvedHunk{
		oldStr:           *h.Find,
		newStr:           *h.Replace,
		replaceAll:       h.ReplaceAll,
		expectedCount:    h.ExpectedCount,
		anchoredFallback: true,
	}, nil
}

// applyHunk resolves one hunk against the current content, returning the new
// content, how many occurrences it replaced, and how many lines away from the
// line the caller named it landed (0 when it landed exactly there).
func applyHunk(content string, h resolvedHunk, path string) (string, int, int, error) {
	if h.positioned {
		return applyPositioned(content, h, path)
	}
	next, n, err := applyAnchored(content, h, path)
	return next, n, 0, err
}

// applyPositioned resolves one at-based hunk.
//
// The line number is a hint, never an identity: the find text is what decides
// where the edit lands, and at only says which of several occurrences the
// caller meant — with the nearest one winning when the file has shifted under
// it. That is the whole trade against the anchored form: a stale line number
// costs nothing, where a stale anchor costs a failed call, and a short anchor
// costs nothing where a short old_string costs an ambiguity error.
func applyPositioned(content string, h resolvedHunk, path string) (string, int, int, error) {
	h = normalizeHunkEOL(content, h)
	starts := lineStarts(content)
	if h.at > len(starts)+1 {
		return "", 0, 0, fmt.Errorf("at %d is past the end of %s, which has %d lines — take the number from the read this change is based on",
			h.at, path, len(starts))
	}
	offset := len(content)
	if h.at <= len(starts) {
		offset = starts[h.at-1]
	}
	if h.oldStr == "" {
		ins := h.newStr
		if !strings.HasSuffix(ins, "\n") {
			ins += eolFor(content)
		}
		return content[:offset] + ins + content[offset:], 1, 0, nil
	}
	best, bestDist := -1, 0
	for from := 0; ; {
		i := strings.Index(content[from:], h.oldStr)
		if i < 0 {
			break
		}
		pos := from + i
		dist := absInt(pos - offset)
		if best < 0 || dist < bestDist {
			best, bestDist = pos, dist
			if dist == 0 {
				break
			}
		}
		from = pos + 1
	}
	if best < 0 {
		return "", 0, 0, fmt.Errorf("find text not found in %s%s", path, notFoundHint(content, h.oldStr))
	}
	moved := 0
	switch {
	case best > offset:
		moved = strings.Count(content[offset:best], "\n")
	case best < offset:
		moved = -strings.Count(content[best:offset], "\n")
	}
	return content[:best] + h.newStr + content[best+len(h.oldStr):], 1, moved, nil
}

// lineStarts returns the byte offset of each 1-based line's start, numbered as
// the read tool numbers them: a file ending in a newline has no extra empty
// line after it, so len(starts) is its line count and starts[len(starts)] —
// the end of the file — is a legal place to insert at.
func lineStarts(content string) []int {
	starts := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' && i+1 < len(content) {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// eolFor names the newline to terminate an inserted line with, so an insert
// into a CRLF file does not leave one LF line behind.
func eolFor(content string) string {
	if uniformlyCRLF(content) {
		return "\r\n"
	}
	return "\n"
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// applyAnchored resolves one old_string/new_string hunk.
func applyAnchored(content string, h resolvedHunk, path string) (string, int, error) {
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
		if hint := alreadyAppliedHint(content, h.newStr); hint != "" {
			return "", 0, fmt.Errorf("old_string not found in %s%s", path, hint)
		}
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

// alreadyAppliedHint recognises the retry of an edit that has already landed:
// the anchor is gone precisely because it was replaced by new_string. The
// caller cannot tell that apart from a wrong anchor by itself, so it re-reads
// and rewrites a change that is already in the file — 18% of this repo's failed
// edits were resends of an edit that had already been applied once. Only an
// unambiguous single occurrence is reported; anything else is left to the
// not-found hint.
func alreadyAppliedHint(content, newStr string) string {
	if len(strings.TrimSpace(newStr)) < 2 || strings.Count(content, newStr) != 1 {
		return ""
	}
	at := strings.Index(content, newStr)
	if embeddedInWord(content, at, len(newStr)) {
		// A short new_string such as "x" turns up inside unrelated identifiers
		// ("Exec") far more often than it is evidence of anything, and the
		// hint would replace the diagnostics that actually help.
		return ""
	}
	lineNo := strings.Count(content[:at], "\n") + 1
	return fmt.Sprintf(" — but new_string already appears once, at line %d: this change looks applied already."+
		" Read that region before resending, rather than sending the same edit again", lineNo)
}

// embeddedInWord reports whether the match at content[at:at+n] is part of a
// longer identifier rather than a token of its own.
func embeddedInWord(content string, at, n int) bool {
	if at > 0 && n > 0 && isWordByte(content[at-1]) && isWordByte(content[at]) {
		return true
	}
	end := at + n
	return end > at && end < len(content) && isWordByte(content[end-1]) && isWordByte(content[end])
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
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
		// No line of the file echoes the anchor even ignoring indentation, so
		// quoting the file back is worse than naming the mistake: these bytes
		// are not the file's. The usual source is the one the cases below
		// rescue — the caller retyped from memory instead of copying a read —
		// only with the wording drifted too far for a whitespace-only match.
		// Point the re-read at the region the anchor's first line still
		// locates; when not even that survives, the generic re-read rule is
		// all that is left to say.
		if named := firstLineMatches(fileLines, oldLines); len(named) > 0 {
			return fmt.Sprintf(" — and dropping every difference in spacing it still matches nowhere: the wording has"+
				" drifted, not just the leading spaces. old_string's first line does appear on %s — re-read the"+
				" region around one of those lines and copy old_string verbatim from the fresh read: the"+
				" file's own bytes there, no line numbers, no truncation marker",
				formatLineNumbers(named))
		}
		return " — and dropping every difference in spacing it still matches nowhere: these are not the file's bytes as" +
			" written. Re-read the region this block should live in and copy old_string verbatim from what" +
			" that read shows — the file's own exact text, no line numbers, no truncation" +
			" marker — rather than retyping it from memory"
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

// firstLineMatches finds lines of the file whose trimmed text contains the
// anchor's first non-blank line — or is contained by it. It is the rescue for
// a bare miss: an anchor too mangled for indentBlindMatches usually still
// carries its first line intact. Few matches say where the caller thought
// this block lives; many say the line is generic, and naming them would be
// noise, so nil means "be generic" back in notFoundHint. Blank lines and
// candidates shorter than a phrase are skipped so a lone "}" never matches.
func firstLineMatches(fileLines, oldLines []string) []int {
	first := ""
	for _, line := range oldLines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			first = trimmed
			break
		}
	}
	if len(first) < 4 {
		return nil
	}
	var named []int
	for i, line := range fileLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || len(trimmed) < 4 {
			continue
		}
		if strings.Contains(trimmed, first) || strings.Contains(first, trimmed) {
			named = append(named, i)
		}
	}
	if len(named) == 0 || len(named) > 5 {
		return nil
	}
	return named
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
