package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadGitignoreRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	body := "# build output\n\ndist/\n\n  node_modules/  \n!keep.md\n# trailing comment\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	rules, truncated, err := readGitignoreRules(path)
	if err != nil {
		t.Fatalf("readGitignoreRules: %v", err)
	}
	if truncated {
		t.Error("short file reported as truncated")
	}

	// Line numbers are the file's own, not the rule's position in the slice:
	// they are only useful if they survive the comments and blanks dropped
	// around them.
	want := []gitignoreRule{
		{Line: 3, Pattern: "dist/"},
		{Line: 5, Pattern: "node_modules/"},
		{Line: 6, Pattern: "!keep.md", Negated: true},
	}
	if len(rules) != len(want) {
		t.Fatalf("got %d rules, want %d: %+v", len(rules), len(want), rules)
	}
	for i, w := range want {
		if rules[i] != w {
			t.Errorf("rule %d = %+v, want %+v", i, rules[i], w)
		}
	}
}

func TestReadGitignoreRulesMissingFile(t *testing.T) {
	_, _, err := readGitignoreRules(filepath.Join(t.TempDir(), ".gitignore"))
	if !os.IsNotExist(err) {
		t.Fatalf("got %v, want a not-exist error so the handler can report the absence", err)
	}
}

func TestReadGitignoreRulesTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	var body []byte
	for i := 0; i < maxGitignoreRules+10; i++ {
		body = append(body, []byte("pattern\n")...)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	rules, truncated, err := readGitignoreRules(path)
	if err != nil {
		t.Fatalf("readGitignoreRules: %v", err)
	}
	if !truncated {
		t.Error("over-long file not reported as truncated — the panel would present a partial list as the whole file")
	}
	if len(rules) != maxGitignoreRules {
		t.Errorf("got %d rules, want the cap %d", len(rules), maxGitignoreRules)
	}
}

func TestFindNestedGitignores(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(".gitignore", "node_modules/\n")
	write("web/.gitignore", "dist/\n")
	// Inside an ignored directory: not reported, because the walk never opens a
	// tree the root file already excludes.
	write("node_modules/pkg/.gitignore", "*.map\n")

	got := findNestedGitignores(root)
	if len(got) != 1 || got[0] != "web/.gitignore" {
		t.Errorf("got %v, want just [web/.gitignore] — the root file is not nested and ignored trees are pruned", got)
	}
}
