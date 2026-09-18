package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/memfile"
)

// projectScopeCtx carries the recall scope the real runner sets for project
// recall — the summaries are indexed under ProjectID "/proj", which the tool
// takes from the scope rather than from the (temp) session dir.
func projectScopeCtx() context.Context {
	return WithRecallScope(context.Background(), RecallScope{Scope: "project", ProjectID: "/proj"})
}

func newMemoryMapTestStore(t *testing.T) *memfile.Store {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return memfile.NewStore(database)
}

// indexSummary writes and indexes one summary with the given labels, mirroring
// what the real writer + IndexFile produce.
func indexSummary(t *testing.T, s *memfile.Store, dir string, sessionID, title string, labels []string, created time.Time) {
	t.Helper()
	body := "# " + title + "\n"
	if len(labels) > 0 {
		body += "\nTopics: " + strings.Join(labels, ", ") + "\n"
	}
	body += "\n## Request\nbody\n"
	meta := memfile.Meta{SessionID: sessionID, ProjectID: "/proj", SessionType: "build", Title: title, CreatedAt: created}
	path, err := memfile.Write(dir, meta, body)
	if err != nil {
		t.Fatalf("write %q: %v", title, err)
	}
	if err := s.IndexFile(path, meta); err != nil {
		t.Fatalf("index %q: %v", title, err)
	}
}

// Project scope with no subdir must render the codebase_map shape: every
// conversation collapsed to ONE line — tag/, its most common topics ranked by
// frequency, its turn count — and nothing else.
func TestMemoryMap_ProjectScopeCollapsesConversations(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)

	// Three turns in one conversation, one in another.
	indexSummary(t, s, dir, "sessAABBCC", "First", []string{"migrations", "schema"}, base)
	indexSummary(t, s, dir, "sessAABBCC", "Second", []string{"migrations", "tooling"}, base.Add(time.Hour))
	indexSummary(t, s, dir, "sessAABBCC", "Third", []string{"migrations"}, base.Add(2*time.Hour))
	indexSummary(t, s, dir, "sessXXYYZZ", "Solo", []string{"ui"}, base.Add(3*time.Hour))

	res, err := NewMemoryMapTool(s).Execute(projectScopeCtx(), nil, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := res.Output
	if !strings.Contains(out, "4 turns in this project") {
		t.Fatalf("missing total in header:\n%s", out)
	}
	// The conversation line carries the ranked topics and the count.
	if !strings.Contains(out, "sessAABB/  migrations, schema, tooling  (3 turns)") {
		t.Fatalf("missing collapsed line for sessAABBCC:\n%s", out)
	}
	if !strings.Contains(out, "sessXXYY/  ui  (1 turn)") {
		t.Fatalf("missing collapsed line for sessXXYYZZ:\n%s", out)
	}
	// Collapsed means collapsed: no outline, no path at this level.
	if strings.Contains(out, "## Request") || strings.Contains(out, ".md") {
		t.Fatalf("project level leaked per-turn detail:\n%s", out)
	}
	if strings.Contains(out, "Conversations end in \"/\"") == false {
		t.Fatalf("missing drilldown hint:\n%s", out)
	}
}

// The subdir drilldown lists one conversation's summaries as codebase_map file
// lines: file name (timestamp--tag--slug) and its topic labels. No title, no
// "Topics:" block, no path, and no outline — the outline is file_map's job.
func TestMemoryMap_SubdirDrillsIntoConversation(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	indexSummary(t, s, dir, "sessAABBCC", "First", []string{"migrations"}, base)
	indexSummary(t, s, dir, "sessXXYYZZ", "Other", []string{"ui"}, base.Add(time.Hour))

	args, _ := json.Marshal(map[string]any{"subdir": "sessAABB"})
	res, err := NewMemoryMapTool(s).Execute(projectScopeCtx(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := res.Output
	if !strings.Contains(out, "1 turn in conversation sessAABB") {
		t.Fatalf("missing drilldown header:\n%s", out)
	}
	// The codebase_map file-line shape: name and labels on one line.
	if !strings.Contains(out, "2026-09-09T100000Z--sessAABB--first.md  migrations") {
		t.Fatalf("drilldown missing the file line for the turn:\n%s", out)
	}
	// Collapsed means collapsed: no old-style detail lines, no other conversation.
	if strings.Contains(out, "Topics:") || strings.Contains(out, "## Request") ||
		strings.Contains(out, "other") || strings.Contains(out, "sessXXYY") {
		t.Fatalf("drilldown leaked outline or another conversation:\n%s", out)
	}
}

// An unknown subdir is an error the model can act on, not an empty success.
func TestMemoryMap_SubdirUnknownTagFailsCleanly(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	indexSummary(t, s, dir, "sessAABBCC", "First", []string{"migrations"}, time.Now())

	args, _ := json.Marshal(map[string]any{"subdir": "sessNOPE"})
	res, err := NewMemoryMapTool(s).Execute(projectScopeCtx(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "No conversation with tag") {
		t.Fatalf("expected a clean not-found, got:\n%s", res.Output)
	}
}

// The topic filter keeps only entries whose labels match, case-insensitively.
func TestMemoryMap_TopicFilter(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	indexSummary(t, s, dir, "sessAABBCC", "Migrated", []string{"SQLite Migration"}, base)
	indexSummary(t, s, dir, "sessAABBCC", "Untouched", []string{"ui polish"}, base.Add(time.Hour))

	args, _ := json.Marshal(map[string]any{"topic": "migration"})
	res, err := NewMemoryMapTool(s).Execute(projectScopeCtx(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := res.Output
	if !strings.Contains(out, "sessAABB/  SQLite Migration  (1 turn)") || strings.Contains(out, "ui polish") {
		t.Fatalf("topic filter wrong:\n%s", out)
	}

	args, _ = json.Marshal(map[string]any{"topic": "zzz-nothing"})
	res, err = NewMemoryMapTool(s).Execute(projectScopeCtx(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "match topic") {
		t.Fatalf("empty topic match should explain itself:\n%s", res.Output)
	}
}

// Session-scoped recall stays flat: that conversation's summaries, one
// codebase_map file line each.
func TestMemoryMap_SessionScopeStaysFlat(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	indexSummary(t, s, dir, "sessAABBCC", "First", []string{"migrations"}, base)
	indexSummary(t, s, dir, "sessAABBCC", "Second", []string{"tooling"}, base.Add(time.Hour))
	indexSummary(t, s, dir, "sessXXYYZZ", "Other", []string{"ui"}, base.Add(2*time.Hour))

	ctx := WithRecallScope(context.Background(), RecallScope{Scope: "session", SessionID: "sessAABBCC", ProjectID: "/proj"})
	res, err := NewMemoryMapTool(s).Execute(ctx, nil, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := res.Output
	if !strings.Contains(out, "2 turns in this conversation") {
		t.Fatalf("missing flat header:\n%s", out)
	}
	if !strings.Contains(out, "--sessAABB--first.md  migrations") || !strings.Contains(out, "--sessAABB--second.md  tooling") {
		t.Fatalf("flat render missing file lines:\n%s", out)
	}
	// No collapsed lines, no other conversation's summaries, no outline.
	if strings.Contains(out, "sessAABB/") || strings.Contains(out, "other") ||
		strings.Contains(out, "## Request") {
		t.Fatalf("session scope leaked other data, collapsed lines, or outlines:\n%s", out)
	}
}

// Session scope with a foreign subdir is refused, not silently widened.
func TestMemoryMap_SessionScopeRejectsForeignSubdir(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	indexSummary(t, s, dir, "sessAABBCC", "First", []string{"migrations"}, time.Now())

	ctx := WithRecallScope(context.Background(), RecallScope{Scope: "session", SessionID: "sessAABBCC", ProjectID: "/proj"})
	args, _ := json.Marshal(map[string]any{"subdir": "sessXXYYZZ"})
	res, err := NewMemoryMapTool(s).Execute(ctx, args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "scoped to conversation") {
		t.Fatalf("expected scope refusal, got:\n%s", res.Output)
	}
}
