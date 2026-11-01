package agent

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// Tool-audit CSV. Every tool call funnels through executeTool, so built-ins and
// MCP tools land here alike: one row per finished call, appended to
// <workDir>/.ogcode/tool_calls.csv. The file is plain CSV so the agent (via the
// query_sheet tool) or a user in a spreadsheet can ask questions about tool
// activity — how long each call took, which failed, who ran what — without the
// session DB.

// toolAuditEnv names the process-wide kill switch. On by default; a falsey
// value (0/false/no/off) stops rows from being written.
const toolAuditEnv = "OGCODE_TOOL_AUDIT"

// Outcome values for the status column. The first three match session.ToolStatus
// so rows read like the transcript; the rest are outcomes the transcript never
// distinguishes (a disallowed call is stored as a completed part whose output
// is the rejection text).
const (
	toolOutcomeCompleted  = string(session.ToolCompleted)
	toolOutcomeError      = string(session.ToolError)
	toolOutcomeDenied     = string(session.ToolDenied)
	toolOutcomeCancelled  = "cancelled"
	toolOutcomeDisallowed = "disallowed"
	toolOutcomeUnknown    = "unknown"
	toolOutcomePanic      = "panic"
)

// toolAuditHeader is written once, when the file starts empty. `target` sits
// right after `tool`, so a row reads who called what against which file.
var toolAuditHeader = []string{"at", "session", "agent", "tool", "target", "status", "duration_ms", "model", "provider", "error"}

// toolAuditTargetIndex is `target`'s position in toolAuditHeader. Pre-target
// files (a 9-column header) are migrated to it in migrateToolAuditHeader.
const toolAuditTargetIndex = 4

// toolAuditTargetKeys lists the argument keys that carry the file a call is
// aimed at, in the order they are checked.
var toolAuditTargetKeys = []string{"path", "file"}

// toolAuditTarget pulls the targeted file's full path out of a call's
// arguments, where the tool names one: `read`/`write`/`edit`/`file_map` and
// kindred tools take `path`, `query_sheet` takes `file`, and `read` also takes
// a `files` list (the first entry is recorded). A relative path is resolved
// against the work directory so the column always holds a full path. A call
// that names no file — bash, glob, skill, an MCP tool with other shapes —
// leaves the column empty.
func toolAuditTarget(workDir string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var args map[string]any
	if json.Unmarshal(input, &args) != nil {
		return ""
	}
	for _, key := range toolAuditTargetKeys {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return auditTargetPath(workDir, s)
			}
			return ""
		}
	}
	if list, ok := args["files"].([]any); ok && len(list) > 0 {
		if first, ok := list[0].(map[string]any); ok {
			if s, ok := first["path"].(string); ok && s != "" {
				return auditTargetPath(workDir, s)
			}
		}
	}
	return ""
}

// auditTargetPath makes a targeted file's path full: absolute stays as-is;
// anything else is joined against the work directory, which is where the file
// tools resolve relative paths.
func auditTargetPath(workDir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, p)
}

var (
	toolAuditMu sync.Mutex
	// toolAuditDead remembers paths that can no longer be written (unwritable
	// directory, full disk). One warning per path, then silence — a
	// best-effort log must not warn on every tool call.
	toolAuditDead sync.Map
)

// toolAuditPath is where a workDir's tool-audit CSV lives. `.ogcode/` sits
// outside the index unconditionally, so the file is never treated as a project
// document — it is read through query_sheet or a spreadsheet, which take the
// explicit path.
func toolAuditPath(workDir string) string {
	return filepath.Join(workDir, ".ogcode", "tool_calls.csv")
}

// toolAuditEnabled reads the kill switch. On unless the env names a falsey
// value, the same convention compactContextEnabled uses.
func toolAuditEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(toolAuditEnv))) {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// auditToolCall appends one row for a finished tool call. Best-effort: a write
// failure is logged once per path and the call's own result is untouched. The
// row's `at` is the call's start time; `duration_ms` covers the call's whole
// lifecycle — permission wait included — to the moment it returned. `target`
// is the file the call aimed at, when its arguments name one.
func auditToolCall(workDir, sessionID, agentID, toolName string, input json.RawMessage, status string, start time.Time, took time.Duration, modelID, providerID, errText string) {
	if !toolAuditEnabled() {
		return
	}
	path := toolAuditPath(workDir)
	if _, dead := toolAuditDead.Load(path); dead {
		return
	}

	// Tool calls run concurrently (runInFileOrder fans out a goroutine per
	// ready call), so the append is serialized.
	toolAuditMu.Lock()
	defer toolAuditMu.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		killToolAudit(path, err)
		return
	}
	if err := migrateToolAuditHeader(path); err != nil {
		killToolAudit(path, err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		killToolAudit(path, err)
		return
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if info, serr := f.Stat(); serr == nil && info.Size() == 0 {
		if werr := w.Write(toolAuditHeader); werr != nil {
			killToolAudit(path, werr)
			return
		}
	}
	if werr := w.Write([]string{
		start.Format(time.RFC3339Nano),
		sessionID,
		agentID,
		toolName,
		toolAuditTarget(workDir, input),
		status,
		strconv.FormatInt(took.Milliseconds(), 10),
		modelID,
		providerID,
		errText,
	}); werr != nil {
		killToolAudit(path, werr)
		return
	}
	w.Flush()
	if werr := w.Error(); werr != nil {
		killToolAudit(path, werr)
	}
}

// migrateToolAuditHeader upgrades a file written before the `target` column
// existed: a 9-column header and rows. It runs under toolAuditMu, before the
// append — the header is rewritten once, every older row padded with an empty
// target cell at toolAuditTargetIndex, and the new row then appends to the
// widened table. A file missing or empty needs no migration.
func migrateToolAuditHeader(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1 // keep ragged rows instead of failing the read
	rows, err := r.ReadAll()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	sameHeader := len(rows[0]) == len(toolAuditHeader)
	for i := 0; sameHeader && i < len(toolAuditHeader); i++ {
		sameHeader = rows[0][i] == toolAuditHeader[i]
	}
	if sameHeader {
		return nil
	}
	for i := 1; i < len(rows); i++ {
		if toolAuditTargetIndex <= len(rows[i]) {
			old := rows[i]
			row := make([]string, 0, len(old)+1)
			row = append(row, old[:toolAuditTargetIndex]...)
			row = append(row, "")
			row = append(row, old[toolAuditTargetIndex:]...)
			rows[i] = row
		}
	}
	rows[0] = append([]string{}, toolAuditHeader...)
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.WriteAll(rows); err != nil {
		return err
	}
	w.Flush()
	if werr := w.Error(); werr != nil {
		return werr
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}

// killToolAudit takes a path out of service and says so — once.
func killToolAudit(path string, err error) {
	if _, dead := toolAuditDead.LoadOrStore(path, err); !dead {
		slog.Warn("tool-audit CSV stopped writing here; calls continue unlogged",
			"path", path, "err", err)
	}
}
