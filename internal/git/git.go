package git

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// FileStatus is one entry from `git status --porcelain`.
type FileStatus struct {
	Path   string `json:"path"`   // workspace-relative
	X      string `json:"x"`      // index/staged state code, e.g. "M", "A", " ", "?"
	Y      string `json:"y"`      // worktree state code
	Staged bool   `json:"staged"` // true when X is non-blank and not "?"
}

// Commit is one entry from `git log --format=...`.
type Commit struct {
	SHA     string `json:"sha"`
	Short   string `json:"short"`
	Message string `json:"message"` // subject line only
	Author  string `json:"author"`
	Time    string `json:"time"` // ISO 8601
}

// IsRepo reports whether dir is inside a git work tree.
func IsRepo(dir string) bool {
	return runGit(dir, "rev-parse", "--is-inside-work-tree") == nil
}

// Status returns the working-tree status of dir as a list of FileStatus entries
// (one per changed file). Returns nil, nil when the tree is clean. Returns
// nil, nil (not an error) when dir is not a git repository.
func Status(dir string) ([]FileStatus, error) {
	out, err := runGitOutput(dir, "status", "--porcelain", "-z")
	if err != nil {
		// Not a git repo, or some other benign failure: treat as clean.
		return nil, nil
	}
	return parsePorcelain(out), nil
}

// parsePorcelain parses the NUL-separated output of `git status --porcelain -z`
// into a list of FileStatus entries. Each record is "XY path". For renames and
// copies (X == "R" or "C"), -z mode emits a second NUL-delimited field holding
// the destination path; we keep the destination as Path.
func parsePorcelain(out string) []FileStatus {
	if out == "" {
		return nil
	}
	records := strings.Split(out, "\x00")
	var result []FileStatus
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if rec == "" || len(rec) < 3 {
			continue
		}
		x := string(rec[0])
		y := string(rec[1])
		path := rec[3:]
		// Rename/copy: the next NUL-delimited field is the destination path.
		if (x == "R" || x == "C") && i+1 < len(records) && records[i+1] != "" {
			i++
			path = records[i]
		}
		result = append(result, FileStatus{
			Path:   path,
			X:      x,
			Y:      y,
			Staged: x != " " && x != "?",
		})
	}
	return result
}

// DiffFile returns the raw unified diff for path. When staged is true it diffs
// the index (cached) instead of the working tree. Returns "" when the file has
// no changes at the requested level. Uses --no-color.
//
// Untracked files (git status "??") are diffed with `git diff --no-index
// /dev/null -- path` so that a brand-new file renders as an all-addition hunk
// instead of the empty string plain `git diff` returns for untracked paths.
// Staged untracked files are handled by the normal --cached path.
func DiffFile(dir, path string, staged bool) (string, error) {
	if !staged && isUntracked(dir, path) {
		// git diff --no-index exits non-zero when the inputs differ (which is
		// always the case here, since /dev/null differs from a non-empty file),
		// so a non-nil error is expected and not a failure.
		out, _ := runGitOutput(dir, "diff", "--no-color", "--no-index", "/dev/null", "--", path)
		return out, nil
	}
	args := []string{"diff", "--no-color"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	return runGitOutput(dir, args...)
}

// isUntracked reports whether path is an untracked file in dir's working tree
// (git status code "??"). It returns false when path is tracked, staged, or
// when the status lookup fails for any reason.
func isUntracked(dir, path string) bool {
	out, err := runGitOutput(dir, "status", "--porcelain", "-z", "--", path)
	if err != nil || out == "" {
		return false
	}
	for _, rec := range strings.Split(out, "\x00") {
		if len(rec) < 3 {
			continue
		}
		if rec[3:] == path && rec[0] == '?' && rec[1] == '?' {
			return true
		}
	}
	return false
}

// ShowCommit returns the full unified diff for the given commit (raw text from
// `git show --no-color`).
func ShowCommit(dir, sha string) (string, error) {
	return runGitOutput(dir, "show", "--no-color", sha)
}

// RecentCommits returns the last n commits (default 20 when n <= 0) as Commit
// entries. Returns nil, nil when dir is not a git repository.
func RecentCommits(dir string, n int) ([]Commit, error) {
	if n <= 0 {
		n = 20
	}
	out, err := runGitOutput(dir, "log", "-n", strconv.Itoa(n),
		"--format=%H%x01%h%x01%s%x01%an%x01%aI%x00")
	if err != nil {
		return nil, nil
	}
	return parseLog(out), nil
}

// parseLog parses the output of `git log --format=...%x00` (fields separated by
// 0x01, commits separated by 0x00) into a list of Commit entries.
func parseLog(out string) []Commit {
	out = strings.TrimRight(out, "\x00")
	if out == "" {
		return nil
	}
	var result []Commit
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		fields := strings.Split(rec, "\x01")
		if len(fields) < 5 {
			continue
		}
		result = append(result, Commit{
			SHA:     fields[0],
			Short:   fields[1],
			Message: fields[2],
			Author:  fields[3],
			Time:    fields[4],
		})
	}
	return result
}

// Stage adds the given paths to the index (git add -- <paths...>). It uses
// explicit paths only — never -A — so the caller controls scope. Empty paths
// is a no-op.
func Stage(dir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, paths...)
	return runGit(dir, args...)
}

// Unstage removes the given paths from the index (git reset HEAD -- <paths...>).
// Empty paths is a no-op.
func Unstage(dir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"reset", "HEAD", "--"}, paths...)
	return runGit(dir, args...)
}

// HasStaged reports whether the index has any staged changes.
func HasStaged(dir string) bool {
	out, err := runGitOutput(dir, "diff", "--cached", "--name-only")
	return err == nil && strings.TrimSpace(out) != ""
}

// CommitStaged commits the currently staged changes with the given message. It
// uses a local identity override so it works in repos without user.name/
// user.email configured. Returns an error when nothing is staged.
func CommitStaged(dir, msg string) error {
	if !HasStaged(dir) {
		return fmt.Errorf("Nothing staged to commit.")
	}
	cmd := exec.Command("git", "-c", "user.name=ogcode", "-c", "user.email=ogcode@local",
		"commit", "-m", msg)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s: %w", string(out), err)
	}
	return nil
}

func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), string(out), err)
	}
	return nil
}

func runGitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}
