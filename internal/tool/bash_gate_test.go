//go:build !windows

package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// runShell runs one bash tool call as s: the mutation path that bypasses the
// file tools and therefore the gate, until observeShell catches up with it.
func (g *changeGate) runShell(s session.SessionID, command string) {
	g.t.Helper()
	if _, err := g.call(s, BashTool{}.Execute, map[string]any{"command": command}); err != nil {
		g.t.Fatalf("bash: %v", err)
	}
}

// TestShellChangeRefusesAReaderOfAFreshFile pins the whole bash path, and the
// bookmark it depends on: a file seeded outside the tools sits at revision 0,
// session-a reads it there, session-b rewrites it with a shell redirect, and
// session-a's write is refused with the command as the catch-up. Before the
// fingerprint the shell was invisible; before the revision-0 bookmark the
// refusal could not fire on a file nothing had mutated.
func TestShellChangeRefusesAReaderOfAFreshFile(t *testing.T) {
	g := newChangeGate(t)
	path := filepath.Join(g.dir, "notes.md")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	g.read("session-a", path)
	g.runShell("session-b", "printf 'two two\\n' > notes.md")

	res := g.write("session-a", map[string]any{"path": path, "content": "three\n"})
	if res.Title != "File changed by another agent" {
		t.Fatalf("title = %q, want the stale refusal (output: %s)", res.Title, res.Output)
	}
	for _, want := range []string{"revision 0", "revision 1", "(bash)", "printf", "diff omitted"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, res.Output)
		}
	}
	if got := g.content(path); got != "two two\n" {
		t.Fatalf("file = %q, want the shell's content %q — a refusal must not write", got, "two two\n")
	}
}

// TestShellDeletionIsRefused covers the case where the stat cannot find the
// file at all: a deletion is a change like any other, and the agent that still
// has the bytes in its context must not write them back unnoticed.
func TestShellDeletionIsRefused(t *testing.T) {
	g := newChangeGate(t)
	path := filepath.Join(g.dir, "notes.md")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	g.read("session-a", path)
	g.runShell("session-b", "rm notes.md")

	res := g.write("session-a", map[string]any{"path": path, "content": "one\n"})
	if res.Title != "File changed by another agent" {
		t.Fatalf("title = %q, want the stale refusal (output: %s)", res.Title, res.Output)
	}
	if !strings.Contains(res.Output, "rm notes.md") {
		t.Errorf("refusal does not name the command:\n%s", res.Output)
	}
}

// TestOwnShellChangeRefusesUntilReRead pins that a shell command is not a read,
// even for the session that ran it: the observer records the change without
// crediting anyone, so the model holding the pre-command bytes is refused and
// must read the file again before it may write.
func TestOwnShellChangeRefusesUntilReRead(t *testing.T) {
	g := newChangeGate(t)
	path := filepath.Join(g.dir, "notes.md")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	g.read("session-a", path)
	g.runShell("session-a", "printf 'two two\\n' > notes.md")

	res := g.write("session-a", map[string]any{"path": path, "content": "three\n"})
	if res.Title != "File changed by another agent" {
		t.Fatalf("title = %q, want the stale refusal (output: %s)", res.Title, res.Output)
	}
	if !strings.Contains(res.Output, "printf") {
		t.Errorf("refusal does not name its own command:\n%s", res.Output)
	}

	// Reading the file again is what catches the session up.
	g.read("session-a", path)
	res = g.write("session-a", map[string]any{"path": path, "content": "three\n"})
	if res.Title == "File changed by another agent" {
		t.Fatalf("a re-read did not clear the refusal: %s", res.Output)
	}
}

// TestUnrelatedCommandDoesNotLaunderAForeignChange pins the reason the observer
// credits nobody: a command that happens to run after another session's write
// must not become its author and excuse it, or the write that follows would
// clobber a change the session never saw.
func TestUnrelatedCommandDoesNotLaunderAForeignChange(t *testing.T) {
	g := newChangeGate(t)
	path := filepath.Join(g.dir, "notes.md")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	g.read("session-a", path)
	if err := os.WriteFile(path, []byte("two two\n"), 0o644); err != nil {
		t.Fatalf("foreign write: %v", err)
	}
	g.runShell("session-a", "true")

	res := g.write("session-a", map[string]any{"path": path, "content": "three\n"})
	if res.Title != "File changed by another agent" {
		t.Fatalf("title = %q, want the stale refusal (output: %s)", res.Title, res.Output)
	}
	if got := g.content(path); got != "two two\n" {
		t.Fatalf("file = %q, want the foreign content %q — a refusal must not write", got, "two two\n")
	}
}

// TestShellChangeToAnUntrackedFileIsNotRecorded pins the bound: only files the
// gate has seen are compared. Nobody holds a bookmark on other.md, so changing
// it can stale no one and is not worth a history row.
func TestShellChangeToAnUntrackedFileIsNotRecorded(t *testing.T) {
	g := newChangeGate(t)
	read := filepath.Join(g.dir, "notes.md")
	if err := os.WriteFile(read, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	g.read("session-a", read)
	g.runShell("session-a", "printf 'one\\n' > other.md")

	if rev := g.log.Rev(FileKey(g.dir, "other.md")); rev != 0 {
		t.Fatalf("untracked file rev = %d, want 0", rev)
	}
	if rev := g.log.Rev(FileKey(g.dir, "notes.md")); rev != 0 {
		t.Fatalf("unchanged file rev = %d, want 0 — a matching stat records nothing", rev)
	}
}
