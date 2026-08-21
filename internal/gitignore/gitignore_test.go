package gitignore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree materialises a set of files (and the directories implied by their
// paths) under a fresh temp dir, and returns it.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestMatch_CoreSyntax(t *testing.T) {
	root := writeTree(t, map[string]string{
		".gitignore": strings.Join([]string{
			"# a comment",
			"",
			"*.log",
			"build/",
			"/only-at-root.txt",
			"docs/*.tmp",
			"deep/**/generated",
			"!keep.log",
			"weird[0-9].txt",
			"space\\ name.txt",
		}, "\n"),
	})
	m := New(root)

	for _, tc := range []struct {
		path  string
		isDir bool
		want  bool
		why   string
	}{
		{"app.log", false, true, "*.log at root"},
		{"nested/deep/app.log", false, true, "an unanchored pattern floats to any depth"},
		{"keep.log", false, false, "a later negation re-includes it"},
		{"build", true, true, "trailing slash matches the directory"},
		{"build/out.o", false, true, "everything under an ignored directory"},
		{"src/build", true, true, "unanchored, so it matches at any depth"},
		{"only-at-root.txt", false, true, "leading slash anchors to the gitignore's dir"},
		{"sub/only-at-root.txt", false, false, "and so does not match deeper"},
		{"docs/notes.tmp", false, true, "a slash mid-pattern anchors it"},
		{"docs/sub/notes.tmp", false, false, "* does not cross a separator"},
		{"deep/generated", true, true, "**/ matches zero directories"},
		{"deep/a/b/generated", true, true, "**/ matches several"},
		{"weird7.txt", false, true, "character class"},
		{"weirdX.txt", false, false, "outside the class"},
		{"space name.txt", false, true, "an escaped space is part of the name"},
		{"src/main.go", false, false, "an ordinary file"},
	} {
		if got := m.Match(tc.path, tc.isDir); got != tc.want {
			t.Errorf("Match(%q, dir=%v) = %v, want %v — %s", tc.path, tc.isDir, got, tc.want, tc.why)
		}
	}
}

// A .gitignore deeper in the tree overrides a shallower one, which is what lets
// a subdirectory re-include something the root excluded.
func TestMatch_NestedGitignoreOverrides(t *testing.T) {
	root := writeTree(t, map[string]string{
		".gitignore":            "*.log\n",
		"service/.gitignore":    "!important.log\n",
		"service/important.log": "",
		"service/other.log":     "",
		"other.log":             "",
	})
	m := New(root)

	if m.Match("service/important.log", false) {
		t.Error("a nested negation did not override the root pattern")
	}
	if !m.Match("service/other.log", false) {
		t.Error("the root pattern should still apply to everything else")
	}
	if !m.Match("other.log", false) {
		t.Error("the root pattern stopped applying at the root")
	}
}

func TestMatch_PathsOutsideTheRoot(t *testing.T) {
	root := writeTree(t, map[string]string{".gitignore": "*.log\n"})
	m := New(root)

	if m.Match(filepath.Join(root, "..", "elsewhere", "app.log"), false) {
		t.Error("a path outside the root was matched")
	}
	if !m.Match(filepath.Join(root, "app.log"), false) {
		t.Error("an absolute path inside the root was not matched")
	}
}

func TestMatch_NoGitignoreAnywhere(t *testing.T) {
	root := writeTree(t, map[string]string{"src/main.go": ""})
	if New(root).Match("src/main.go", false) {
		t.Error("a tree with no .gitignore ignored something")
	}
}

// The reference test. Everything above encodes what I believe git does; this
// asks git. Any disagreement is this package being wrong, because the whole
// value of reading .gitignore is that the answers match the tool that wrote it.
func TestMatch_AgreesWithGitCheckIgnore(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}

	files := map[string]string{
		".gitignore": strings.Join([]string{
			"*.log",
			"!keep.log",
			"build/",
			"/root-only.txt",
			"docs/*.tmp",
			"deep/**/generated",
			"**/anywhere.bin",
			"node_modules/",
			"*.o",
			"!src/keep.o",
			"trailing   ",
			"a?c.txt",
			"[Dd]ebug/",
		}, "\n"),
		"pkg/.gitignore": "!nested-keep.log\n*.local\n",
	}
	// The candidate paths, and whether each is a directory.
	candidates := []struct {
		path  string
		isDir bool
	}{
		{"app.log", false}, {"keep.log", false}, {"sub/app.log", false},
		{"build", true}, {"build/out.o", false}, {"src/build", true},
		{"root-only.txt", false}, {"sub/root-only.txt", false},
		{"docs/x.tmp", false}, {"docs/sub/x.tmp", false},
		{"deep/generated", true}, {"deep/a/b/generated", true},
		{"anywhere.bin", false}, {"x/y/anywhere.bin", false},
		{"node_modules", true}, {"node_modules/pkg/index.js", false},
		{"src/main.o", false}, {"src/keep.o", false},
		{"a-c.txt", false}, {"abc.txt", false}, {"abbc.txt", false},
		// "Debug" and "debug" only; an all-caps third case would test the
		// filesystem rather than the matcher, since macOS folds case and the two
		// directories collide into one before git ever sees them.
		{"Debug", true}, {"debug", true},
		{"pkg/nested-keep.log", false}, {"pkg/thing.local", false},
		{"src/main.go", false}, {"README.md", false},
	}

	root := writeTree(t, files)
	for _, c := range candidates {
		full := filepath.Join(root, filepath.FromSlash(c.path))
		if c.isDir {
			_ = os.MkdirAll(full, 0o755)
		} else {
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			_ = os.WriteFile(full, []byte("x"), 0o644)
		}
	}
	run := func(args ...string) {
		cmd := exec.Command(gitPath, args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")

	m := New(root)
	for _, c := range candidates {
		// git check-ignore exits 0 when the path is ignored, 1 when it is not.
		cmd := exec.Command(gitPath, "check-ignore", "-q", "--no-index", "--", c.path)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		err := cmd.Run()
		wantIgnored := err == nil

		if got := m.Match(c.path, c.isDir); got != wantIgnored {
			t.Errorf("%-32s (dir=%-5v) ours=%-5v git=%v", c.path, c.isDir, got, wantIgnored)
		}
	}
}

// Git does not treat its own metadata directory as part of the working tree, so
// no rule applies to it and none can re-include it. Modelling that here is what
// lets a caller drop every other hardcoded exclusion and still not walk into a
// repository's object store.
func TestMatch_GitDirectoryIsAlwaysIgnored(t *testing.T) {
	root := writeTree(t, map[string]string{
		".gitignore": "!.git\n!.git/**\n", // even an explicit attempt to re-include
	})
	m := New(root)

	for _, p := range []string{".git", ".git/config", ".git/objects/ab/cdef", "sub/.git/config"} {
		isDir := p == ".git"
		if !m.Match(p, isDir) {
			t.Errorf("Match(%q) = false; the git directory must never be walked", p)
		}
	}
	if m.Match("src/gitignore-notes.md", false) {
		t.Error("a path merely containing 'git' was excluded")
	}
}
