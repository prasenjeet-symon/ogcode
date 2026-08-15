//go:build !windows

package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestBashTool_DoesNotHangOnBackgroundChild is a regression test for the classic
// os/exec pipe-hang: a command that starts a long-lived background process (a dev
// server, a daemon) which inherits the stdout pipe. Before the WaitDelay +
// process-group fix, cmd.Run() blocked until that child exited — here it would
// hang for the sleep's full 60s (and Abort could not interrupt it), freezing the
// whole agent loop. With the fix, hitting the 1s timeout kills the whole process
// group (child included), the pipe closes, and Execute returns promptly.
func TestBashTool_DoesNotHangOnBackgroundChild(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"command": "sleep 60 & wait", // background child inherits the pipe; shell stays alive
		"timeout": 1,                 // trip the context deadline fast
	})

	done := make(chan struct{})
	go func() {
		_, _ = BashTool{}.Execute(context.Background(), args, Context{SessionDir: t.TempDir()})
		close(done)
	}()

	select {
	case <-done:
		// Returned well before the child's 60s lifetime — the fix works.
	case <-time.After(15 * time.Second):
		t.Fatal("bash Execute hung on a backgrounded child that outlived the shell — WaitDelay/process-group fix regressed")
	}
}
