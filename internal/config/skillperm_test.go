package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A toggle must not damage the rest of ogcode.json: writing one skill's rule
// leaves providers, mcp and unknown fields exactly as they were.
func TestSetProjectSkillPermissions_PreservesOtherFields(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, ".git"), "") // repo-root boundary for findProjectFile
	path := filepath.Join(dir, "ogcode.json")
	writeJSON(t, path, `{
		"providers": { "anthropic": { "apiKey": "keep-me" } },
		"skills": { "paths": ["team-skills"], "permissions": {} },
		"mcp": {},
		"customField": "left alone"
	}`)

	if _, err := SetProjectSkillPermissions(dir, map[string]string{"docx": "deny"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	providers, _ := got["providers"].(map[string]any)
	anthropic, _ := providers["anthropic"].(map[string]any)
	if anthropic["apiKey"] != "keep-me" {
		t.Errorf("provider key not preserved: %v", got["providers"])
	}
	if got["customField"] != "left alone" {
		t.Errorf("unknown field not preserved: %v", got["customField"])
	}
	skills, _ := got["skills"].(map[string]any)
	paths, _ := skills["paths"].([]any)
	if len(paths) != 1 || paths[0] != "team-skills" {
		t.Errorf("skills.paths not preserved: %v", skills["paths"])
	}
	perms, _ := skills["permissions"].(map[string]any)
	if perms["docx"] != "deny" {
		t.Errorf("permission not written: %v", perms)
	}

	// And it reads back through the typed accessor.
	back, backPath := ProjectSkillPermissions(dir)
	if backPath != path {
		t.Errorf("path = %q, want %q", backPath, path)
	}
	if back["docx"] != "deny" {
		t.Errorf("ProjectSkillPermissions = %v, want docx:deny", back)
	}
}

// With no ogcode.json anywhere up the tree, the first toggle creates one.
func TestSetProjectSkillPermissions_CreatesFileWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, ".git"), "") // bound findProjectFile's upward walk

	if perms, path := ProjectSkillPermissions(dir); path != "" || len(perms) != 0 {
		t.Fatalf("expected no project file yet, got path=%q perms=%v", path, perms)
	}

	written, err := SetProjectSkillPermissions(dir, map[string]string{"pdf": "deny"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if want := filepath.Join(dir, "ogcode.json"); written != want {
		t.Errorf("created at %q, want %q", written, want)
	}
	if perms, _ := ProjectSkillPermissions(dir); perms["pdf"] != "deny" {
		t.Errorf("read back %v, want pdf:deny", perms)
	}
}

// Enabling a skill deletes its rule; the write must drop the key, not leave a
// stale "allow" behind that would clutter the file.
func TestSetProjectSkillPermissions_RemovesKey(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, ".git"), "")
	writeJSON(t, filepath.Join(dir, "ogcode.json"), `{"skills":{"permissions":{"docx":"deny","pdf":"deny"}}}`)

	perms, _ := ProjectSkillPermissions(dir)
	delete(perms, "docx")
	if _, err := SetProjectSkillPermissions(dir, perms); err != nil {
		t.Fatalf("write: %v", err)
	}

	back, _ := ProjectSkillPermissions(dir)
	if _, ok := back["docx"]; ok {
		t.Errorf("docx rule should be gone, got %v", back)
	}
	if back["pdf"] != "deny" {
		t.Errorf("pdf rule should remain, got %v", back)
	}
}
