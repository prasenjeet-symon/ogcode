package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRootIsGlobalUnlessOverridden(t *testing.T) {
	t.Setenv("OGCODE_LOG_DIR", "")
	if got, want := Root("/home/u"), filepath.Join("/home/u", ".ogcode", "logs"); got != want {
		t.Errorf("Root = %q, want %q", got, want)
	}
	t.Setenv("OGCODE_LOG_DIR", "/var/log/ogcode")
	if got := Root("/home/u"); got != "/var/log/ogcode" {
		t.Errorf("Root = %q, want the OGCODE_LOG_DIR override", got)
	}
}

// One project has one log folder however its path is spelled, the folder is
// named after the project, and two projects with the same name stay apart.
func TestProjectDirIsStableAndDistinct(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "one", "app")
	b := filepath.Join(base, "two", "app")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root := "/logs"

	got := ProjectDir(root, a)
	if filepath.Dir(got) != root {
		t.Errorf("ProjectDir(%q) = %q, want a folder directly under %q", a, got, root)
	}
	if name := filepath.Base(got); !strings.HasPrefix(name, "app-") || len(name) != len("app-")+8 {
		t.Errorf("folder %q should be the project name plus an 8-character hash", name)
	}
	if again := ProjectDir(root, a+string(filepath.Separator)); again != got {
		t.Errorf("a trailing separator moved the folder: %q vs %q", again, got)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(a, link); err == nil {
		if via := ProjectDir(root, link); via != got {
			t.Errorf("a symlinked path moved the folder: %q vs %q", via, got)
		}
	}
	if other := ProjectDir(root, b); other == got {
		t.Errorf("two projects named app share the folder %q", got)
	}
}

func TestFolderNameIsSafe(t *testing.T) {
	for in, want := range map[string]string{
		"oglab":          "oglab",
		"My Project (2)": "My-Project--2",
		".dotfiles":      "dotfiles",
		"/":              "project",
		"日本":             "project",
		"a:b*c?d":        "a-b-c-d",
	} {
		if got := folderName(in); got != want {
			t.Errorf("folderName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := folderName(strings.Repeat("x", 200)); len(got) > 48 {
		t.Errorf("folderName kept %d characters, want at most 48", len(got))
	}
}

func TestProjectMaxAgeParsing(t *testing.T) {
	for env, want := range map[string]time.Duration{
		"":     projectMaxAgeDefault, // unset means the default
		"0":    0,                    // never prune
		"7":    7 * 24 * time.Hour,
		"abc":  projectMaxAgeDefault, // garbage falls back
		"-3":   projectMaxAgeDefault, // so does negative
		" 30 ": 30 * 24 * time.Hour,
	} {
		t.Setenv("OGCODE_LOG_PROJECT_MAX_AGE_DAYS", env)
		if got := ProjectMaxAge(); got != want {
			t.Errorf("ProjectMaxAge with %q = %v, want %v", env, got, want)
		}
	}
}

// PruneProjects removes only whole folders whose newest file is older than
// maxAge — across the tree, since one fresh file keeps its whole folder —
// and leaves symlinks, non-directories and root itself alone. maxAge 0 is a
// no-op.
func TestPruneProjectsRemovesStaleFoldersOnly(t *testing.T) {
	root := t.TempDir()
	put := func(rel string, when time.Time) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
	ancient := time.Now().Add(-120 * 24 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	put("gone-app-1234/ogcode.log", ancient)
	put("live-app-5678/ogcode.log", ancient)
	put("live-app-5678/ogcode-2.log", recent) // newest file wins
	put("nested-gone-90ab/a/b/ogcode.log", ancient)
	put("nested-live-cdef/a/old.log", ancient)
	put("nested-live-cdef/b/new.log", recent) // fresh file in a sibling keeps the folder
	put("empty-app-4455/.keep", ancient)
	put("stray.log", ancient) // a file at the root is not a project folder
	if err := os.Symlink(filepath.Join(root, "live-app-5678"), filepath.Join(root, "link-to-live")); err != nil {
		t.Fatal(err)
	}

	const maxAge = 90 * 24 * time.Hour
	if removed, err := PruneProjects(root, 0); removed != 0 || err != nil {
		t.Fatalf("PruneProjects with maxAge 0 removed %d folders (%v), want none", removed, err)
	}

	removed, err := PruneProjects(root, maxAge)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Errorf("removed %d folders, want 3 (the stale, the stale-nested, the empty)", removed)
	}
	for _, gone := range []string{"gone-app-1234", "nested-gone-90ab", "empty-app-4455"} {
		if exists(filepath.Join(root, gone)) {
			t.Errorf("stale folder %s survived", gone)
		}
	}
	for _, kept := range []string{
		"live-app-5678/ogcode.log",
		"live-app-5678/ogcode-2.log",
		"nested-live-cdef/a/old.log",
		"nested-live-cdef/b/new.log",
		"stray.log",
		"link-to-live",
	} {
		if !exists(filepath.Join(root, kept)) {
			t.Errorf("kept %s, but it is gone", kept)
		}
	}
}

// A root that cannot be read is an error, not silence.
func TestPruneProjectsReportsUnreadableRoot(t *testing.T) {
	if _, err := PruneProjects(filepath.Join(t.TempDir(), "missing"), time.Hour); err == nil {
		t.Error("pruning a missing root reported no error")
	}
}
