package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestEnsureProjectFileAddsToExistingGitignore(t *testing.T) {
	dir := t.TempDir()
	// Seed a .gitignore the project already keeps.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if path := EnsureProjectFile(dir); path == "" {
		t.Fatal("EnsureProjectFile returned empty; want the created file path")
	}

	body, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "ogcode.json") {
		t.Errorf(".gitignore was not extended to ignore ogcode.json:\n%s", body)
	}
	// The pre-existing rule survives.
	if !strings.Contains(string(body), "dist/") {
		t.Errorf("the pre-existing dist/ rule was lost:\n%s", body)
	}
}

func TestEnsureProjectFileDoesNotCreateGitignore(t *testing.T) {
	dir := t.TempDir()
	if path := EnsureProjectFile(dir); path == "" {
		t.Fatal("EnsureProjectFile returned empty; want the created file path")
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("a .gitignore was created; ogcode must only extend an existing one: %v", err)
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

// Skill sources merge differently from provider settings: a project adds its
// own skill directories to the user's global ones rather than replacing them,
// so a global library survives a project that configures its own.
func TestLoad_SkillPathsAndURLsAreUnioned(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	globalDir := filepath.Join(home, ".config", "ogcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.json"), []byte(`{
		"skills": {
			"paths": ["/global/skills", "/shared"],
			"urls": ["https://example.com/a/index.json"],
			"permissions": {"*": "allow", "internal-*": "deny"}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "ogcode.json"), []byte(`{
		"skills": {
			"paths": ["./team", "/shared"],
			"urls": ["https://example.com/b/index.json"],
			"permissions": {"internal-*": "ask"}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Load(project)

	wantPaths := []string{"/global/skills", "/shared", "./team"}
	if !reflect.DeepEqual(cfg.Skills.Paths, wantPaths) {
		t.Errorf("paths = %v, want %v (unioned, duplicates dropped)", cfg.Skills.Paths, wantPaths)
	}
	wantURLs := []string{"https://example.com/a/index.json", "https://example.com/b/index.json"}
	if !reflect.DeepEqual(cfg.Skills.URLs, wantURLs) {
		t.Errorf("urls = %v, want %v", cfg.Skills.URLs, wantURLs)
	}
	// Permissions merge key by key, so a project overrides one rule without
	// having to restate the rest.
	if cfg.Skills.Permissions["internal-*"] != "ask" {
		t.Errorf("internal-* = %q, want the project's ask to win", cfg.Skills.Permissions["internal-*"])
	}
	if cfg.Skills.Permissions["*"] != "allow" {
		t.Errorf("* = %q, want the global rule to survive", cfg.Skills.Permissions["*"])
	}
}

// A config with no skills section must still be usable — the standard skill
// directories are scanned regardless.
func TestLoad_NoSkillsSectionIsUsable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "ogcode.json"), []byte(`{"providers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load(project)
	if len(cfg.Skills.Paths) != 0 || len(cfg.Skills.URLs) != 0 || len(cfg.Skills.Permissions) != 0 || len(cfg.Skills.Env) != 0 {
		t.Errorf("expected an empty skills section, got %+v", cfg.Skills)
	}
}

// EnsureProjectFile's template is what most users will edit by hand, so it has
// to parse back into the shape Load expects.
func TestProjectFileTemplate_ParsesIntoAUsableConfig(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(projectFileTemplate), &c); err != nil {
		t.Fatalf("the template ogcode writes is not valid JSON: %v", err)
	}
	if _, ok := c.Providers["anthropic"]; !ok {
		t.Error("template lost its provider section")
	}
	if c.Skills.Paths == nil || c.Skills.URLs == nil {
		t.Error("template should show the skills section so the file is self-documenting")
	}
	if c.Skills.Env == nil {
		t.Error("template should show skills.env so a skill's credentials have a documented place to go")
	}
	if !strings.Contains(projectFileTemplate, `"previewDomain"`) {
		t.Error("template should show previewDomain so serving previews under a real domain is discoverable")
	}
}

// skills.env merges key by key, project-local last, so a secret kept in the
// global config serves every project without being restated and a project can
// override one name without losing the others.
func TestLoad_SkillEnvMergesPerNameWithProjectWinning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	globalDir := filepath.Join(home, ".config", "ogcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(globalDir, "config.json"), `{
		"skills": { "env": { "GLOBAL_ONLY": "g", "OVERRIDDEN": "global" } }
	}`)

	project := t.TempDir()
	writeJSON(t, filepath.Join(project, "ogcode.json"), `{
		"skills": { "env": { "PROJECT_ONLY": "p", "OVERRIDDEN": "project" } }
	}`)

	cfg := Load(project)
	want := map[string]string{
		"GLOBAL_ONLY":  "g",
		"PROJECT_ONLY": "p",
		"OVERRIDDEN":   "project",
	}
	if !reflect.DeepEqual(cfg.Skills.Env, want) {
		t.Errorf("skills.env = %v, want %v (merged per name, project wins)", cfg.Skills.Env, want)
	}
}

// The preview domain merges like a provider base URL: a project value wins, a
// global-only value survives.
func TestLoad_PreviewDomainMergesWithProjectWinning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	globalDir := filepath.Join(home, ".config", "ogcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(globalDir, "config.json"), `{"previewDomain": "global.example.com"}`)

	project := t.TempDir()
	writeJSON(t, filepath.Join(project, "ogcode.json"), `{"previewDomain": "project.example.com"}`)

	if got := Load(project).PreviewDomain; got != "project.example.com" {
		t.Errorf("PreviewDomain = %q, want the project value to win", got)
	}

	// A project that sets none keeps the global value.
	other := t.TempDir()
	writeJSON(t, filepath.Join(other, "ogcode.json"), `{"providers":{}}`)
	if got := Load(other).PreviewDomain; got != "global.example.com" {
		t.Errorf("PreviewDomain = %q, want the global value preserved", got)
	}
}

// ApplyEnv is what makes a `requires:` declaration satisfiable from ogcode.json.
// Without it the config field would be inert — the skill tool checks the process
// environment, so a value that never reaches it reads as missing.
func TestApplyEnv_ExportsSkillEnv(t *testing.T) {
	t.Setenv("OGCODE_TEST_FROM_CONFIG", "")
	os.Unsetenv("OGCODE_TEST_FROM_CONFIG")

	cfg := &Config{Skills: SkillsConfig{Env: map[string]string{"OGCODE_TEST_FROM_CONFIG": "from-config"}}}
	cfg.ApplyEnv()

	if got := os.Getenv("OGCODE_TEST_FROM_CONFIG"); got != "from-config" {
		t.Errorf("OGCODE_TEST_FROM_CONFIG = %q, want the config value exported", got)
	}
}

// A real environment variable beats the config file for skill secrets too. The
// whole point of the field is that it is a fallback for the file, never an
// override of what the operator exported.
func TestApplyEnv_SkillEnvDoesNotOverrideExistingEnv(t *testing.T) {
	t.Setenv("OGCODE_TEST_EXISTING", "from-the-environment")

	cfg := &Config{Skills: SkillsConfig{Env: map[string]string{"OGCODE_TEST_EXISTING": "from-config"}}}
	cfg.ApplyEnv()

	if got := os.Getenv("OGCODE_TEST_EXISTING"); got != "from-the-environment" {
		t.Errorf("OGCODE_TEST_EXISTING = %q, want the environment value preserved", got)
	}
}

// An empty entry is not an exported variable. Exporting "" would make the name
// appear set to an os.Getenv existence check while satisfying nothing — the
// exact confusion the requires check is meant to remove.
func TestApplyEnv_SkipsEmptySkillEnvValues(t *testing.T) {
	os.Unsetenv("OGCODE_TEST_EMPTY_CONFIG")
	cfg := &Config{Skills: SkillsConfig{Env: map[string]string{"OGCODE_TEST_EMPTY_CONFIG": ""}}}
	cfg.ApplyEnv()

	if _, ok := os.LookupEnv("OGCODE_TEST_EMPTY_CONFIG"); ok {
		t.Error("an empty config value must not be exported as a set variable")
	}
}

// A preview domain in the config reaches live-service previews the same way an
// exported one does: ApplyEnv puts it in the environment PreviewDomain reads.
func TestApplyEnv_ExportsPreviewDomain(t *testing.T) {
	t.Setenv("OGCODE_PREVIEW_DOMAIN", "")
	os.Unsetenv("OGCODE_PREVIEW_DOMAIN")

	(&Config{PreviewDomain: "preview.example.com"}).ApplyEnv()

	if got := os.Getenv("OGCODE_PREVIEW_DOMAIN"); got != "preview.example.com" {
		t.Errorf("OGCODE_PREVIEW_DOMAIN = %q, want the config value exported", got)
	}
}

// A real OGCODE_PREVIEW_DOMAIN beats the config file, like every other field.
func TestApplyEnv_PreviewDomainDoesNotOverrideExistingEnv(t *testing.T) {
	t.Setenv("OGCODE_PREVIEW_DOMAIN", "from-the-environment")

	(&Config{PreviewDomain: "from-config"}).ApplyEnv()

	if got := os.Getenv("OGCODE_PREVIEW_DOMAIN"); got != "from-the-environment" {
		t.Errorf("OGCODE_PREVIEW_DOMAIN = %q, want the environment value preserved", got)
	}
}

// An unset config field exports nothing, so PreviewDomain keeps its default.
func TestApplyEnv_SkipsEmptyPreviewDomain(t *testing.T) {
	os.Unsetenv("OGCODE_PREVIEW_DOMAIN")
	(&Config{}).ApplyEnv()
	if _, ok := os.LookupEnv("OGCODE_PREVIEW_DOMAIN"); ok {
		t.Error("an empty config preview domain must not be exported")
	}
}

func TestCustomProvidersFiltersBuiltInSlots(t *testing.T) {
	cfg := &Config{Providers: map[string]ProviderConfig{
		"openai":         {BaseURL: "https://builtin.openai"},
		"my-deepseek":    {Type: "openai", BaseURL: "https://deepseek.example", APIKey: "sk-test"},
		"typeless-entry": {BaseURL: "https://typeless.example"},
	}}
	got := cfg.CustomProviders()
	if len(got) != 2 {
		t.Fatalf("CustomProviders returned %d entries (%v), want 2", len(got), got)
	}
	pc := got["my-deepseek"]
	if pc.BaseURL != "https://deepseek.example" || pc.APIKey != "sk-test" {
		t.Errorf("my-deepseek = %+v, want its baseUrl and apiKey passed through", pc)
	}
	if pc.Type != "openai" {
		t.Errorf("my-deepseek type = %q, want %q", pc.Type, "openai")
	}
	// An empty type means openai, and CustomProviders normalizes it so callers
	// never see "".
	if got["typeless-entry"].Type != "openai" {
		t.Errorf("typeless-entry type = %q, want normalized to %q", got["typeless-entry"].Type, "openai")
	}
}

func TestCustomProvidersSkipsDisabledAndUnknownType(t *testing.T) {
	cfg := &Config{Providers: map[string]ProviderConfig{
		"off":   {BaseURL: "https://off.example", Disabled: true},
		"alien": {BaseURL: "https://alien.example", Type: "anthropic"},
		"ogx":   {BaseURL: "https://first-party.example"},
		"kept":  {BaseURL: "https://kept.example"},
	}}
	got := cfg.CustomProviders()
	if len(got) != 1 {
		t.Fatalf("CustomProviders returned %d entries (%v), want 1", len(got), got)
	}
	if _, ok := got["kept"]; !ok {
		t.Errorf("CustomProviders dropped kept, want it returned")
	}
}

// An entry that is not pointed anywhere — or has no id at all — never comes
// out of CustomProviders: there is nothing a caller could register for it.
func TestCustomProvidersSkipsBaseUrllessEntries(t *testing.T) {
	cfg := &Config{Providers: map[string]ProviderConfig{
		"":      {BaseURL: "https://idless.example"},
		"nourl": {},
		"ok":    {BaseURL: "https://ok.example"},
	}}
	got := cfg.CustomProviders()
	if len(got) != 1 {
		t.Fatalf("CustomProviders returned %d entries (%v), want 1", len(got), got)
	}
	if _, ok := got["ok"]; !ok {
		t.Errorf("CustomProviders dropped ok, want it returned")
	}
}

func TestLoadMergedCustomProviderFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".config", "ogcode")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(globalDir, "config.json"), `{
		"providers": {
			"my-deepseek": {"type": "openai", "baseUrl": "https://global:8000", "apiKey": "global-key", "model": "deepseek-v4.1", "label": "Global"},
			"my-shutoff":  {"type": "openai", "baseUrl": "https://shutoff:8000", "disabled": true}
		}
	}`)

	projectDir := t.TempDir()
	writeJSON(t, filepath.Join(projectDir, "ogcode.json"), `{
		"providers": {
			"my-deepseek": {"baseUrl": "https://project:8000"},
			"my-shutoff":  {}
		}
	}`)

	cfg := Load(projectDir)

	got := cfg.Providers["my-deepseek"]
	if got.BaseURL != "https://project:8000" {
		t.Errorf("my-deepseek baseUrl = %q, want project value to win", got.BaseURL)
	}
	if got.APIKey != "global-key" {
		t.Errorf("my-deepseek apiKey = %q, want global value preserved", got.APIKey)
	}
	if got.Model != "deepseek-v4.1" || got.Label != "Global" {
		t.Errorf("my-deepseek model/label = %q/%q, want global values preserved", got.Model, got.Label)
	}
	// Type must survive the JSON→merge path too: the merge loop copies it like
	// the other fields, and CustomProviders trusts it to filter unsupported
	// protocols. A regression that drops the merge silently turns a disabled-
	// by-unsupported entry back into an OpenAI-compatible provider.
	if got.Type != "openai" {
		t.Errorf("my-deepseek type = %q, want the global value preserved", got.Type)
	}
	if got.Disabled {
		t.Error("my-deepseek disabled = true, want it off")
	}

	if shutoff := cfg.Providers["my-shutoff"]; !shutoff.Disabled {
		t.Error("my-shutoff: project states no disabled, want the global disabled preserved")
	}

	// A project can disable what the global config enabled — and, once
	// disabled, later turns cannot use it (the one-way merge in Load).
	everythingElse := t.TempDir()
	t.Setenv("HOME", everythingElse)
	globalDir2 := filepath.Join(everythingElse, ".config", "ogcode")
	if err := os.MkdirAll(globalDir2, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(globalDir2, "config.json"), `{
		"providers": {
			"my-quiet": {"type": "openai", "baseUrl": "https://quiet:8000"}
		}
	}`)
	projectDir2 := t.TempDir()
	writeJSON(t, filepath.Join(projectDir2, "ogcode.json"), `{
		"providers": {
			"my-quiet": {"type": "openai", "baseUrl": "https://quiet:8000", "disabled": true}
		}
	}`)
	if quiet := Load(projectDir2).Providers["my-quiet"]; !quiet.Disabled {
		t.Error("my-quiet disabled = false, want the project's disabled to win")
	}

	// A project can also RETYPE an entry — and CustomProviders must still see
	// the project-won value, not the global one.
	writeJSON(t, filepath.Join(projectDir2, "ogcode.json"), `{
		"providers": {
			"my-quiet": {"type": "anthropic", "baseUrl": "https://quiet:8000"}
		}
	}`)
	if quiet := Load(projectDir2).Providers["my-quiet"]; quiet.Type != "anthropic" {
		t.Errorf("my-quiet type = %q, want the project's anthropic to win", quiet.Type)
	}
}
