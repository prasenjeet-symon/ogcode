package server

import (
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// autoIndexServer builds a Server wired with just the pieces autoIndexDir
// touches: the docindex store, the running guard, and a bus.
func autoIndexServer(t *testing.T) *Server {
	t.Helper()
	srv := newTestServer(t)
	srv.bus = bus.New(64)
	srv.docindexStore = docindex.NewStore(srv.db)
	return srv
}

// autoIndexDir must not start a second run over a directory already being
// indexed. The automatic refresh and a manual build share one guard, so a turn
// that ends mid-build is skipped rather than racing the running pass for the
// same rows.
func TestAutoIndexDir_SkipsWhenAlreadyRunning(t *testing.T) {
	srv := autoIndexServer(t)

	// Simulate a build already in flight.
	srv.docindexMu.Lock()
	srv.docindexRunning = true
	srv.docindexMu.Unlock()

	srv.autoIndexDir(srv.dir, "", "")

	// The guard is still the one that was set: autoIndexDir returned without
	// touching it, so the in-flight run keeps ownership and clears it itself.
	srv.docindexMu.Lock()
	running := srv.docindexRunning
	srv.docindexMu.Unlock()
	if !running {
		t.Error("autoIndexDir cleared the running flag it did not set; the in-flight run has lost ownership")
	}
}

// An empty directory is a no-op: there is nothing to index and no reason to
// claim the running guard.
func TestAutoIndexDir_EmptyDirIsNoOp(t *testing.T) {
	srv := autoIndexServer(t)

	srv.autoIndexDir("", "", "")

	srv.docindexMu.Lock()
	running := srv.docindexRunning
	srv.docindexMu.Unlock()
	if running {
		t.Error("autoIndexDir claimed the running guard for an empty directory")
	}
}
