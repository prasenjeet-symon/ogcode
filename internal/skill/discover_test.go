package skill

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestScanRoot_ReadsALibraryOfSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha", "---\nname: alpha\ndescription: first\n---\nbody\n")
	writeSkill(t, root, "beta", "---\nname: beta\ndescription: second\n---\nbody\n")
	// A directory with no SKILL.md is not a skill and is passed over silently.
	if err := os.MkdirAll(filepath.Join(root, "notaskill"), 0o755); err != nil {
		t.Fatal(err)
	}

	skills, problems := ScanRoot(root, SourceProject)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(skills) != 2 {
		t.Fatalf("got %d skills, want 2", len(skills))
	}
	if skills[0].Name != "alpha" || skills[1].Name != "beta" {
		t.Errorf("results are not sorted by name: %q, %q", skills[0].Name, skills[1].Name)
	}
	if skills[0].Source != SourceProject {
		t.Errorf("source = %q, want project", skills[0].Source)
	}
}

// A configured path may point at one skill rather than a library of them, and
// the two are told apart by whether the directory itself holds a SKILL.md.
func TestScanRoot_AcceptsASingleSkillDirectory(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "solo", "---\nname: solo\ndescription: alone\n---\nbody\n")
	skills, problems := ScanRoot(dir, SourceConfig)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if len(skills) != 1 || skills[0].Name != "solo" {
		t.Fatalf("got %v, want one skill named solo", skills)
	}
}

// Most of the locations ogcode looks in do not exist in any given project.
func TestScanRoot_MissingRootIsNotAProblem(t *testing.T) {
	skills, problems := ScanRoot(filepath.Join(t.TempDir(), "nope"), SourceGlobal)
	if len(skills) != 0 || len(problems) != 0 {
		t.Errorf("got skills=%v problems=%v, want both empty", skills, problems)
	}
}

// One malformed SKILL.md must not take the working skills down with it — but it
// must be reported, or the user is left with a skill that silently never
// appears.
func TestScanRoot_BadSkillIsReportedAndSkipped(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "good", "---\nname: good\ndescription: fine\n---\nbody\n")
	writeSkill(t, root, "bad", "no frontmatter here\n")

	skills, problems := ScanRoot(root, SourceProject)
	if len(skills) != 1 || skills[0].Name != "good" {
		t.Fatalf("got %v, want only the good skill", skills)
	}
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want 1 naming the bad skill", len(problems))
	}
}

// The walk stops at the repo root, so an unrelated .claude or .agents directory
// above the project never contributes skills to it.
func TestProjectRoots_StopAtTheRepoRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	roots := ProjectRoots(nested)
	for _, r := range roots {
		if len(r) < len(repo) || r[:len(repo)] != repo {
			t.Errorf("root %q is outside the repo %q", r, repo)
		}
	}
	// Outermost first, so a caller registering in order lets the innermost win.
	if !slices.Contains(roots, filepath.Join(repo, ".agents", "skills")) {
		t.Error("repo-level .agents/skills missing")
	}
	if !slices.Contains(roots, filepath.Join(nested, ".claude", "skills")) {
		t.Error("nested .claude/skills missing")
	}
	if roots[len(roots)-1][:len(nested)] != nested {
		t.Errorf("last root %q is not the innermost one", roots[len(roots)-1])
	}
}

func TestSiblingFiles_ListsShippedFilesAndCaps(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "shipper", "---\nname: shipper\n---\nbody\n")
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "REFERENCE.md"), []byte("ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, truncated := SiblingFiles(dir, 10)
	if truncated {
		t.Error("two files should not truncate a limit of 10")
	}
	if !slices.Contains(files, "scripts/run.sh") || !slices.Contains(files, "REFERENCE.md") {
		t.Errorf("files = %v", files)
	}
	// The body is already in context; listing it again is pure cost.
	if slices.Contains(files, Filename) {
		t.Error("SKILL.md must not be listed as one of its own siblings")
	}
	// Paths are relative to the skill directory and slash-separated, so they
	// read the same on every OS.
	for _, f := range files {
		if filepath.IsAbs(f) {
			t.Errorf("%q should be relative to the skill directory", f)
		}
	}

	// A truncated listing must say so, or a sample reads as the whole set.
	if _, truncated := SiblingFiles(dir, 1); !truncated {
		t.Error("expected truncated=true when the limit cuts the listing short")
	}
}

// Without a project directory there is nothing to scan. Falling through would
// join relative paths and quietly scan whatever directory the process happens
// to be running in.
func TestProjectRoots_EmptyDirScansNothing(t *testing.T) {
	if roots := ProjectRoots(""); roots != nil {
		t.Errorf("ProjectRoots(\"\") = %v, want nil", roots)
	}
}

// The cap bounds entries examined, not skills found: the cost being bounded is
// the scan itself, and a root full of directories that are not skills is
// exactly the case worth stopping early.
func TestScanRoot_CapsEntriesExamined(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxRootEntries+5; i++ {
		if err := os.MkdirAll(filepath.Join(root, "empty-"+strconv.Itoa(i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, problems := ScanRoot(root, SourceProject)
	if len(problems) != 1 {
		t.Fatalf("got %d problems, want 1 reporting the truncated scan", len(problems))
	}
	if !strings.Contains(problems[0].Error(), "were examined") {
		t.Errorf("problem should say the scan was cut short: %v", problems[0])
	}
}
