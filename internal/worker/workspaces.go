package worker

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
)

// discoverWorkspaces enumerates the directories this worker will host sessions
// in. For each configured root it reports the root itself, any git worktrees
// linked from it, and — for multi-user repo clones — each immediate child
// directory that is itself a git repo plus that repo's worktrees (a clone at
// <root>/<slug> hosts its user worktrees at <root>/<slug>/.ogcode/worktrees/…,
// one level below what the root-level scan reaches). Paths are absolute and
// de-duplicated.
func discoverWorkspaces(roots []string) []*cpv1.Workspace {
	seen := map[string]bool{}
	var out []*cpv1.Workspace

	add := func(path string) {
		abs, err := filepath.Abs(path)
		if err != nil || abs == "" || seen[abs] {
			return
		}
		seen[abs] = true
		_, statErr := os.Stat(abs)
		out = append(out, &cpv1.Workspace{
			Path:    abs,
			Name:    filepath.Base(abs),
			Branch:  gitBranch(abs),
			Present: statErr == nil,
		})
	}

	scanDir := func(dir string) {
		for _, wt := range gitWorktrees(dir) {
			add(wt)
		}
		add(dir)
	}

	for _, root := range roots {
		abs, err := filepath.Abs(expandHome(root))
		if err != nil {
			continue
		}
		scanDir(abs)
		entries, err := os.ReadDir(abs)
		if err != nil {
			continue
		}
		for _, e := range entries {
			// Only descend into child directories that are git repos
			// (managed clones); plain non-git children are not workspaces.
			child := filepath.Join(abs, e.Name())
			if e.IsDir() && isGitWorkTree(child) {
				scanDir(child)
			}
		}
	}
	return out
}

// gitWorktrees returns the worktree paths linked from a git repo at dir, or nil
// when dir is not a git repo (or git is unavailable).
func gitWorktrees(dir string) []string {
	cmd := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain")
	outPipe, err := cmd.Output()
	if err != nil {
		return nil
	}
	var paths []string
	sc := bufio.NewScanner(strings.NewReader(string(outPipe)))
	for sc.Scan() {
		line := sc.Text()
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, strings.TrimSpace(p))
		}
	}
	return paths
}

// gitBranch returns the current branch name for a git repo at dir, or "".
func gitBranch(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// expandHome resolves a leading ~ to the user's home directory.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
