//go:build !windows

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func bashRun(t *testing.T, command string) Result {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"command": command})
	res, err := BashTool{}.Execute(context.Background(), args, Context{SessionDir: t.TempDir()})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

// A command that fails without printing anything used to return an empty,
// success-shaped result: cmd.Run's error was consulted only to spot a timeout,
// so the exit status never reached the model — a grep with no match, a script
// dying under set -e, a quiet build failure all looked like clean runs, and
// the loop carried on as if the step had landed.
func TestBashTool_QuietFailureReportsExitStatus(t *testing.T) {
	res := bashRun(t, "exit 3")

	if !strings.Contains(res.Output, "[exit status 3]") {
		t.Errorf("a quiet failure must surface its exit status, got: %q", res.Output)
	}
}

// A failure that did print keeps its output, with the status appended — the
// stderr text locates the problem, the status confirms it was fatal.
func TestBashTool_FailureKeepsOutputAndAddsStatus(t *testing.T) {
	res := bashRun(t, "echo broken >&2; exit 1")

	for _, want := range []string{"broken", "[exit status 1]"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("output should contain %q, got: %q", want, res.Output)
		}
	}
}

// Success stays unannotated: empty output plus no note is the normal shape of
// a quiet successful command, and stamping every run would be noise.
func TestBashTool_SuccessStaysUnannotated(t *testing.T) {
	res := bashRun(t, "echo ok")

	if strings.Contains(res.Output, "exit status") {
		t.Errorf("a successful run must not carry a status note, got: %q", res.Output)
	}
	if !strings.Contains(res.Output, "ok") {
		t.Errorf("output lost the command's own text: %q", res.Output)
	}
}
