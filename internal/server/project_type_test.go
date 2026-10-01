package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/indexer"
)

// writeFiles creates each named file (with an empty body) under dir, making
// parent directories as needed.
func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectProjectType(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"go project", []string{"main.go", "internal/a.go", "README.md"}, "go"},
		{"typescript majority", []string{"a.ts", "b.tsx", "c.ts", "tool.go"}, "typescript"},
		{"python", []string{"app.py", "pkg/util.pyi"}, "python"},
		{"rust", []string{"src/main.rs", "Cargo.toml"}, "rust"},
		// Only extensions a project is written in count: a README and a JSON
		// blob are not a project type.
		{"no source", []string{"README.md", "data.json", "Makefile"}, ""},
		{"empty", nil, ""},
		// A tie breaks alphabetically, not by map order.
		{"tie breaks alphabetically", []string{"main.go", "app.py"}, "go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, tc.files...)
			if got := detectProjectType(dir); got != tc.want {
				t.Errorf("detectProjectType = %q, want %q", got, tc.want)
			}
		})
	}
}

// Dependency and build directories must not vote: a vendored bundle of another
// language is not the project's type.
func TestDetectProjectTypeIgnoresDependenciesAndBuildOutput(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir,
		"app.py",
		"node_modules/left-pad/index.js",
		"node_modules/other.js",
		"dist/bundle.js",
		"vendor/lib.go",
	)
	if got := detectProjectType(dir); got != "python" {
		t.Errorf("detectProjectType = %q, want %q", got, "python")
	}
}

// ogcode's own state directory is never part of the project.
func TestDetectProjectTypeIgnoresStateDir(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "app.py", ".ogcode/worktrees/task/main.go")
	if got := detectProjectType(dir); got != "python" {
		t.Errorf("detectProjectType = %q, want %q", got, "python")
	}
}

func TestDetectProjectTypeEmptyDir(t *testing.T) {
	if got := detectProjectType(""); got != "" {
		t.Errorf("detectProjectType(\"\") = %q, want \"\"", got)
	}
}

func TestReportProjectTypeOnceEmitsWithTheServerId(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "main.go", "pkg/a.go")
	var calls []captureCall
	capture := func(event, distinctID string, props map[string]any) {
		calls = append(calls, captureCall{event, distinctID, props})
	}

	reportProjectTypeOnce(dir, capture)
	if len(calls) != 1 {
		t.Fatalf("capture called %d times, want 1", len(calls))
	}
	if calls[0].event != projectTypeEvent {
		t.Errorf("event = %q, want %q", calls[0].event, projectTypeEvent)
	}
	if calls[0].distinctID != posthogDistinctID() {
		t.Errorf("distinctID = %q, want %q", calls[0].distinctID, posthogDistinctID())
	}
	if calls[0].props["type"] != "go" {
		t.Errorf("props[type] = %v, want %q", calls[0].props["type"], "go")
	}

	// The marker scopes the report to this project and suppresses later calls.
	marker := filepath.Join(dir, indexer.StateDirName, projectReportedFilename)
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker not written: %v", err)
	}
	reportProjectTypeOnce(dir, capture)
	if len(calls) != 1 {
		t.Errorf("capture called %d times after the marker, want 1", len(calls))
	}
}

func TestReportProjectTypeOnceNoOps(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	capture := func(string, string, map[string]any) { calls++ }

	reportProjectTypeOnce("", capture)
	reportProjectTypeOnce(dir, nil)
	if calls != 0 {
		t.Errorf("capture called %d times for no-op inputs, want 0", calls)
	}
}

// A tree with no recognisable source reports nothing and leaves no marker, so
// a later start — once the project has source — still reports.
func TestReportProjectTypeOnceDefersWhenNoSource(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, "README.md")
	calls := 0
	capture := func(string, string, map[string]any) { calls++ }

	reportProjectTypeOnce(dir, capture)
	if calls != 0 {
		t.Errorf("capture called %d times with no source, want 0", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, indexer.StateDirName, projectReportedFilename)); err == nil {
		t.Errorf("marker written despite no report")
	}

	writeFiles(t, dir, "main.go")
	reportProjectTypeOnce(dir, capture)
	if calls != 1 {
		t.Errorf("capture called %d times after source appeared, want 1", calls)
	}
}
