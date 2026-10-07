package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/git"
	"github.com/prasenjeet-symon/ogcode/internal/plan"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/task"
)

func gh(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("gh", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// TestE2E_RealChainPR drives the real chain-PR code path (openChainPR ->
// git.PushBranch -> git.CreatePR via gh -> recordChainPROutcome) against a real
// throwaway GitHub repo, and asserts a real PR is opened and recorded onto every
// task in the chain. Network + gh + a real GitHub account required, so it is
// gated behind OGCODE_E2E_REALPR=1 and never runs in normal test runs.
//
// Run with:  OGCODE_E2E_REALPR=1 go test ./internal/server/ -run TestE2E_RealChainPR -v
func TestE2E_RealChainPR(t *testing.T) {
	if os.Getenv("OGCODE_E2E_REALPR") != "1" {
		t.Skip("set OGCODE_E2E_REALPR=1 to run the real-GitHub chain-PR e2e")
	}
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not installed")
	}
	// gh and git find the developer's GitHub login under the real home, which
	// TestMain hides. This test builds its Server by hand and never opens the
	// global config DB, so it alone gets the real home back.
	t.Setenv("HOME", realHome)

	// 1. Create a real (private) throwaway repo and clone it.
	repoName := "ogcode-e2e-chainpr-" + time.Now().Format("20060102-150405")
	workdir := t.TempDir()
	if out, err := gh(t, workdir, "repo", "create", repoName, "--private", "--clone"); err != nil {
		t.Fatalf("gh repo create: %s: %v", out, err)
	}
	clone := filepath.Join(workdir, repoName)
	who, _ := gh(t, "", "api", "user", "--jq", ".login")
	fullName := who + "/" + repoName
	t.Logf("created throwaway repo: https://github.com/%s", fullName)

	// Always try to clean up the PR (token lacks delete_repo, so the empty repo
	// is left behind for manual deletion — reported at the end).
	defer func() {
		_, _ = gh(t, clone, "pr", "close", "1", "--delete-branch")
		t.Logf("CLEANUP: closed PR; delete the repo manually: gh repo delete %s --yes", fullName)
	}()

	// 2. Seed an initial commit on main and push.
	if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("# e2e seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, clone, "add", "README.md")
	runGitTest(t, clone, "commit", "-q", "-m", "seed")
	runGitTest(t, clone, "branch", "-M", "main")
	runGitTest(t, clone, "push", "-q", "-u", "origin", "main")

	// 2b. Simulate the user working on a NON-default active branch that is NOT yet
	//     on the remote. The PR must target THIS branch (not main), and the server
	//     must auto-push it so gh can use it as the base.
	const activeBranch = "feature/active-work"
	runGitTest(t, clone, "checkout", "-q", "-b", activeBranch)

	// 3. Build the shared chain branch exactly as the server does — cut from the
	//    current (active) branch; each task branches from it, commits, merges back.
	chainBranch := "chain/pln_e2e-feature"
	if err := git.CreateChainBranch(clone, chainBranch); err != nil {
		t.Fatalf("create chain branch: %v", err)
	}
	wtA, err := git.CreateTaskWorktree(clone, "tsk_A", "schema", chainBranch)
	if err != nil {
		t.Fatalf("worktree A: %v", err)
	}
	commitFileInWorktree(t, wtA.Path, "schema.go", "package feature\n\n// Schema layer\n")
	if err := git.MergeTaskBranch(clone, chainBranch, wtA.BranchName, "Schema layer"); err != nil {
		t.Fatalf("merge A: %v", err)
	}
	wtB, err := git.CreateTaskWorktree(clone, "tsk_B", "handler", chainBranch)
	if err != nil {
		t.Fatalf("worktree B: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wtB.Path, "schema.go")); err != nil {
		t.Fatalf("task B did not see task A's work: %v", err)
	}
	commitFileInWorktree(t, wtB.Path, "handler.go", "package feature\n\n// Handler using the schema\n")
	if err := git.MergeTaskBranch(clone, chainBranch, wtB.BranchName, "Handler logic"); err != nil {
		t.Fatalf("merge B: %v", err)
	}

	// 4. Stand up a real Server pointed at the clone, with the two chain tasks
	//    marked completed — the exact state when the chain tail finishes.
	pdb, err := db.Open(filepath.Join(workdir, "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer pdb.Close()
	s := &Server{
		db:        pdb,
		dir:       clone,
		bus:       bus.New(256),
		store:     session.NewStore(pdb),
		planStore: plan.NewStore(pdb),
		taskStore: task.NewStore(pdb),
	}
	const planID = "pln_e2e"
	makeChainPlan(t, s, planID)
	makeChainTask(t, s, planID, "tsk_A", "Schema layer", chainBranch, task.StatusCompleted)
	makeChainTask(t, s, planID, "tsk_B", "Handler logic", chainBranch, task.StatusCompleted)
	p, _ := s.planStore.Get(planID)
	// The plan captured the active branch at lock time — set it here to drive the base.
	p.BaseBranch = activeBranch
	if err := s.planStore.Update(p); err != nil {
		t.Fatalf("persist base branch: %v", err)
	}

	// 5. The actual code under test: open the chain PR.
	s.openChainPR(chainBranch, p)

	// 6a. Verify the PR really exists on GitHub.
	out, err := gh(t, clone, "pr", "list", "--state", "open", "--json", "number,url,title")
	if err != nil {
		t.Fatalf("gh pr list: %s: %v", out, err)
	}
	var prs []struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		t.Fatalf("parse gh pr list %q: %v", out, err)
	}
	if len(prs) == 0 {
		t.Fatal("no open PR was created on GitHub")
	}
	t.Logf("REAL PR opened: %s (%q)", prs[0].URL, prs[0].Title)

	// 6a-bis. The PR must target the ACTIVE branch (not the default), and that
	//         branch must have been auto-pushed to the remote.
	baseOut, err := gh(t, clone, "pr", "view", strconv.Itoa(prs[0].Number), "--json", "baseRefName")
	if err != nil {
		t.Fatalf("gh pr view: %s: %v", baseOut, err)
	}
	var base struct {
		BaseRefName string `json:"baseRefName"`
	}
	if err := json.Unmarshal([]byte(baseOut), &base); err != nil {
		t.Fatalf("parse baseRefName %q: %v", baseOut, err)
	}
	if base.BaseRefName != activeBranch {
		t.Fatalf("PR base = %q, want active branch %q", base.BaseRefName, activeBranch)
	}
	t.Logf("PR correctly targets active branch: base=%s", base.BaseRefName)
	if ls := runGitTest(t, clone, "ls-remote", "--heads", "origin", activeBranch); !strings.Contains(ls, activeBranch) {
		t.Fatalf("active branch %q was not auto-pushed to the remote", activeBranch)
	}
	t.Logf("active branch auto-pushed to remote: %s", activeBranch)

	// 6b. Verify the PR was recorded onto BOTH chain tasks (the fix for #1).
	for _, id := range []string{"tsk_A", "tsk_B"} {
		fresh, _ := s.taskStore.Get(id)
		if fresh.PRURL == "" {
			t.Errorf("task %s has no PRURL recorded after chain PR", id)
		}
		if fresh.PRNumber == nil {
			t.Errorf("task %s has no PRNumber recorded after chain PR", id)
		}
		if fresh.PRURL != prs[0].URL {
			t.Errorf("task %s PRURL %q != actual PR %q", id, fresh.PRURL, prs[0].URL)
		}
		t.Logf("task %s recorded PR: %s (#%v)", id, fresh.PRURL, deref(fresh.PRNumber))
	}
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
