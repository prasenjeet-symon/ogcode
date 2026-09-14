package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// git runs a git command in dir, tolerating the test environment.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	allArgs := append([]string{
		"-c", "user.name=t",
		"-c", "user.email=t@local",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", allArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// writeCommit seeds a file into the repo at dir and commits it.
func writeCommit(t *testing.T, dir string, file, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "--quiet", "-m", msg)
}

// seedBareRepo creates a bare repo with one committed file, returning its path.
func seedBareRepo(t *testing.T) string {
	t.Helper()
	work := t.TempDir()
	git(t, work, "init", "--quiet")
	writeCommit(t, work, "hello.txt", "hello world\n", "seed")
	bare := filepath.Join(t.TempDir(), "seed.git")
	git(t, work, "init", "--quiet", "--bare", bare)
	git(t, work, "push", "--quiet", bare, "HEAD")
	return bare
}

func TestCloneRepo_Materializes(t *testing.T) {
	bare := seedBareRepo(t)
	dst := filepath.Join(t.TempDir(), "wc")

	if err := cloneRepo(bare, dst); err != nil {
		t.Fatalf("cloneRepo: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "hello.txt"))
	if err != nil {
		t.Fatalf("cloned file missing: %v", err)
	}
	if string(b) != "hello world\n" {
		t.Fatalf("cloned content = %q, want %q", b, "hello world\n")
	}
}

func TestCloneRepo_RefusesExistingDir(t *testing.T) {
	bare := seedBareRepo(t)
	parent := t.TempDir()
	dst := filepath.Join(parent, "wc")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := cloneRepo(bare, dst); err == nil {
		t.Fatal("cloneRepo over existing dir should fail")
	} else if !strings.Contains(err.Error(), "refusing to clone") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCloneRepo_BadURL(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "wc")
	if err := cloneRepo("https://127.0.0.1:1/nope.git", dst); err == nil {
		t.Fatal("cloneRepo with unreachable url should fail")
	}
}

func TestBootstrapWorkspace_ExistingDirOK(t *testing.T) {
	dir := t.TempDir()
	if err := bootstrapWorkspace(dir, ""); err != nil {
		t.Fatalf("existing dir should pass with no ref: %v", err)
	}
}

func TestBootstrapWorkspace_AbsentNoRefHint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	err := bootstrapWorkspace(dir, "")
	if err == nil {
		t.Fatal("absent dir with no ref should error")
	}
	if !strings.Contains(err.Error(), "provide a git ref to clone it") {
		t.Fatalf("error should carry clone hint: %v", err)
	}
}

func TestBootstrapWorkspace_ClonesWhenRef(t *testing.T) {
	bare := seedBareRepo(t)
	dir := filepath.Join(t.TempDir(), "wc")

	if err := bootstrapWorkspace(dir, bare); err != nil {
		t.Fatalf("bootstrapWorkspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err != nil {
		t.Fatalf("cloned workspace missing seed file: %v", err)
	}
}
