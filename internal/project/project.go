// Package project resolves the stable identity of the workspace a session
// belongs to. Agentic memory is stored in a single database shared by every
// workspace on the machine, so rows need a project key to be recallable
// workspace-by-workspace.
package project

import (
	"path/filepath"
	"sync"
)

var (
	mu    sync.RWMutex
	cache = map[string]string{}
)

// Resolve returns the canonical project key for a working directory.
//
// The key is the directory's absolute, symlink-resolved path. macOS in
// particular hands out /var and /tmp paths that resolve to /private/..., and an
// unresolved path would split one workspace's memory into two projects.
//
// Note that a git worktree resolves to its own path, not the main repository
// root, so a task worktree keeps a memory pool separate from the repo it was
// branched from. Change this function to fold worktrees into their parent repo.
func Resolve(dir string) string {
	if dir == "" {
		return ""
	}
	mu.RLock()
	cached, ok := cache[dir]
	mu.RUnlock()
	if ok {
		return cached
	}

	resolved := dir
	if abs, err := filepath.Abs(dir); err == nil {
		resolved = abs
	}
	// EvalSymlinks fails on paths that do not exist yet; the absolute path is
	// still a usable key in that case.
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	}
	resolved = filepath.Clean(resolved)

	mu.Lock()
	cache[dir] = resolved
	mu.Unlock()
	return resolved
}
