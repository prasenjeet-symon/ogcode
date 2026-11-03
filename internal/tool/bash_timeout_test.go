package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// An explicit timeout of 0 or a negative one runs without a deadline. Passing
// the raw value into context.WithTimeout expired the context before the
// command even started, so the caller saw an instant fake "[command timed
// out]" for a command that never ran; an explicit zero now means no deadline.
// An ABSENT timeout still keeps the documented 120s default (the pointer keeps
// the two readings apart), so a sleeping command without a timeout is capped —
// checked with a 2s sleep, which must surface the timeout note at ~1s.
func TestBashTool_NegativeOrZeroTimeoutRunsWithoutDeadline(t *testing.T) {
	for name, timeout := range map[string]int{"zero": 0, "negative": -5} {
		args, _ := json.Marshal(map[string]any{"command": "echo ran", "timeout": timeout})
		res, err := BashTool{}.Execute(context.Background(), args, Context{SessionDir: t.TempDir()})
		if err != nil {
			t.Fatalf("%s: execute: %v", name, err)
		}
		if strings.Contains(res.Output, "[command timed out]") {
			t.Errorf("%s: a non-positive timeout must not read as an instant fake timeout, got: %q", name, res.Output)
		}
		if !strings.Contains(res.Output, "ran") {
			t.Errorf("%s: output lost the command's own text: %q", name, res.Output)
		}
	}
}

func TestBashTool_AbsentTimeoutStillCapsASleepingCommand(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"command": "sleep 30", "timeout": 1})
	res, err := BashTool{}.Execute(context.Background(), args, Context{SessionDir: t.TempDir()})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(res.Output, "[command timed out]") {
		t.Errorf("a timed-out sleeping command must say so, got: %q", res.Output)
	}
}
