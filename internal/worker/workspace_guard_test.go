package worker

import (
	"path/filepath"
	"testing"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
)

// TestWorker_WorkspaceAllowed pins the containment contract: a StartAgent's
// directory must be one the worker itself offered — a configured --workspace
// root, or anything under one. Everything else (a sibling, a parent, an
// absolute path elsewhere on disk, a traversal) is refused before any
// bootstrap or git clone can touch the disk.
func TestWorker_WorkspaceAllowed(t *testing.T) {
	root := t.TempDir()
	under := filepath.Join(root, "repo")
	nested := filepath.Join(root, "a", "b")
	w := New(Options{Workspaces: []string{root}})

	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"root itself", root, true},
		{"direct child", under, true},
		{"deeply nested child", nested, true},
		{"parent of root", filepath.Dir(root), false},
		{"sibling of root", filepath.Join(filepath.Dir(root), "elsewhere"), false},
		{"absent child (clone target)", filepath.Join(root, "fresh-clone"), true},
		{"relative path", "relative/dir", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.workspaceAllowed(tc.dir); got != tc.want {
				t.Errorf("workspaceAllowed(%q) = %v, want %v", tc.dir, got, tc.want)
			}
		})
	}

	// Traversal cannot smuggle a path outside a root: the component-wise
	// check rejects it even though it lexically normalizes inside.
	if w.workspaceAllowed(filepath.Join(root, "..", "elsewhere")) {
		t.Errorf("traversal path %q accepted", filepath.Join(root, "..", "elsewhere"))
	}

	// A worker with no configured roots hosts nothing.
	if (New(Options{})).workspaceAllowed(root) {
		t.Error("worker with no roots accepted a workspace")
	}
}

// TestStartAgent_RefusesNonAdvertisedWorkspace pins that handleCommand's
// StartAgent path fails cleanly (and hosts nothing, spawns no server, clones
// nothing) when the workspace is outside the worker's advertised set.
func TestStartAgent_RefusesNonAdvertisedWorkspace(t *testing.T) {
	w := newHostingWorker(t)
	w.tunnelled = emptySyncMap()

	outside := t.TempDir() // a sibling of nothing the worker offers
	err := w.startAgent(&cpv1.StartAgent{
		SessionId: "ses_outside",
		Workspace: outside,
		Prompt:    "should never run",
	})
	if err == nil {
		t.Fatal("startAgent accepted a non-advertised workspace")
	}
	if w.sessions.has("ses_outside") {
		t.Error("rejected StartAgent still hosted the session")
	}
	if sh := w.hostServerFor("ses_outside"); sh != nil {
		t.Error("rejected StartAgent spawned a worktree server")
	}
}
