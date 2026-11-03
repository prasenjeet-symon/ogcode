package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/filechange"
)

type WriteTool struct{}

func (WriteTool) ID() string { return "write" }
func (WriteTool) Description() string {
	return "Write content to a file, creating it if it doesn't exist. The result reports any syntax error the written content contains, so a truncated or malformed file surfaces immediately rather than at the next build."
}
func (WriteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path to write to"},
			"content": {"type": "string", "description": "Content to write. Absent is refused (it would truncate the file); an explicit \"\" writes an empty file."},
			"description": {"type": "string", "description": "A few lines: what this change does and why. Another agent working in this project will see this text if it has to re-read the file after your edit."}
		},
		"required": ["path", "content", "description"]
	}`)
}

func (WriteTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Path string `json:"path"`
		// Content is a pointer so a field omitted from the call is told apart
		// from an explicit "": an empty write is legitimate intent, an absent
		// one is a bug that would truncate the file to empty.
		Content     *string `json:"content"`
		Description string  `json:"description"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	// The description is the only record of WHY a write happened, and it is
	// what a refused agent is shown: without it the catch-up is a diff with no
	// intent. The schema's "required" is advice to the model (DecodeArgs is a
	// plain unmarshal), so it is enforced here, as a result rather than an
	// error for the same reason the stale refusal is one.
	description := strings.TrimSpace(input.Description)
	if description == "" {
		return Result{
			Title:  "Description required",
			Output: "Every write carries a description: a few lines on what this change does and why. Another agent working in this project sees that text if it has to re-read the file after your write. Resend the call with \"description\" beside \"path\" and \"content\".",
		}, nil
	}

	// Same trap as edit: schema "required" is advice, and a missing path would
	// resolve to the session directory itself and fail with an unreadable
	// "is a directory" on the folder. Land the refusal on the missing field.
	if strings.TrimSpace(input.Path) == "" {
		return Result{Title: "Path required", Output: "path is required — the write has no target. Resend the call with the file's path beside \"content\" and \"description\"."}, nil
	}

	// Content gets the same enforcement as the two above, but through the
	// pointer: a schema-required field the model dropped used to decode to ""
	// and truncate the target to empty. An explicit "" is still written —
	// clearing a file is a real intent, and the pointer is what separates it
	// from the dropped field.
	if input.Content == nil {
		return Result{Title: "Content required", Output: "content is required — the write has nothing to write, and the file would have been truncated to empty. An intentionally empty write passes \"\" as the content. Resend the call with \"content\" beside \"path\" and \"description\"."}, nil
	}
	content := *input.Content

	path := input.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(tctx.SessionDir, path)
	}
	if tctx.SessionDir != "" && path == tctx.SessionDir {
		return Result{Title: "Path required", Output: "path points at the session directory itself — name the file inside it instead. Resend the call with the file's path beside \"content\" and \"description\"."}, nil
	}

	// Serialize with any concurrent write/edit to the same file. The agent loop
	// runs a turn's tool calls in parallel, so two mutations to this path would
	// otherwise race and lose one another's changes.
	log, key := tctx.fileLog(), tctx.fileKey(path)
	unlock := lockFileKey(key)
	defer unlock()

	// Refuse rather than overwrite: another session changed this file after
	// this one last read it, so the content this call was built against is no
	// longer what is on disk. The check runs under the lock, where every
	// writer records its change, so the revision cannot move between the check
	// and the write below.
	seen := log.Seen(key, tctx.SessionID)
	if changes, dropped := log.Stale(ctx, key, tctx.SessionID, seen); len(changes) > 0 || dropped > 0 {
		return staleRefusal(input.Path, log, seen, log.Rev(key), changes, dropped), nil
	}

	// Capture the prior content (if any) so the UI can render a before/after diff.
	// Cap the captured size so huge files don't bloat the persisted message.
	const maxDiffBytes = 256 * 1024
	var oldContent string
	// prior holds the bytes as they were, uncapped, for the syntax comparison
	// below. oldContent is the same content subject to the diff cap: the UI can
	// afford to skip a diff on a huge file, but the check that decides whether
	// this write broke the file cannot afford to skip its baseline.
	var prior []byte
	created := true
	diffOmitted := false

	// Existence is decided by Stat, not by whether the read below succeeds: a
	// file that exists but can't be read (permissions, a transient I/O error)
	// is still an overwrite, not a creation. Keying "created" off ReadFile's
	// error used to conflate the two — a write-only file got silently
	// overwritten while being reported as newly "Created", hiding that content
	// existed and was lost, and dropping the syntax baseline so a pre-existing
	// error would be blamed on this write.
	switch _, statErr := os.Stat(path); {
	case statErr == nil:
		created = false
		if existing, err := os.ReadFile(path); err == nil {
			prior = existing
			if len(existing) <= maxDiffBytes && len(content) <= maxDiffBytes {
				oldContent = string(existing)
			} else {
				diffOmitted = true
			}
		} else {
			// Exists but unreadable: there is no baseline to diff or check
			// syntax against, but it must still not be reported as created.
			diffOmitted = true
		}
	case os.IsNotExist(statErr):
		if len(content) > maxDiffBytes {
			diffOmitted = true
		}
	default:
		return Result{}, fmt.Errorf("stat %s: %w", path, statErr)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create dirs: %w", err)
	}

	// Atomic: a failure here leaves the previous file intact rather than a
	// truncated one. See writeFileAtomic.
	if err := writeFileAtomic(path, []byte(content)); err != nil {
		return Result{}, fmt.Errorf("write file: %w", err)
	}

	// Recorded after the bytes are on disk: the log describes what happened, so
	// it must not claim a change that a failed write never made. prior is nil
	// for a file that did not exist, which is the right baseline — every line
	// of a new file is this call's — but an unreadable existing file has no
	// baseline at all, and a diff invented from empty would be a lie.
	if log != nil {
		diff, truncated := filechange.DiffLines(string(prior), content)
		if !created && prior == nil {
			diff, truncated = "", true
		}
		log.Record(ctx, key, filechange.Change{
			Session:     tctx.SessionID,
			Agent:       tctx.Agent,
			Tool:        "write",
			Note:        description,
			Diff:        diff,
			DiffOmitted: truncated,
		})
	}

	metadata := map[string]any{"created": created}
	if diffOmitted {
		metadata["diffOmitted"] = true
	} else if !created {
		metadata["oldContent"] = oldContent
	}

	// Rewriting a whole file is the mutation most likely to leave it unparseable
	// — a truncated write or a body assembled from two fragments produces a file
	// that saves without complaint. prior is nil for a file that did not exist,
	// which is the right baseline: in a new file every error is this call's.
	note, check := syntaxNote(path, input.Path, prior, []byte(content))

	verb := "Wrote"
	if created {
		verb = "Created"
	}
	return applySyntaxNote(Result{
		Title:    filepath.Base(path),
		Output:   fmt.Sprintf("%s %d bytes to %s", verb, len(content), path),
		Metadata: metadata,
	}, note, check), nil
}
