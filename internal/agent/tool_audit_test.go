package agent

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// Audit rows land in <workDir>/.ogcode/tool_calls.csv in plain CSV, so the
// query_sheet tool (or a user in a spreadsheet) can ask questions about tool
// call activity. These tests pin the writer, the kill switch, and that a real
// turn's tool call shows up with its outcome and duration.

func TestAuditToolCallWritesHeaderAndRows(t *testing.T) {
	t.Setenv(toolAuditEnv, "")
	dir := t.TempDir()
	start := time.Now()

	auditToolCall(dir, "s1", "build", "bash", nil, toolOutcomeCompleted, start,
		1500*time.Millisecond, "mock-model", "mock", "")
	auditToolCall(dir, "s1", "build", "bash", json.RawMessage(`{}`), toolOutcomeError, start,
		300*time.Millisecond, "mock-model", "mock", `failed: err, with "quotes" and
a newline`)

	rows := readAuditRows(t, toolAuditPath(dir))
	if len(rows) != 3 {
		t.Fatalf("got %d CSV rows, want header + 2 calls", len(rows))
	}
	gotHeader := rows[0]
	wantHeader := toolAuditHeader
	if len(gotHeader) != len(wantHeader) {
		t.Fatalf("header columns = %v, want %v", gotHeader, wantHeader)
	}
	for i := range wantHeader {
		if gotHeader[i] != wantHeader[i] {
			t.Fatalf("header column %d = %q, want %q", i, gotHeader[i], wantHeader[i])
		}
	}

	completed := rows[1]
	if completed[toolAuditColumn("session")] != "s1" ||
		completed[toolAuditColumn("agent")] != "build" ||
		completed[toolAuditColumn("tool")] != "bash" ||
		completed[toolAuditColumn("status")] != string(session.ToolCompleted) ||
		completed[toolAuditColumn("model")] != "mock-model" ||
		completed[toolAuditColumn("provider")] != "mock" ||
		completed[toolAuditColumn("error")] != "" {
		t.Fatalf("completed row = %v, fields do not match expectations", completed)
	}
	if ms := completed[toolAuditColumn("duration_ms")]; ms != "1500" {
		t.Fatalf("duration_ms = %q, want 1500", ms)
	}
	if at, err := time.Parse(time.RFC3339Nano, completed[toolAuditColumn("at")]); err != nil {
		t.Fatalf("at is not RFC3339Nano: %v", err)
	} else if got := at.Sub(start); got < -time.Second || got > time.Second {
		t.Fatalf("at drifted %v from the call's start", got)
	}

	errored := rows[2]
	wantErr := `failed: err, with "quotes" and
a newline`
	if errored[toolAuditColumn("status")] != string(session.ToolError) {
		t.Fatalf("error row status = %q, want %q", errored[toolAuditColumn("status")], session.ToolError)
	}
	if errored[toolAuditColumn("error")] != wantErr {
		t.Fatalf("error field not quoted intact: %q", errored[toolAuditColumn("error")])
	}
}

// auditTestInput marshals a call's arguments so a test can pass the same
// thing the loop passes on: the raw JSON of the call as streamed.
func auditTestInput(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal tool input: %v", err)
	}
	return raw
}

func TestAuditToolCallRecordsTarget(t *testing.T) {
	t.Setenv(toolAuditEnv, "")
	dir := t.TempDir()
	start := time.Now()

	auditToolCall(dir, "s1", "build", "read", auditTestInput(t, map[string]any{"path": "sub/file.txt"}),
		toolOutcomeCompleted, start, time.Millisecond, "m", "p", "")
	abs := filepath.Join(dir, "elsewhere.txt")
	auditToolCall(dir, "s1", "build", "edit", auditTestInput(t, map[string]any{"path": abs}),
		toolOutcomeCompleted, start, time.Millisecond, "m", "p", "")
	auditToolCall(dir, "s1", "build", "query_sheet", auditTestInput(t, map[string]any{"file": "report.csv", "sql": "SELECT 1"}),
		toolOutcomeCompleted, start, time.Millisecond, "m", "p", "")
	auditToolCall(dir, "s1", "build", "read", auditTestInput(t, map[string]any{"files": []any{map[string]any{"path": "a.txt"}, map[string]any{"path": "b.txt"}}}),
		toolOutcomeCompleted, start, time.Millisecond, "m", "p", "")
	auditToolCall(dir, "s1", "build", "bash", auditTestInput(t, map[string]any{"command": "ls -la"}),
		toolOutcomeCompleted, start, time.Millisecond, "m", "p", "")

	rows := readAuditRows(t, toolAuditPath(dir))
	if len(rows) != 6 {
		t.Fatalf("got %d CSV rows, want header + 5 calls", len(rows))
	}
	cases := []struct {
		tool, wantTarget string
	}{
		{"read", filepath.Join(dir, "sub", "file.txt")},
		{"edit", abs},
		{"query_sheet", filepath.Join(dir, "report.csv")},
		{"read", filepath.Join(dir, "a.txt")},
		{"bash", ""},
	}
	for i, c := range cases {
		row := rows[i+1]
		if row[toolAuditColumn("tool")] != c.tool {
			t.Fatalf("row %d tool = %q, want %q", i, row[toolAuditColumn("tool")], c.tool)
		}
		if got := row[toolAuditColumn("target")]; got != c.wantTarget {
			t.Fatalf("row %d (%s) target = %q, want %q", i, c.tool, got, c.wantTarget)
		}
	}
}

func TestToolAuditMigratesOldHeaderWithoutTarget(t *testing.T) {
	t.Setenv(toolAuditEnv, "")
	dir := t.TempDir()
	path := toolAuditPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("seed .ogcode dir: %v", err)
	}
	old := "at,session,agent,tool,status,duration_ms,model,provider,error\n" +
		"2026-11-01T10:00:00Z,s1,build,read,completed,5,m,p,\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatalf("seed old file: %v", err)
	}

	auditToolCall(dir, "s2", "build", "edit", auditTestInput(t, map[string]any{"path": "x.txt"}),
		toolOutcomeCompleted, time.Now(), time.Millisecond, "m", "p", "")

	rows := readAuditRows(t, path)
	if len(rows) != 3 {
		t.Fatalf("got %d CSV rows, want migrated header + old row + new row", len(rows))
	}
	for i, h := range toolAuditHeader {
		if rows[0][i] != h {
			t.Fatalf("migrated header column %d = %q, want %q", i, rows[0][i], h)
		}
	}
	migrated := rows[1]
	if migrated[toolAuditColumn("target")] != "" {
		t.Fatalf("old row target = %q, want empty (pre-target rows stay empty)", migrated[toolAuditColumn("target")])
	}
	if migrated[toolAuditColumn("tool")] != "read" ||
		migrated[toolAuditColumn("status")] != "completed" ||
		migrated[toolAuditColumn("duration_ms")] != "5" {
		t.Fatalf("old row not intact after padding: %v", migrated)
	}
	fresh := rows[2]
	if got := fresh[toolAuditColumn("target")]; got != filepath.Join(dir, "x.txt") {
		t.Fatalf("new row target = %q, want %q", got, filepath.Join(dir, "x.txt"))
	}
}

func TestToolAuditKillSwitch(t *testing.T) {
	t.Setenv(toolAuditEnv, "off")
	dir := t.TempDir()
	auditToolCall(dir, "s1", "build", "bash", nil, toolOutcomeCompleted, time.Now(), time.Millisecond, "m", "p", "")
	if _, err := os.Stat(toolAuditPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("row written despite OGCODE_TOOL_AUDIT=off: %v", err)
	}
}

func TestToolAuditUnwritablePathStaysSilent(t *testing.T) {
	t.Setenv(toolAuditEnv, "")
	// A file where the directory should be: MkdirAll fails, the row is
	// dropped, and neither a panic nor per-call log spam follows.
	path := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	for i := 0; i < 3; i++ {
		auditToolCall(filepath.Join(path, "sub", "more"), "s1", "build", "bash", nil, toolOutcomeError, time.Now(), time.Millisecond, "m", "p", "boom")
	}
}

// The harness below runs a real turn whose single tool call must land in the
// session directory's audit CSV with status and duration filled in.

type auditProbeTool struct{}

func (auditProbeTool) ID() string          { return "grep" }
func (auditProbeTool) Description() string { return "audit probe (test)" }
func (auditProbeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"}}}`)
}
func (auditProbeTool) Execute(_ context.Context, _ json.RawMessage, _ tool.Context) (tool.Result, error) {
	return tool.Result{Title: "grep", Output: "match"}, nil
}

func TestToolCallLandsInAuditCSV(t *testing.T) {
	t.Setenv(toolAuditEnv, "")
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-model", ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	reg.Register(&deltaArgsProvider{})
	tools := tool.NewRegistry()
	tools.Register(auditProbeTool{})

	dir := t.TempDir()
	lr := &LoopRunner{
		Store: store, Bus: bus.New(64), Registry: reg, Tools: tools,
		Dir: dir, MaxSteps: 5,
	}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: dir,
		Title: "t", Model: "mock-model", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	userMsg := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := store.CreateMessage(userMsg); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: "find it"})
	if err := store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: userMsg.ID, SessionID: sess.ID,
		Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}

	rows := readAuditRows(t, toolAuditPath(dir))
	if len(rows) != 2 {
		t.Fatalf("got %d CSV rows, want header + 1 call", len(rows))
	}
	row := rows[1]
	if row[toolAuditColumn("tool")] != "grep" {
		t.Fatalf("tool = %q, want grep", row[toolAuditColumn("tool")])
	}
	if status := row[toolAuditColumn("status")]; status != string(session.ToolCompleted) {
		t.Fatalf("status = %q, want %q", status, session.ToolCompleted)
	}
	if agent := row[toolAuditColumn("agent")]; agent == "" {
		t.Fatal("agent column empty")
	}
	if sessionID := row[toolAuditColumn("session")]; sessionID != string(sess.ID) {
		t.Fatalf("session = %q, want %q", sessionID, string(sess.ID))
	}
	if row[toolAuditColumn("model")] != "mock-model" || row[toolAuditColumn("provider")] != "mock" {
		t.Fatalf("model/provider = %q/%q, want mock-model/mock", row[toolAuditColumn("model")], row[toolAuditColumn("provider")])
	}
	ms, err := strconv.Atoi(row[toolAuditColumn("duration_ms")])
	if err != nil || ms < 0 {
		t.Fatalf("duration_ms = %q, want a non-negative number", row[toolAuditColumn("duration_ms")])
	}
	if err := row[toolAuditColumn("error")]; err != "" {
		t.Fatalf("error column should be empty on success, got %q", err)
	}
}

func readAuditRows(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit CSV: %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatalf("parse audit CSV: %v", err)
	}
	return rows
}

// toolAuditColumn maps a header name to its index, in one place.
func toolAuditColumn(name string) int {
	for i, h := range toolAuditHeader {
		if h == name {
			return i
		}
	}
	return -1
}
