package skill

import (
	"path/filepath"
	"testing"
)

// isolateHome points UserHomeDir at a temp directory so a test never picks up
// the skills the developer running it happens to have installed.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	return home
}

func TestLoader_ProjectSkillsOverrideEverythingElse(t *testing.T) {
	home := isolateHome(t)
	project := t.TempDir()
	configDir := t.TempDir()

	writeSkill(t, filepath.Join(home, ".config", "ogcode", "skills"), "release",
		"---\nname: release\ndescription: global\n---\nglobal body\n")
	writeSkill(t, configDir, "release",
		"---\nname: release\ndescription: from a configured path\n---\nconfig body\n")
	writeSkill(t, filepath.Join(project, ".agents", "skills"), "release",
		"---\nname: release\ndescription: the project's own\n---\nproject body\n")

	reg := NewLoader(Config{Paths: []string{configDir}}).Load(project)
	got, ok := reg.Get("release")
	if !ok {
		t.Fatal("release not found")
	}
	if got.Source != SourceProject {
		t.Errorf("source = %q, want the project's skill to win", got.Source)
	}
	if got.Description != "the project's own" {
		t.Errorf("description = %q", got.Description)
	}
}

// A skill written for Claude Code works here unchanged — that is the whole
// point of reading .claude/skills.
func TestLoader_ReadsClaudeCompatibleDirectories(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	writeSkill(t, filepath.Join(project, ".claude", "skills"), "borrowed",
		"---\nname: borrowed\ndescription: written for another tool\n---\nbody\n")

	if _, ok := NewLoader(Config{}).Load(project).Get("borrowed"); !ok {
		t.Error("a skill in .claude/skills was not discovered")
	}
}

// The built-in is always there, and a project that writes its own version of it
// replaces it rather than colliding with it.
func TestLoader_BuiltInIsPresentAndOverridable(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()

	reg := NewLoader(Config{}).Load(project)
	builtin, ok := reg.Get("customize-ogcode")
	if !ok {
		t.Fatal("the built-in customize-ogcode skill is missing")
	}
	if builtin.Source != SourceEmbedded {
		t.Errorf("source = %q, want built-in", builtin.Source)
	}

	writeSkill(t, filepath.Join(project, ".agents", "skills"), "customize-ogcode",
		"---\nname: customize-ogcode\ndescription: ours\n---\nours\n")
	override, _ := NewLoader(Config{}).Load(project).Get("customize-ogcode")
	if override.Source != SourceProject {
		t.Errorf("source = %q, want the project's version to replace the built-in", override.Source)
	}
	if override.Description != "ours" {
		t.Errorf("description = %q, want the project's version to win", override.Description)
	}
}

// A configured relative path is relative to the project, not to whatever
// directory the process happens to be running in.
func TestLoader_RelativeConfiguredPathResolvesAgainstTheProject(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	writeSkill(t, filepath.Join(project, "team-skills"), "shared",
		"---\nname: shared\ndescription: team\n---\nbody\n")

	if _, ok := NewLoader(Config{Paths: []string{"team-skills"}}).Load(project).Get("shared"); !ok {
		t.Error("a relative skills path did not resolve against the project directory")
	}
}

// A broken skill must not take the working ones down with it, and the loader
// must never fail the caller — a turn does not stop because a SKILL.md has a
// typo.
func TestLoader_SurvivesABrokenSkill(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	root := filepath.Join(project, ".agents", "skills")
	writeSkill(t, root, "good", "---\nname: good\ndescription: fine\n---\nbody\n")
	writeSkill(t, root, "broken", "this file has no frontmatter\n")

	reg := NewLoader(Config{}).Load(project)
	if _, ok := reg.Get("good"); !ok {
		t.Error("the working skill was lost alongside the broken one")
	}
	if _, ok := reg.Get("broken"); ok {
		t.Error("the broken skill should not be registered")
	}
}

// Permissions come from config and reach the registry, so the prompt listing
// and the tool both read the same verdict.
func TestLoader_AppliesConfiguredPermissions(t *testing.T) {
	isolateHome(t)
	project := t.TempDir()
	root := filepath.Join(project, ".agents", "skills")
	writeSkill(t, root, "internal-docs", "---\nname: internal-docs\ndescription: x\n---\nbody\n")
	writeSkill(t, root, "git-release", "---\nname: git-release\ndescription: y\n---\nbody\n")

	reg := NewLoader(Config{Permissions: map[string]string{"internal-*": "deny"}}).Load(project)
	if got := reg.Action("internal-docs"); got != Deny {
		t.Errorf("internal-docs action = %q, want deny", got)
	}
	for _, s := range reg.Visible() {
		if s.Name == "internal-docs" {
			t.Error("a denied skill was listed as visible")
		}
	}
}
