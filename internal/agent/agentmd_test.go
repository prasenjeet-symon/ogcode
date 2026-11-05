package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentMDPaths_NoFiles(t *testing.T) {
	dir := t.TempDir()
	if got := agentMDPaths(dir); len(got) != 0 {
		t.Errorf("expected no paths, got %v", got)
	}
}

func TestAgentMDPaths_SingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENT.md")
	mustWrite(t, path, "Always use Go style comments.")

	got := agentMDPaths(dir)
	if len(got) != 1 || got[0] != path {
		t.Fatalf("expected [%s], got %v", path, got)
	}
}

// Discovery stats a file and never opens it, so an empty instruction file is
// still listed. Whether it says anything is the agent's judgement, made after
// it reads the file on demand.
func TestAgentMDPaths_EmptyFileIsStillListed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	mustWrite(t, path, "")

	got := agentMDPaths(dir)
	if len(got) != 1 || got[0] != path {
		t.Fatalf("an empty instruction file must still be discovered; got %v", got)
	}
}

// Both names in one directory are both listed, AGENTS.md first: the generic
// cross-tool file is the outer rule, ogcode's own name the more specific one.
func TestAgentMDPaths_BothNamesInOneDir(t *testing.T) {
	dir := t.TempDir()
	agents := filepath.Join(dir, "AGENTS.md")
	agent := filepath.Join(dir, "AGENT.md")
	mustWrite(t, agents, "Generic house style.")
	mustWrite(t, agent, "Ogcode-specific rule.")

	got := agentMDPaths(dir)
	if len(got) != 2 || got[0] != agents || got[1] != agent {
		t.Errorf("want [%s %s], got %v", agents, agent, got)
	}
}

// The names are honoured at every level of the walk, and the order is
// outermost first — the same precedence a directory walk gives, so a global
// file cannot override the project's own.
func TestAgentMDPaths_WalksUpRootToLeaf(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(root, "AGENT.md")
	projectPath := filepath.Join(project, "AGENTS.md")
	mustWrite(t, rootPath, "Root instructions.")
	mustWrite(t, projectPath, "Project instructions.")

	got := agentMDPaths(project)
	if len(got) != 2 || got[0] != rootPath || got[1] != projectPath {
		t.Errorf("want root-to-leaf [%s %s], got %v", rootPath, projectPath, got)
	}
}

// A single ~/AGENTS.md covers every project beneath it, and the plural name is
// the one a tree of repos is most likely to carry.
func TestAgentMDPaths_PluralWalksUpToo(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, "work", "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(home, "AGENTS.md")
	local := filepath.Join(proj, "AGENT.md")
	mustWrite(t, global, "Global: reply in Russian.")
	mustWrite(t, local, "Project: targets Go 1.26.")

	got := agentMDPaths(proj)
	if len(got) != 2 || got[0] != global || got[1] != local {
		t.Errorf("want [%s %s], got %v", global, local, got)
	}
}

func TestAgentMDPaths_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := agentMDPaths(dir); len(got) != 0 {
		t.Errorf("a directory named like the instruction file is not one; got %v", got)
	}
}

// mustWrite creates one instruction file for the tests below.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
