package memfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readSessionMemory(t *testing.T, dir, id string) string {
	t.Helper()
	b, err := os.ReadFile(SessionMemoryPath(dir, id))
	if err != nil {
		t.Fatalf("read session memory: %v", err)
	}
	return string(b)
}

func TestAppendDeepSearchesCreatesTheSection(t *testing.T) {
	dir := t.TempDir()
	id := "ses_01TEST"
	at := time.Date(2026, 11, 2, 14, 5, 0, 0, time.UTC)

	changed, err := AppendDeepSearches(dir, id, []DeepSearchNote{
		{Query: "ogcode web cache design", At: at},
		{Query: "sqlite fts5 external content", Cached: true, At: at},
	})
	if err != nil {
		t.Fatalf("AppendDeepSearches: %v", err)
	}
	if !changed {
		t.Fatal("a fresh note must report the file as changed")
	}

	got := readSessionMemory(t, dir, id)
	for _, want := range []string{
		"# Session " + SessionKey(id),
		DeepSearchHeading,
		`- 2026-11-02 14:05 — "ogcode web cache design" (live research)`,
		`- 2026-11-02 14:05 — "sqlite fts5 external content" (cached answer)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("session memory is missing %q; got:\n%s", want, got)
		}
	}
}

func TestAppendDeepSearchesKeepsTheAgentsOwnMemory(t *testing.T) {
	dir := t.TempDir()
	id := "ses_01TEST"
	path := SessionMemoryPath(dir, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := "# Session " + SessionKey(id) + "\n\n- Task: build the thing\n  Command every time: `make build`\n\n## Host server\n\n- not restarted\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	at := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	if _, err := AppendDeepSearches(dir, id, []DeepSearchNote{{Query: "incus worker isolation", At: at}}); err != nil {
		t.Fatalf("AppendDeepSearches: %v", err)
	}

	got := readSessionMemory(t, dir, id)
	if !strings.HasPrefix(got, "# Session "+SessionKey(id)+"\n\n- Task: build the thing\n") {
		t.Errorf("the agent's own memory must survive untouched at the top; got:\n%s", got)
	}
	if !strings.Contains(got, "## Host server\n\n- not restarted") {
		t.Errorf("an existing section must survive unchanged; got:\n%s", got)
	}
	if !strings.Contains(got, `"incus worker isolation"`) {
		t.Errorf("the note was not appended; got:\n%s", got)
	}
	// The note belongs in its own section, after the agent's own.
	if strings.Index(got, DeepSearchHeading) < strings.Index(got, "## Host server") {
		t.Errorf("the note section was inserted ahead of the agent's own sections; got:\n%s", got)
	}

	// Appending into a section that already exists must extend it in place, not
	// open a second heading with the same name.
	if _, err := AppendDeepSearches(dir, id, []DeepSearchNote{{Query: "ogx token metering", Cached: true, At: at.Add(time.Hour)}}); err != nil {
		t.Fatalf("second AppendDeepSearches: %v", err)
	}
	got = readSessionMemory(t, dir, id)
	if n := strings.Count(got, DeepSearchHeading); n != 1 {
		t.Errorf("the section heading appears %d times, want 1; got:\n%s", n, got)
	}
	if !strings.Contains(got, `"ogx token metering" (cached answer)`) {
		t.Errorf("a replayed answer must be marked as such; got:\n%s", got)
	}
}

func TestAppendDeepSearchesDedupesAndCaps(t *testing.T) {
	dir := t.TempDir()
	id := "ses_01TEST"
	at := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)

	if _, err := AppendDeepSearches(dir, id, []DeepSearchNote{{Query: "same question", At: at}}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	changed, err := AppendDeepSearches(dir, id, []DeepSearchNote{{Query: "same question", At: at.Add(time.Hour)}})
	if err != nil {
		t.Fatalf("second append: %v", err)
	}
	if changed {
		t.Error("a query already listed must not be recorded again")
	}

	notes := make([]DeepSearchNote, 0, deepSearchNoteCap+5)
	for i := 0; i < deepSearchNoteCap+5; i++ {
		notes = append(notes, DeepSearchNote{Query: "question " + string(rune('a'+i)), At: at})
	}
	if _, err := AppendDeepSearches(dir, id, notes); err != nil {
		t.Fatalf("bulk append: %v", err)
	}

	got := readSessionMemory(t, dir, id)
	bullets := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "- ") && strings.Contains(line, `"question `) {
			bullets++
		}
	}
	if bullets > deepSearchNoteCap {
		t.Errorf("the section keeps %d bullets, cap is %d:\n%s", bullets, deepSearchNoteCap, got)
	}
	if !strings.Contains(got, `"question `+string(rune('a'+deepSearchNoteCap+4))+`"`) {
		t.Errorf("the newest note was dropped; got:\n%s", got)
	}
	if strings.Contains(got, `"same question"`) {
		t.Errorf("the oldest note should have been dropped past the cap; got:\n%s", got)
	}
}
