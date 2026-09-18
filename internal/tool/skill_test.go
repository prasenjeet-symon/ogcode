package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/skill"
)

// skillProject builds a project directory with the given skills under
// .agents/skills, isolating the home directory so the developer's own installed
// skills never leak into the result.
func skillProject(t *testing.T, skills map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	project := t.TempDir()
	for name, content := range skills {
		dir := filepath.Join(project, ".agents", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return project
}

func runSkillTool(t *testing.T, cfg skill.Config, dir, name string) Result {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"name": name})
	res, err := NewSkillTool(skill.NewLoader(cfg)).Execute(context.Background(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

func TestSkillTool_ReturnsBodyBaseDirAndFiles(t *testing.T) {
	project := skillProject(t, map[string]string{
		"git-release": "---\nname: git-release\ndescription: tag a release\n---\n## Steps\n\nRun scripts/release.sh.\n",
	})
	dir := filepath.Join(project, ".agents", "skills", "git-release")
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "release.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := runSkillTool(t, skill.Config{}, project, "git-release").Output

	for _, want := range []string{
		`<skill_content name="git-release">`,
		"Run scripts/release.sh.", // the body itself
		dir,                       // the base directory relative paths resolve against
		"<file>scripts/release.sh</file>",
		"</skill_content>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

// The model reached for this because a skill looked relevant, and the usual
// cause of a miss is a near-miss on the name. Listing the alternatives is what
// lets it recover in the same turn.
func TestSkillTool_UnknownNameListsTheAlternatives(t *testing.T) {
	project := skillProject(t, map[string]string{
		"git-release": "---\nname: git-release\ndescription: x\n---\nbody\n",
	})
	out := runSkillTool(t, skill.Config{}, project, "git-releases").Output

	if !strings.Contains(out, "No skill named") {
		t.Errorf("output should say the name was not found: %s", out)
	}
	if !strings.Contains(out, "git-release") {
		t.Errorf("output should name the available skills: %s", out)
	}
}

// Denied skills are already withheld from the prompt, but headless runs have no
// permission gate at all, so the tool refuses them itself.
func TestSkillTool_RefusesADeniedSkill(t *testing.T) {
	project := skillProject(t, map[string]string{
		"internal-docs": "---\nname: internal-docs\ndescription: x\n---\nsecret body\n",
	})
	cfg := skill.Config{Permissions: map[string]string{"internal-*": "deny"}}

	out := runSkillTool(t, cfg, project, "internal-docs").Output
	if strings.Contains(out, "secret body") {
		t.Error("a denied skill's body was returned")
	}
	if !strings.Contains(out, "denied") {
		t.Errorf("output should say why nothing was loaded: %s", out)
	}
}

// A built-in has no directory on disk. Telling the model otherwise would send
// it looking for files that were never there.
func TestSkillTool_BuiltInReportsNoBaseDirectory(t *testing.T) {
	project := skillProject(t, nil)
	out := runSkillTool(t, skill.Config{}, project, "customize-ogcode").Output

	if strings.Contains(out, "Base directory for this skill") {
		t.Errorf("a built-in skill must not claim a base directory: %s", out)
	}
	if !strings.Contains(out, "built-in skill") {
		t.Errorf("output should say it is built in: %s", out)
	}
	if !strings.Contains(out, "ogcode.json") {
		t.Errorf("the built-in skill's body was not returned: %s", out)
	}
}

// A skill that declares a credential it does not have is refused before its
// body is handed over. Showing the body with a warning would walk the agent
// into the failure the declaration exists to prevent, and the refusal has to
// name the variable so the user can supply it.
func TestSkillTool_RefusesWhenARequiredVarIsMissing(t *testing.T) {
	t.Setenv("OGCODE_TEST_PRESENT", "set")
	project := skillProject(t, map[string]string{
		"deploy": "---\nname: deploy\ndescription: ship it\nrequires: OGCODE_TEST_PRESENT, OGCODE_TEST_ABSENT\n---\nrun scripts/deploy.sh with the token\n",
	})

	res := runSkillTool(t, skill.Config{}, project, "deploy")

	if strings.Contains(res.Output, "run scripts/deploy.sh") {
		t.Error("the body was returned despite a missing requirement")
	}
	for _, want := range []string{"OGCODE_TEST_ABSENT", "was not loaded", "skills.env"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("refusal missing %q:\n%s", want, res.Output)
		}
	}
	// The variable that IS set must not be reported as missing.
	if strings.Contains(res.Output, "OGCODE_TEST_PRESENT") {
		t.Errorf("a satisfied requirement was reported missing:\n%s", res.Output)
	}
	if missing, ok := res.Metadata["missing"].([]string); !ok || len(missing) != 1 || missing[0] != "OGCODE_TEST_ABSENT" {
		t.Errorf("metadata missing = %v, want [OGCODE_TEST_ABSENT]", res.Metadata["missing"])
	}
}

// The check is the whole feature only if a satisfied declaration loads normally.
func TestSkillTool_LoadsWhenRequirementsAreSatisfied(t *testing.T) {
	t.Setenv("OGCODE_TEST_PRESENT", "set")
	project := skillProject(t, map[string]string{
		"deploy": "---\nname: deploy\ndescription: ship it\nrequires: OGCODE_TEST_PRESENT\n---\nrun scripts/deploy.sh\n",
	})

	out := runSkillTool(t, skill.Config{}, project, "deploy").Output
	if !strings.Contains(out, "run scripts/deploy.sh") {
		t.Errorf("a satisfied requirement blocked the load:\n%s", out)
	}
	if !strings.Contains(out, `<skill_content name="deploy">`) {
		t.Errorf("expected the normal skill block:\n%s", out)
	}
}

// A skill with no `requires` is unaffected — the field is optional and almost
// every skill omits it.
func TestSkillTool_SkillWithoutRequirementsLoads(t *testing.T) {
	project := skillProject(t, map[string]string{
		"plain": "---\nname: plain\ndescription: nothing needed\n---\nplain body\n",
	})
	if out := runSkillTool(t, skill.Config{}, project, "plain").Output; !strings.Contains(out, "plain body") {
		t.Errorf("a skill with no requirements did not load:\n%s", out)
	}
}

func TestSkillTool_RequiresAName(t *testing.T) {
	tool := NewSkillTool(skill.NewLoader(skill.Config{}))
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"  "}`), Context{SessionDir: t.TempDir()}); err == nil {
		t.Error("expected an error for a blank name")
	}
}

// The tool is described to the model in terms of the prompt block it reads the
// names from; a description that does not connect the two leaves it guessing at
// what to pass.
func TestSkillTool_DescriptionMatchesBehaviour(t *testing.T) {
	desc := SkillTool{}.Description()
	for _, want := range []string{"available_skills", "system prompt"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description should mention %q: %s", want, desc)
		}
	}
	var params map[string]any
	if err := json.Unmarshal(SkillTool{}.Parameters(), &params); err != nil {
		t.Fatalf("parameters are not valid JSON schema: %v", err)
	}
}

// A skill body is instructions the agent is meant to follow. When one was
// written by whoever hosts a configured URL rather than by the developer, the
// output has to say so — the agent has no other way to tell the two apart.
func TestRenderSkill_MarksRemoteProvenance(t *testing.T) {
	remote := renderSkill(skill.Skill{Name: "shared", Dir: t.TempDir(), Content: "body", Source: skill.SourceRemote})
	if !strings.Contains(remote, "skills URL configured in ogcode.json") {
		t.Errorf("a remote skill must be marked as such:\n%s", remote)
	}

	// A skill the developer wrote carries no such note — it would be noise on
	// every load of every local skill.
	local := renderSkill(skill.Skill{Name: "shared", Dir: t.TempDir(), Content: "body", Source: skill.SourceProject})
	if strings.Contains(local, "skills URL configured") {
		t.Errorf("a project skill must not be marked as remote:\n%s", local)
	}
}

// The provenance caveat qualifies how the body should be read, so it has to
// come before it. A body placed first could carry text that passes itself off
// as the end of the block, leaving the caveat looking like separate content.
func TestRenderSkill_ProvenanceComesBeforeTheBody(t *testing.T) {
	out := renderSkill(skill.Skill{
		Name:    "shared",
		Dir:     t.TempDir(),
		Content: "THE BODY",
		Source:  skill.SourceRemote,
	})
	notice := strings.Index(out, "skills URL configured in ogcode.json")
	body := strings.Index(out, "THE BODY")
	if notice < 0 || body < 0 {
		t.Fatalf("output is missing the notice or the body:\n%s", out)
	}
	if notice > body {
		t.Errorf("the provenance notice must precede the body it qualifies:\n%s", out)
	}
}
