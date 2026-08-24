package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMergesGlobalAndProjectPreferringProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".config", "ogcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(globalDir, "config.json"), `{
		"providers": {
			"ollama": {"baseUrl": "http://global:11434", "apiKey": "global-key"},
			"anthropic": {"baseUrl": "http://global-anthropic"}
		}
	}`)

	projectDir := t.TempDir()
	writeJSON(t, filepath.Join(projectDir, "ogcode.json"), `{
		"providers": {
			"ollama": {"baseUrl": "http://project:11434"}
		}
	}`)

	cfg := Load(projectDir)

	if got := cfg.Providers["ollama"].BaseURL; got != "http://project:11434" {
		t.Errorf("ollama baseUrl = %q, want project value to win", got)
	}
	if got := cfg.Providers["ollama"].APIKey; got != "global-key" {
		t.Errorf("ollama apiKey = %q, want global value preserved", got)
	}
	if got := cfg.Providers["anthropic"].BaseURL; got != "http://global-anthropic" {
		t.Errorf("anthropic baseUrl = %q, want global-only value preserved", got)
	}
}

func TestApplyEnvDoesNotOverrideExistingEnv(t *testing.T) {
	t.Setenv("OLLAMA_BASE_URL", "http://already-set:11434")

	cfg := &Config{Providers: map[string]ProviderConfig{
		"ollama": {BaseURL: "http://from-config:11434"},
	}}
	cfg.ApplyEnv()

	if got := os.Getenv("OLLAMA_BASE_URL"); got != "http://already-set:11434" {
		t.Errorf("OLLAMA_BASE_URL = %q, want existing env var preserved", got)
	}
}

func TestApplyEnvSetsUnsetVars(t *testing.T) {
	os.Unsetenv("OLLAMA_BASE_URL")
	os.Unsetenv("OLLAMA_API_KEY")

	cfg := &Config{Providers: map[string]ProviderConfig{
		"ollama": {BaseURL: "http://from-config:11434", APIKey: "k"},
	}}
	cfg.ApplyEnv()

	if got := os.Getenv("OLLAMA_BASE_URL"); got != "http://from-config:11434" {
		t.Errorf("OLLAMA_BASE_URL = %q, want value from config", got)
	}
	if got := os.Getenv("OLLAMA_API_KEY"); got != "k" {
		t.Errorf("OLLAMA_API_KEY = %q, want value from config", got)
	}
}

func TestFindProjectFileSearchesUpToRepoRoot(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(repoRoot, "ogcode.json"), `{"providers": {"ollama": {"baseUrl": "http://root:11434"}}}`)

	subDir := filepath.Join(repoRoot, "internal", "server")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := findProjectFile(subDir); got != filepath.Join(repoRoot, "ogcode.json") {
		t.Errorf("findProjectFile(%q) = %q, want the repo-root ogcode.json", subDir, got)
	}
}

func TestFindProjectFileStopsAtRepoRootBoundary(t *testing.T) {
	outer := t.TempDir()
	// An ogcode.json outside the repo must never leak into a search that
	// starts inside the repo — otherwise an unrelated ancestor directory
	// (e.g. a parent workspace folder) could silently inject provider config.
	writeJSON(t, filepath.Join(outer, "ogcode.json"), `{"providers": {"ollama": {"baseUrl": "http://outer:11434"}}}`)

	repoRoot := filepath.Join(outer, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(repoRoot, "src")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := findProjectFile(subDir); got != "" {
		t.Errorf("findProjectFile(%q) = %q, want empty (search must stop at repo root)", subDir, got)
	}
}

func TestFindProjectFileTreatsGitFileAsRepoRootBoundary(t *testing.T) {
	outer := t.TempDir()
	writeJSON(t, filepath.Join(outer, "ogcode.json"), `{"providers": {"ollama": {"baseUrl": "http://outer:11434"}}}`)

	// A linked git worktree (or submodule) has a .git *file*, not a
	// directory, pointing at the real git dir elsewhere.
	worktreeRoot := filepath.Join(outer, "worktree")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(worktreeRoot, ".git"), `gitdir: /somewhere/else/.git/worktrees/foo`)

	subDir := filepath.Join(worktreeRoot, "src")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := findProjectFile(subDir); got != "" {
		t.Errorf("findProjectFile(%q) = %q, want empty (a .git file must stop the search too)", subDir, got)
	}
}

func TestEnsureProjectFileCreatesWhenMissing(t *testing.T) {
	dir := t.TempDir()

	path := EnsureProjectFile(dir)
	want := filepath.Join(dir, "ogcode.json")
	if path != want {
		t.Fatalf("EnsureProjectFile(%q) = %q, want %q", dir, path, want)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("created file is not readable: %v", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("created file is not valid JSON: %v", err)
	}
	for _, id := range []string{"anthropic", "openai", "openrouter", "ollama"} {
		if _, ok := c.Providers[id]; !ok {
			t.Errorf("created file is missing provider %q", id)
		}
	}
}

func TestEnsureProjectFileLeavesExistingFileUntouched(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "ogcode.json")
	writeJSON(t, existing, `{"providers": {"ollama": {"baseUrl": "http://mine:11434"}}}`)

	if path := EnsureProjectFile(dir); path != "" {
		t.Errorf("EnsureProjectFile(%q) = %q, want no-op when a file already exists", dir, path)
	}

	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"providers": {"ollama": {"baseUrl": "http://mine:11434"}}}` {
		t.Errorf("existing file content was modified: %s", data)
	}
}

func TestEnsureProjectFileSkipsWhenAncestorHasOne(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(repoRoot, "ogcode.json"), `{"providers": {}}`)

	subDir := filepath.Join(repoRoot, "src")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if path := EnsureProjectFile(subDir); path != "" {
		t.Errorf("EnsureProjectFile(%q) = %q, want no-op — repo root already has one", subDir, path)
	}
	if _, err := os.Stat(filepath.Join(subDir, "ogcode.json")); err == nil {
		t.Error("a second ogcode.json was created in the subdirectory")
	}
}

func TestLoadMissingFilesReturnsUsableEmptyConfig(t *testing.T) {
	cfg := Load(t.TempDir())
	if cfg == nil || cfg.Providers == nil {
		t.Fatal("Load must never return a nil Config or nil Providers map")
	}
	if len(cfg.Providers) != 0 {
		t.Errorf("expected no providers, got %v", cfg.Providers)
	}
}

func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
