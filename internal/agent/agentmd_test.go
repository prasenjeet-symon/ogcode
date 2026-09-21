package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAgentMD_NoFiles(t *testing.T) {
	dir := t.TempDir()
	got := LoadAgentMD(dir)
	if got != "" {
		t.Errorf("expected empty string with no AGENT.md files, got %q", got)
	}
}

func TestLoadAgentMD_SingleFile(t *testing.T) {
	dir := t.TempDir()
	content := "Always use Go style comments."
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got := LoadAgentMD(dir)
	if got == "" {
		t.Fatal("expected non-empty result")
	}
	if !contains(got, "<agent-md") {
		t.Errorf("expected <agent-md> tag, got %q", got)
	}
	if !contains(got, content) {
		t.Errorf("expected content %q in result, got %q", content, got)
	}
	if !contains(got, `path="AGENT.md"`) {
		t.Errorf("expected relative path in result, got %q", got)
	}
}

func TestLoadAgentMD_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("  \n\n  "), 0644); err != nil {
		t.Fatal(err)
	}

	got := LoadAgentMD(dir)
	if got != "" {
		t.Errorf("expected empty string for whitespace-only AGENT.md, got %q", got)
	}
}

func TestLoadAgentMD_Hierarchy(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "project")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	rootContent := "# Root instructions\nUse tabs for indentation."
	projectContent := "# Project instructions\nAlways run tests before committing."

	if err := os.WriteFile(filepath.Join(root, "AGENT.md"), []byte(rootContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "AGENT.md"), []byte(projectContent), 0644); err != nil {
		t.Fatal(err)
	}

	got := LoadAgentMD(subdir)
	if got == "" {
		t.Fatal("expected non-empty result")
	}

	// Root content should appear before project content (root-to-leaf order)
	rootIdx := indexOf(got, rootContent)
	projectIdx := indexOf(got, projectContent)
	if rootIdx == -1 || projectIdx == -1 {
		t.Fatalf("expected both contents in result, got %q", got)
	}
	if rootIdx >= projectIdx {
		t.Errorf("expected root content before project content, root at %d, project at %d", rootIdx, projectIdx)
	}
}

func TestLoadAgentMD_SizeLimit(t *testing.T) {
	dir := t.TempDir()
	// Create a file larger than the max size
	bigContent := make([]byte, maxAgentMDSize+1000)
	for i := range bigContent {
		bigContent[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), bigContent, 0644); err != nil {
		t.Fatal(err)
	}

	got := LoadAgentMD(dir)
	if got == "" {
		t.Fatal("expected non-empty result")
	}
	// Should be truncated to maxAgentMDSize
	tagOverhead := len("\n\n<agent-md path=\"AGENT.md\">\n\n</agent-md>")
	if len(got) > maxAgentMDSize+tagOverhead {
		t.Errorf("result too long: got %d bytes, expected at most %d", len(got), maxAgentMDSize+tagOverhead)
	}
}

func TestDiscoverAgentMDPaths_WalksUp(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create AGENT.md at root and at subdir
	if err := os.WriteFile(filepath.Join(root, "AGENT.md"), []byte("root"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "AGENT.md"), []byte("subdir"), 0644); err != nil {
		t.Fatal(err)
	}

	paths := discoverAgentMDPaths(subdir)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(paths), paths)
	}
	// Root-to-leaf order: root first, subdir second
	if filepath.Base(paths[0]) != "AGENT.md" || filepath.Dir(paths[0]) != root {
		t.Errorf("expected first path in root, got %s", paths[0])
	}
	if filepath.Dir(paths[1]) != subdir {
		t.Errorf("expected second path in subdir, got %s", paths[1])
	}
}

func TestDiscoverAgentMDPaths_NoFiles(t *testing.T) {
	dir := t.TempDir()
	paths := discoverAgentMDPaths(dir)
	if len(paths) != 0 {
		t.Errorf("expected no paths, got %v", paths)
	}
}

// helpers

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestLoadAgentMD_AgentsPluralIsRead covers the cross-tool convention. A project
// that already carries AGENTS.md for another agent needs no ogcode-specific
// file: its instructions are honoured as they stand.
func TestLoadAgentMD_AgentsPluralIsRead(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), "Always reply in Russian.")

	out := LoadAgentMD(dir)
	if !contains(out, "Always reply in Russian.") {
		t.Errorf("AGENTS.md was not loaded:\n%s", out)
	}
	if !contains(out, "AGENTS.md") {
		t.Errorf("the block should name the file it came from:\n%s", out)
	}
}

// TestLoadAgentMD_BothNamesInOneDir pins the order when a directory holds both.
// The generic cross-tool file comes first and ogcode's own name last, which puts
// the more specific instructions closer to the model — the same outer-to-inner
// precedence the directory walk already uses.
func TestLoadAgentMD_BothNamesInOneDir(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), "Generic house style.")
	mustWrite(t, filepath.Join(dir, "AGENT.md"), "Ogcode-specific rule.")

	out := LoadAgentMD(dir)
	generic := strings.Index(out, "Generic house style.")
	specific := strings.Index(out, "Ogcode-specific rule.")
	if generic < 0 || specific < 0 {
		t.Fatalf("both files should load:\n%s", out)
	}
	if generic > specific {
		t.Errorf("AGENT.md should come last so it takes precedence:\n%s", out)
	}
}

// TestLoadAgentMD_IdenticalNamesDeduped is the copy case: two names, same text,
// which a project gets from keeping the files in sync or from a tool writing
// both. Stating it twice would double the token cost for no added instruction.
func TestLoadAgentMD_IdenticalNamesDeduped(t *testing.T) {
	dir := t.TempDir()
	const body = "Explain each action — why and how."
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), body)
	mustWrite(t, filepath.Join(dir, "AGENT.md"), body)

	out := LoadAgentMD(dir)
	if n := strings.Count(out, body); n != 1 {
		t.Errorf("identical content appeared %d times, want 1:\n%s", n, out)
	}
}

// TestLoadAgentMD_PluralWalksUpToo checks the new name is honoured at every
// level of the walk, not only in the working directory — so a single
// ~/AGENTS.md covers every project beneath it.
func TestLoadAgentMD_PluralWalksUpToo(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, "work", "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(home, "AGENTS.md"), "Global: reply in Russian.")
	mustWrite(t, filepath.Join(proj, "AGENT.md"), "Project: targets Go 1.26.")

	out := LoadAgentMD(proj)
	global := strings.Index(out, "Global: reply in Russian.")
	project := strings.Index(out, "Project: targets Go 1.26.")
	if global < 0 || project < 0 {
		t.Fatalf("both levels should load:\n%s", out)
	}
	if global > project {
		t.Errorf("the outer file should come first:\n%s", out)
	}
}

// mustWrite creates one instruction file for the tests below.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
