package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/memfile"
)

func TestMemoryMDPaths_NoFiles(t *testing.T) {
	dir := t.TempDir()
	if got := memoryMDPaths(dir); len(got) != 0 {
		t.Errorf("expected no paths, got %v", got)
	}
}

// The closest MEMORY.md is the most specific one, so the order is outermost
// first and the project's own file comes last — the order the prompt states.
func TestMemoryMDPaths_WalksUpRootToLeaf(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(root, "MEMORY.md")
	projectPath := filepath.Join(project, "MEMORY.md")
	mustWrite(t, rootPath, "Root memory.")
	mustWrite(t, projectPath, "Project memory.")

	got := memoryMDPaths(project)
	if len(got) != 2 || got[0] != rootPath || got[1] != projectPath {
		t.Errorf("want root-to-leaf [%s %s], got %v", rootPath, projectPath, got)
	}
}

// discoverMemoryFiles is the one call the loop makes: project instructions,
// long-term memory, and the per-session path — paths only, never contents.
func TestDiscoverMemoryFiles_CollectsAllThreeStores(t *testing.T) {
	dir := t.TempDir()
	agentPath := filepath.Join(dir, "AGENTS.md")
	memoryPath := filepath.Join(dir, "MEMORY.md")
	mustWrite(t, agentPath, "PROJECT INSTRUCTION BODY")
	mustWrite(t, memoryPath, "LONG TERM MEMORY BODY")
	const key = "ses_01M269J8TEST"

	got := discoverMemoryFiles(dir, key)
	if len(got.agentMD) != 1 || got.agentMD[0] != agentPath {
		t.Errorf("agentMD = %v, want [%s]", got.agentMD, agentPath)
	}
	if len(got.memoryMD) != 1 || got.memoryMD[0] != memoryPath {
		t.Errorf("memoryMD = %v, want [%s]", got.memoryMD, memoryPath)
	}
	if want := memfile.SessionMemoryRelPath(dir, key); got.sessionMD != want {
		t.Errorf("sessionMD = %q, want %q even when the file does not exist yet", got.sessionMD, want)
	}
}

// A project with none of the files still gets the session path: it is where the
// writable agent is told to create the file.
func TestDiscoverMemoryFiles_NoProjectFiles(t *testing.T) {
	dir := t.TempDir()
	const key = "ses_empty"

	got := discoverMemoryFiles(dir, key)
	if len(got.agentMD) != 0 || len(got.memoryMD) != 0 {
		t.Errorf("a directory with no memory files must discover none; got %+v", got)
	}
	if got.sessionMD == "" {
		t.Error("sessionMD must name the path the agent can create")
	}
}
