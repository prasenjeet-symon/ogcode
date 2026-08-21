package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePorcelain(t *testing.T) {
	// -z output: records NUL-separated. A modified+staged file, an untracked
	// file, and a rename (source then dest as a separate field).
	in := "M  main.go\x00?? new.txt\x00R  old.go\x00new.go\x00"
	got := parsePorcelain(in)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(got), got)
	}

	// Staged modification.
	if got[0].Path != "main.go" || got[0].X != "M" || got[0].Y != " " || !got[0].Staged {
		t.Fatalf("entry 0 mismatch: %+v", got[0])
	}

	// Untracked file — not staged.
	if got[1].Path != "new.txt" || got[1].X != "?" || got[1].Y != "?" || got[1].Staged {
		t.Fatalf("entry 1 mismatch: %+v", got[1])
	}

	// Rename: Path must be the destination ("new.go"), staged.
	if got[2].Path != "new.go" || got[2].X != "R" || got[2].Y != " " || !got[2].Staged {
		t.Fatalf("rename entry mismatch: %+v", got[2])
	}
}

func TestParsePorcelain_Empty(t *testing.T) {
	if got := parsePorcelain(""); got != nil {
		t.Fatalf("expected nil for empty input, got %+v", got)
	}
}

func TestParseLog(t *testing.T) {
	// Fields separated by 0x01, commits separated by 0x00, trailing NUL.
	in := "deadbeefdeadbeef\x01deadbee\x01fix bug\x01Alice\x012026-08-21T17:00:00+05:30\x00" +
		"cafebabecafebabe\x01cafebabe\x01add feature\x01Bob\x012026-08-20T10:00:00+05:30\x00"
	got := parseLog(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 commits, got %d: %+v", len(got), got)
	}
	if got[0].SHA != "deadbeefdeadbeef" || got[0].Short != "deadbee" || got[0].Message != "fix bug" ||
		got[0].Author != "Alice" || got[0].Time != "2026-08-21T17:00:00+05:30" {
		t.Fatalf("commit 0 mismatch: %+v", got[0])
	}
	if got[1].SHA != "cafebabecafebabe" || got[1].Short != "cafebabe" || got[1].Message != "add feature" ||
		got[1].Author != "Bob" || got[1].Time != "2026-08-20T10:00:00+05:30" {
		t.Fatalf("commit 1 mismatch: %+v", got[1])
	}
}

func TestParseLog_Empty(t *testing.T) {
	if got := parseLog(""); got != nil {
		t.Fatalf("expected nil for empty input, got %+v", got)
	}
}

// TestDiffFile_Untracked verifies that DiffFile renders a brand-new (untracked)
// file as an all-addition diff instead of returning an empty string.
func TestDiffFile_Untracked(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "--quiet")
	writeCommit(t, dir, "README.md", "seed\n", "seed")

	// Create a new file without staging it — status code "??".
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Status(dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	var found bool
	for _, f := range st {
		if f.Path == "new.go" && f.X == "?" && f.Y == "?" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected new.go as untracked in status: %+v", st)
	}

	// Unstaged diff of an untracked file must show every line as an addition.
	diff, err := DiffFile(dir, "new.go", false)
	if err != nil {
		t.Fatalf("DiffFile untracked: %v", err)
	}
	if diff == "" {
		t.Fatal("expected non-empty diff for untracked file, got \"\"")
	}
	if !contains(diff, "+package main") || !contains(diff, "+func main() {}") {
		t.Fatalf("untracked diff missing additions:\n%s", diff)
	}
	// No deletions should appear for a brand-new file.
	if contains(diff, "-package main") {
		t.Fatalf("untracked diff should not contain deletions:\n%s", diff)
	}
}

// TestStatus_RepoAndDiff exercises the shell-out wrappers end to end against a
// temp repo: status, diff, and recent commits.
func TestStatus_RepoAndDiff(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "--quiet")
	writeCommit(t, dir, "README.md", "seed\n", "seed")

	// Modify a tracked file — unstaged change.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Status(dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(st) != 1 {
		t.Fatalf("expected 1 changed file, got %d: %+v", len(st), st)
	}
	if st[0].Path != "README.md" || st[0].X != " " || st[0].Y != "M" || st[0].Staged {
		t.Fatalf("status entry mismatch: %+v", st[0])
	}

	// Unstaged diff should mention the changed line.
	diff, err := DiffFile(dir, "README.md", false)
	if err != nil {
		t.Fatalf("DiffFile: %v", err)
	}
	if !contains(diff, "-seed") || !contains(diff, "+changed") {
		t.Fatalf("diff missing expected hunks:\n%s", diff)
	}

	// Staged diff should be empty until we stage.
	if diff, err := DiffFile(dir, "README.md", true); err != nil {
		t.Fatalf("DiffFile staged: %v", err)
	} else if diff != "" {
		t.Fatalf("expected empty staged diff, got %q", diff)
	}

	// Stage and confirm staged diff shows the change.
	git(t, dir, "add", "README.md")
	if diff, err := DiffFile(dir, "README.md", true); err != nil {
		t.Fatalf("DiffFile staged after add: %v", err)
	} else if !contains(diff, "+changed") {
		t.Fatalf("staged diff missing change:\n%s", diff)
	}

	// Commit and verify recent commits.
	git(t, dir, "commit", "-q", "-m", "update README")
	commits, err := RecentCommits(dir, 5)
	if err != nil {
		t.Fatalf("RecentCommits: %v", err)
	}
	if len(commits) < 2 {
		t.Fatalf("expected >=2 commits, got %d", len(commits))
	}
	if commits[0].Message != "update README" {
		t.Fatalf("latest commit message mismatch: %q", commits[0].Message)
	}

	// ShowCommit returns a diff for the latest commit.
	show, err := ShowCommit(dir, commits[0].SHA)
	if err != nil {
		t.Fatalf("ShowCommit: %v", err)
	}
	if !contains(show, "update README") {
		t.Fatalf("ShowCommit output missing commit subject:\n%s", show)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
