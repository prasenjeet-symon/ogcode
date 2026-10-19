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

// A bare call still lists the skills for an agent with no matcher and no
// <available_skills> block — the one case that has no other way to learn the
// names. A session that runs the matcher is refused it, tested below.
func TestSkillTool_ListsSkillsWhenCalledWithoutAName(t *testing.T) {
	project := skillProject(t, map[string]string{
		"git-release": "---\nname: git-release\ndescription: tag a release\n---\nbody\n",
		"excel":       "---\nname: excel\ndescription: read spreadsheets\n---\nbody\n",
	})
	res, err := NewSkillTool(skill.NewLoader(skill.Config{})).Execute(context.Background(), json.RawMessage(`{}`), Context{SessionDir: project})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"git-release — tag a release", "excel — read spreadsheets"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("listing is missing %q:\n%s", want, res.Output)
		}
	}

	// A truly empty listing cannot be reached through Execute: the loader
	// always includes the embedded skills. Pin the pure function instead.
	if got := renderSkillList(skill.NewRegistry(skill.Rules{})); got != "No skills are available in this project." {
		t.Errorf("an empty registry did not say so: %q", got)
	}
}

// The tool is a loader, not a discovery path: the plan states what the agent
// intends and the matcher names the skills that fit, so a description
// advertising a bare listing would send the model the wrong way.
func TestSkillTool_DescriptionMatchesBehaviour(t *testing.T) {
	desc := SkillTool{}.Description()
	for _, gone := range []string{"list the skills", "see what is available", "available_skills", "system prompt"} {
		if strings.Contains(desc, gone) {
			t.Errorf("description still advertises the listing (%q): %s", gone, desc)
		}
	}
	for _, want := range []string{"with the name of the skill", "Do not call it with no name"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description is missing %q: %s", want, desc)
		}
	}
	var params map[string]any
	if err := json.Unmarshal(SkillTool{}.Parameters(), &params); err != nil {
		t.Fatalf("parameters are not valid JSON schema: %v", err)
	}
	// name stays optional so a bare call reaches the refusal below as a
	// readable answer rather than a decode error.
	if _, ok := params["required"]; ok {
		t.Error("name is required — a bare call must reach the refusal, not fail to parse")
	}
}

// A session that runs the intent matcher must not be able to enumerate every
// skill: the matcher names the ones fitted to the plan's steps, and a full
// listing would spend context on all the others. The refusal points the agent
// back at the plan instead of leaving it to guess.
func TestSkillTool_MatcherSessionRefusesAListing(t *testing.T) {
	project := skillProject(t, map[string]string{
		"git-release": "---\nname: git-release\ndescription: tag a release\n---\nbody\n",
	})
	res, err := NewSkillTool(skill.NewLoader(skill.Config{})).Execute(context.Background(), json.RawMessage(`{}`), Context{SessionDir: project, IntentMatcher: true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, leaked := range []string{"git-release", "tag a release", "excel"} {
		if strings.Contains(res.Output, leaked) {
			t.Errorf("the listing leaked into a matcher session (%q): %s", leaked, res.Output)
		}
	}
	for _, want := range []string{"update_intent", "matcher"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("refusal is missing %q: %s", want, res.Output)
		}
	}

	// An unknown name in a matcher session must not become a list either.
	unknown, err := NewSkillTool(skill.NewLoader(skill.Config{})).Execute(context.Background(), json.RawMessage(`{"name":"git-releas"}`), Context{SessionDir: project, IntentMatcher: true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(unknown.Output, "Available skills") || strings.Contains(unknown.Output, "tag a release") {
		t.Errorf("an unknown name enumerated the skills: %s", unknown.Output)
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
