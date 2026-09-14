package worker

import (
	"path/filepath"
	"testing"
)

func TestParseSessionID(t *testing.T) {
	cases := []struct {
		name  string
		props string
		want  string
	}{
		{"present", `{"sessionId":"ses_abc","other":1}`, "ses_abc"},
		{"absent", `{"other":1}`, ""},
		{"empty", ``, ""},
		{"malformed", `{not json`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseSessionID([]byte(tc.props)); got != tc.want {
				t.Fatalf("parseSessionID(%q) = %q, want %q", tc.props, got, tc.want)
			}
		})
	}
}

func TestDiscoverWorkspacesReportsRoots(t *testing.T) {
	dir := t.TempDir()
	ws := discoverWorkspaces([]string{dir})

	var found *workspaceView
	for _, w := range ws {
		if w.GetPath() == dir {
			found = &workspaceView{name: w.GetName(), present: w.GetPresent()}
		}
	}
	if found == nil {
		t.Fatalf("configured root %q not reported; got %d workspaces", dir, len(ws))
	}
	if !found.present {
		t.Fatal("existing directory should be marked present")
	}
	if found.name != filepath.Base(dir) {
		t.Fatalf("name: got %q want %q", found.name, filepath.Base(dir))
	}
}

func TestDiscoverWorkspacesAbsentPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	ws := discoverWorkspaces([]string{missing})
	if len(ws) == 0 {
		t.Fatal("absent path should still be reported (as not present)")
	}
	for _, w := range ws {
		if w.GetPath() == missing && w.GetPresent() {
			t.Fatal("absent path must be marked not present")
		}
	}
}

func TestDiscoverWorkspacesDeduplicates(t *testing.T) {
	dir := t.TempDir()
	ws := discoverWorkspaces([]string{dir, dir})
	count := 0
	for _, w := range ws {
		if w.GetPath() == dir {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected root reported once, got %d", count)
	}
}

type workspaceView struct {
	name    string
	present bool
}
