package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
