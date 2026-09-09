package memfile

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return NewStore(database)
}

// A lexical sort of turn filenames must be chronological, so the folder reads
// oldest→newest without opening any file.
func TestFilenameChronological(t *testing.T) {
	base := time.Date(2026, 9, 9, 14, 30, 5, 0, time.UTC)
	times := []time.Time{
		base.Add(2 * time.Hour),
		base,
		base.Add(24 * time.Hour),
		base.Add(1 * time.Minute),
	}
	var names []string
	for _, ts := range times {
		names = append(names, Filename(ts, "sess1234", "Some Turn Title"))
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)

	// Sorted-by-name must equal sorted-by-time.
	byTime := append([]time.Time(nil), times...)
	sort.Slice(byTime, func(i, j int) bool { return byTime[i].Before(byTime[j]) })
	for i, ts := range byTime {
		want := Filename(ts, "sess1234", "Some Turn Title")
		if sorted[i] != want {
			t.Fatalf("position %d: lexical order != chronological order\n got %q\nwant %q", i, sorted[i], want)
		}
	}
}

func TestFilenameShape(t *testing.T) {
	ts := time.Date(2026, 9, 9, 14, 30, 5, 0, time.UTC)
	got := Filename(ts, "abc123def456", "Wire the Recall Agent!")
	if !strings.HasPrefix(got, "2026-09-09T143005Z--abc123de--") {
		t.Fatalf("unexpected prefix: %q", got)
	}
	if !strings.HasSuffix(got, "wire-the-recall-agent.md") {
		t.Fatalf("unexpected slug/suffix: %q", got)
	}
}

func TestWriteFrontmatterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	meta := Meta{
		SessionID:   "s1",
		ProjectID:   "/proj",
		SessionType: "build",
		Title:       "My Turn",
		CreatedAt:   time.Date(2026, 9, 9, 14, 30, 5, 0, time.UTC),
	}
	body := "# My Turn\n\n## Request\nDo the thing.\n"
	path, err := Write(dir, meta, body)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if filepath.Dir(path) != MemoryDir(dir) {
		t.Fatalf("file written outside memory dir: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	content := string(raw)
	for _, want := range []string{
		"session_id: \"s1\"",
		"project_id: \"/proj\"",
		"session_type: \"build\"",
		"title: \"My Turn\"",
		"created_at: 2026-09-09T14:30:05Z",
		"# My Turn",
		"Do the thing.",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("written file missing %q\n---\n%s", want, content)
		}
	}
}

// Two summaries written in the same second must not clobber each other.
func TestWriteCollisionSuffix(t *testing.T) {
	dir := t.TempDir()
	meta := Meta{SessionID: "s1", Title: "Same", CreatedAt: time.Date(2026, 9, 9, 14, 30, 5, 0, time.UTC)}
	p1, err := Write(dir, meta, "# Same\nbody one\n")
	if err != nil {
		t.Fatalf("write 1: %v", err)
	}
	p2, err := Write(dir, meta, "# Same\nbody two\n")
	if err != nil {
		t.Fatalf("write 2: %v", err)
	}
	if p1 == p2 {
		t.Fatalf("collision not de-duped: both wrote %s", p1)
	}
}

func TestIndexFileAndScopedLists(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()

	older := Meta{SessionID: "sessA", ProjectID: "/proj", SessionType: "build", Title: "Older Turn", CreatedAt: time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)}
	newer := Meta{SessionID: "sessB", ProjectID: "/proj", SessionType: "build", Title: "Newer Turn", CreatedAt: time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)}

	pOld, err := Write(dir, older, "# Older Turn\n\n## Request\nfirst\n\n## Outcome\ndone first\n")
	if err != nil {
		t.Fatalf("write older: %v", err)
	}
	pNew, err := Write(dir, newer, "# Newer Turn\n\n## Request\nsecond\n")
	if err != nil {
		t.Fatalf("write newer: %v", err)
	}
	if err := s.IndexFile(pOld, older); err != nil {
		t.Fatalf("index older: %v", err)
	}
	if err := s.IndexFile(pNew, newer); err != nil {
		t.Fatalf("index newer: %v", err)
	}

	// Newest first across the project.
	proj, err := s.ListByProject("/proj")
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(proj) != 2 {
		t.Fatalf("expected 2 project entries, got %d", len(proj))
	}
	if proj[0].Title != "Newer Turn" || proj[1].Title != "Older Turn" {
		t.Fatalf("wrong order: %q then %q", proj[0].Title, proj[1].Title)
	}
	// Outline was extracted from the markdown headings.
	if !strings.Contains(proj[1].Outline, "Request") || !strings.Contains(proj[1].Outline, "Outcome") {
		t.Errorf("older outline missing headings: %q", proj[1].Outline)
	}

	// Session scope filters to one conversation.
	sessB, err := s.ListBySession("sessB")
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(sessB) != 1 || sessB[0].Title != "Newer Turn" {
		t.Fatalf("session filter wrong: %+v", sessB)
	}

	// IsIndexed reflects existence; re-indexing the same file stays at one row.
	if ok, _ := s.IsIndexed(pNew); !ok {
		t.Errorf("expected %s to be indexed", pNew)
	}
	if err := s.IndexFile(pNew, newer); err != nil {
		t.Fatalf("re-index: %v", err)
	}
	if again, _ := s.ListByProject("/proj"); len(again) != 2 {
		t.Fatalf("re-index changed row count: %d", len(again))
	}
}

func TestPurgeMissing(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	meta := Meta{SessionID: "s", ProjectID: "/proj", SessionType: "build", Title: "Gone", CreatedAt: time.Now()}
	p, err := Write(dir, meta, "# Gone\nbody\n")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := s.IndexFile(p, meta); err != nil {
		t.Fatalf("index: %v", err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := s.PurgeMissing("/proj"); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if left, _ := s.ListByProject("/proj"); len(left) != 0 {
		t.Fatalf("expected purge to drop the row, %d left", len(left))
	}
}

// Wait must block while a summary is in flight and return once it lands.
func TestManagerWaitBlocksUntilDone(t *testing.T) {
	m := NewManager()
	m.Begin("proj")

	done := make(chan struct{})
	go func() {
		m.Wait("proj")
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Wait returned while a summary was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	m.Done("proj")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after Done")
	}

	// A different project is never blocked by this one.
	m.Begin("proj")
	other := make(chan struct{})
	go func() { m.Wait("other"); close(other) }()
	select {
	case <-other:
	case <-time.After(time.Second):
		t.Fatal("Wait on an idle project blocked")
	}
	m.Done("proj")
}
