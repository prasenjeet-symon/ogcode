package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// writeSkill creates <root>/<name>/SKILL.md with the given body and returns the
// skill directory.
func writeSkill(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParse_ReadsFrontmatterAndBody(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "git-release", `---
name: git-release
description: Draft release notes and tag the release.
license: MIT
---

## Steps

1. Read the commits.
`)
	s, err := Load(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.Name != "git-release" {
		t.Errorf("name = %q", s.Name)
	}
	if s.Description != "Draft release notes and tag the release." {
		t.Errorf("description = %q", s.Description)
	}
	if !strings.HasPrefix(s.Content, "## Steps") {
		t.Errorf("body should start after the frontmatter, got %q", s.Content)
	}
	if strings.Contains(s.Content, "license") {
		t.Error("frontmatter leaked into the body")
	}
	if s.Dir != dir {
		t.Errorf("dir = %q, want %q", s.Dir, dir)
	}
}

// The name is a lookup key the model types back verbatim. A file that does not
// supply a usable one cannot be listed or loaded, so it is rejected outright
// rather than registered under something guessed.
func TestParse_RejectsUnusableNames(t *testing.T) {
	cases := []struct {
		label string
		front string
	}{
		{"no frontmatter", "# Just markdown\n"},
		{"unterminated frontmatter", "---\nname: thing\n"},
		{"no name", "---\ndescription: a thing\n---\nbody\n"},
		{"uppercase", "---\nname: Git-Release\n---\nbody\n"},
		{"underscores", "---\nname: git_release\n---\nbody\n"},
		{"double hyphen", "---\nname: git--release\n---\nbody\n"},
		{"leading hyphen", "---\nname: -release\n---\nbody\n"},
		{"too long", "---\nname: " + strings.Repeat("a", MaxNameLen+1) + "\n---\nbody\n"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if _, err := parseContent([]byte(tc.front)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// The frontmatter name, the directory on disk, and the name the model passes to
// the skill tool have to be one string. A mismatch is a lookup that silently
// finds nothing, so it fails at parse time where it can be reported.
func TestParse_RequiresNameToMatchDirectory(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "release", `---
name: git-release
---
body
`)
	_, err := Load(filepath.Join(dir, Filename))
	if err == nil {
		t.Fatal("expected a mismatch error")
	}
	if !strings.Contains(err.Error(), "does not match its directory") {
		t.Errorf("error should name the mismatch, got %v", err)
	}
}

// An over-long description is clamped rather than rejected: it only feeds the
// prompt listing, and losing the whole skill over its length would cost the user
// the skill to fix a formatting problem.
func TestParse_ClampsLongDescription(t *testing.T) {
	s, err := parseContent([]byte("---\nname: verbose\ndescription: " + strings.Repeat("x", MaxDescriptionLen+500) + "\n---\nbody\n"))
	if err != nil {
		t.Fatalf("a long description must not reject the skill: %v", err)
	}
	if len([]rune(s.Description)) > MaxDescriptionLen+1 { // +1 for the ellipsis
		t.Errorf("description not clamped: %d runes", len([]rune(s.Description)))
	}
}

func TestParseFrontmatter_Subset(t *testing.T) {
	fields := parseFrontmatter(`name: demo
quoted: "a: value with a colon"
single: 'it''s quoted'
folded: >
  first line
  second line
literal: |
  line one
  line two
metadata:
  audience: maintainers
  workflow: github
after: still-parsed`)

	want := map[string]string{
		"name":     "demo",
		"quoted":   "a: value with a colon",
		"single":   "it's quoted",
		"folded":   "first line second line",
		"literal":  "line one\nline two",
		"metadata": "", // a nested map is skipped, not modeled
		"after":    "still-parsed",
	}
	for k, v := range want {
		if fields[k] != v {
			t.Errorf("%s = %q, want %q", k, fields[k], v)
		}
	}
}

// A nested map must not swallow the keys that follow it — that would silently
// drop a description written below a metadata block.
func TestParseFrontmatter_NestedMapDoesNotConsumeLaterKeys(t *testing.T) {
	fields := parseFrontmatter("metadata:\n  a: 1\n  b: 2\ndescription: after the map")
	if fields["description"] != "after the map" {
		t.Errorf("description = %q; the metadata block consumed it", fields["description"])
	}
}

// Editors add a BOM and Windows line endings invisibly. Either one would make
// the opening fence unrecognizable and cost the user the whole skill.
func TestParse_ToleratesBOMAndCRLF(t *testing.T) {
	s, err := parseContent([]byte("\ufeff---\r\nname: demo\r\ndescription: works\r\n---\r\nbody\r\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Name != "demo" || s.Description != "works" {
		t.Errorf("got name=%q description=%q", s.Name, s.Description)
	}
}

// The description is rendered on one line inside an XML element in the system
// prompt; an embedded newline breaks that block's shape.
func TestParse_CollapsesDescriptionToOneLine(t *testing.T) {
	s, err := parseContent([]byte("---\nname: demo\ndescription: |\n  first\n  second\n---\nbody\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if strings.Contains(s.Description, "\n") {
		t.Errorf("description spans lines: %q", s.Description)
	}
	if s.Description != "first second" {
		t.Errorf("description = %q", s.Description)
	}
}

// Every built-in skill is held to the same rules a disk skill is, so one added
// later cannot ship in a shape that would have been rejected on disk.
func TestEmbedded_AllParse(t *testing.T) {
	skills, errs := Embedded()
	for _, err := range errs {
		t.Errorf("built-in skill failed to parse: %v", err)
	}
	if len(skills) == 0 {
		t.Fatal("expected at least one built-in skill")
	}
	for _, s := range skills {
		if s.Description == "" {
			t.Errorf("built-in %q has no description; it is all the agent gets to choose on", s.Name)
		}
		if s.Dir != "" {
			t.Errorf("built-in %q claims a directory %q, but nothing ships beside it", s.Name, s.Dir)
		}
		if s.Source != SourceEmbedded {
			t.Errorf("built-in %q has source %q", s.Name, s.Source)
		}
	}
}

// The clamp cuts on a rune boundary. A byte slice through a multi-byte
// character would put invalid UTF-8 straight into the system prompt — a worse
// outcome than the long description it was avoiding.
func TestParse_ClampIsRuneSafe(t *testing.T) {
	long := strings.Repeat("日", MaxDescriptionLen+200) // 3 bytes per rune
	s, err := parseContent([]byte("---\nname: cjk\ndescription: " + long + "\n---\nbody\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !utf8.ValidString(s.Description) {
		t.Error("clamped description is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(s.Description); n > MaxDescriptionLen+1 { // +1 for the ellipsis
		t.Errorf("description is %d runes, over the %d limit", n, MaxDescriptionLen)
	}
	if !strings.HasSuffix(s.Description, "…") {
		t.Error("a clamped description should end in an ellipsis so the cut is visible")
	}
}

// Trailing whitespace on a fence is invisible in an editor. The closing fence
// has always tolerated it; the opening one must too, or the user loses the
// whole skill to a stray space.
func TestParse_ToleratesTrailingSpaceOnFences(t *testing.T) {
	s, err := parseContent([]byte("---  \nname: demo\ndescription: works\n--- \nbody\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Name != "demo" || s.Content != "body" {
		t.Errorf("got name=%q content=%q", s.Name, s.Content)
	}
}
