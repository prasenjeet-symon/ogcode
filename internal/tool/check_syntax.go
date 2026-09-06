package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/codemap"
)

// CheckSyntaxTool parses a file with tree-sitter and reports its syntax errors.
//
// The failure it catches is both common and quiet: a replaced block that drops
// a brace, an indentation slip in Python, a merge of two fragments that leaves
// a stray token. None of those announce themselves — the file writes fine, and
// the damage surfaces later, in a build the agent may not run for several
// turns, by which point it has stacked more edits on a broken parse.
//
// write and edit call syntaxNote for exactly that reason, which leaves this
// tool the cases they cannot cover: a file changed by a shell command, a
// formatter, a patch or a generator, and the confirmation that a fix landed.
//
// The check is deliberately narrow. It shares the parse file_map already relies
// on, costs single-digit milliseconds, and needs no toolchain, so it works in
// any project — but it validates grammar only. It is a cheap guard against
// having broken the file, not a substitute for the compiler.
type CheckSyntaxTool struct{}

func (CheckSyntaxTool) ID() string { return "check_syntax" }

func (CheckSyntaxTool) Description() string {
	return "Parse a source file and report any syntax errors, with the line and column of each. The write and edit tools already run this check on what they change, so use it for a file changed some other way — by a shell command, a formatter, a patch, a generator — and to confirm a file is clean after you fix a reported error. Returns OK when the file parses, the error locations when it does not, and NOT CHECKED when no grammar covers the file type. Checks grammar only: it does not find undefined names, type errors, or logic bugs, so a clean result is not a substitute for running the build or the tests."
}

func (CheckSyntaxTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["path"],
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the file to check (absolute or relative to session directory)"
			}
		}
	}`)
}

func (CheckSyntaxTool) Execute(_ context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := DecodeArgs(args, &params); err != nil {
		return Result{}, fmt.Errorf("parse check_syntax args: %w", err)
	}
	if params.Path == "" {
		return Result{Title: "Check Syntax", Output: "path is required"}, nil
	}

	path := params.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}

	res, err := codemap.Check(path)
	if err != nil {
		// As in file_map, these are properties of the file rather than failures
		// of the call, and the agent needs to be told which one it hit. A
		// missing file after an edit means the path is wrong, and that is worth
		// knowing plainly rather than as a tool error.
		var tooLarge *codemap.TooLargeError
		switch {
		case errors.Is(err, os.ErrNotExist):
			return Result{Title: "Check Syntax", Output: fmt.Sprintf("%s does not exist", params.Path)}, nil
		case errors.Is(err, codemap.ErrBinary):
			return Result{Title: "Check Syntax", Output: fmt.Sprintf("%s is a binary file — it has no syntax to check", params.Path)}, nil
		case errors.As(err, &tooLarge):
			return Result{Title: "Check Syntax", Output: fmt.Sprintf("%s is too large to check (%d bytes)", params.Path, tooLarge.Size)}, nil
		}
		return Result{}, fmt.Errorf("check %s: %w", params.Path, err)
	}

	// The title carries the verdict on its own so it reads correctly in the
	// call list, where the body may be collapsed.
	base := filepath.Base(path)
	title := fmt.Sprintf("Check Syntax / %s — OK", base)
	switch {
	case !res.Checked:
		title = fmt.Sprintf("Check Syntax / %s — not checked", base)
	case len(res.Diagnostics) > 0:
		title = fmt.Sprintf("Check Syntax / %s — %d error(s)", base, len(res.Diagnostics))
	}

	return Result{
		Title: title,
		Metadata: map[string]any{
			"ok":      res.OK(),
			"checked": res.Checked,
			"errors":  len(res.Diagnostics),
		},
		Output: codemap.RenderCheck(res),
	}, nil
}

// noteDiagnostics caps how many errors a write or edit lists inline. The note
// is a signal to go look, not the report — check_syntax gives the full list,
// and the mutating tool's own output should not grow into one.
const noteDiagnostics = 5

// syntaxNote parses a file's content before and after a mutation and returns
// the note the mutating tool should append to its output. It returns "" when
// there is nothing worth saying: the file still parses, no grammar covers it,
// or it is too large or too binary to be worth the parse.
//
// The before/after comparison is the whole point. Checking only the result
// would blame every edit for damage that was already in the file, and an agent
// that learns the warning fires on edits it did not break stops reading it.
// Passing before as nil treats the file as new, where any error is the
// caller's.
//
// The baseline parse only runs once the result is known to be broken, so the
// overwhelmingly common case — a change that leaves the file fine — costs one
// parse, not two.
func syntaxNote(path string, before, after []byte) (string, *codemap.CheckResult) {
	if len(after) > codemap.MaxFileSize || bytes.IndexByte(after, 0) >= 0 {
		return "", nil
	}

	res, err := codemap.CheckSource(path, after)
	// A failure here is a parser problem, not a problem with the caller's
	// change. The write succeeded; staying quiet is better than reporting a
	// tool fault as if it were bad code.
	if err != nil || !res.Checked || len(res.Diagnostics) == 0 {
		return "", res
	}

	brokenBefore := false
	if before != nil {
		if prior, err := codemap.CheckSource(path, before); err == nil {
			brokenBefore = len(prior.Diagnostics) > 0
		}
	}

	shown := res.Diagnostics
	if len(shown) > noteDiagnostics {
		shown = shown[:noteDiagnostics]
	}

	var b strings.Builder
	if brokenBefore {
		fmt.Fprintf(&b, "\n\nSYNTAX NOTE: the file has %d syntax error(s). It also had errors before\nthis change, so some may not be yours.\n\n", len(res.Diagnostics))
	} else {
		fmt.Fprintf(&b, "\n\nSYNTAX ERROR: this change left %d syntax error(s) in the file.\nIt parsed cleanly before, so the damage is in what you just wrote.\n\n", len(res.Diagnostics))
	}
	b.WriteString(codemap.FormatDiagnostics(shown))
	if len(res.Diagnostics) > len(shown) {
		fmt.Fprintf(&b, "  ... and %d more — run check_syntax(%q) for the full list.\n", len(res.Diagnostics)-len(shown), filepath.Base(path))
	}
	b.WriteString("\nFix this before making further changes to the file. The parser recovers after\nan error, so the position shown is where it gave up, not always where the\nmistake is — start at the first one.\n")
	return b.String(), res
}

// applySyntaxNote appends the note to a result and records the verdict in its
// metadata, so the UI can flag a damaging write without parsing the output text.
func applySyntaxNote(res Result, note string, check *codemap.CheckResult) Result {
	if check != nil && check.Checked {
		if res.Metadata == nil {
			res.Metadata = map[string]any{}
		}
		res.Metadata["syntaxOK"] = len(check.Diagnostics) == 0
		res.Metadata["syntaxErrors"] = len(check.Diagnostics)
	}
	res.Output += note
	return res
}
