package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

type BashTool struct{}

func (BashTool) ID() string { return "bash" }
func (BashTool) Description() string {
	if runtime.GOOS == "windows" {
		return "Execute a shell command and return the output. Commands are executed via \"cmd /c\" (Windows cmd.exe) — write Windows cmd.exe-compatible syntax, not POSIX sh."
	}
	return "Execute a shell command and return the output. Commands are executed via \"sh -c\" (POSIX sh) — write POSIX-compatible shell, not bash-only syntax."
}
func (BashTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "The shell command to execute"},
			"timeout": {"type": "number", "description": "Timeout in seconds (default 120)"}
		},
		"required": ["command"]
	}`)
}

func (BashTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	// Defense-in-depth: refuse a small set of high-confidence catastrophic
	// commands (recursive root/home deletion, disk overwrite, mkfs, fork bomb).
	// Returned as a normal Result (not a Go error) so the loop continues and the
	// model sees the refusal and can adjust, rather than the turn hard-failing.
	if bad, reason := isDangerousCommand(input.Command); bad {
		return Result{
			Title:  input.Command,
			Output: fmt.Sprintf("Refused to run: potentially destructive command (%s). If this is genuinely intended, run it yourself outside the agent.", reason),
		}, nil
	}

	timeout := time.Duration(input.Timeout) * time.Second
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Use the OS-native shell so the tool works on every platform. On Windows
	// there is no "sh" by default (only installed via WSL/Git Bash/MSYS2), so a
	// hardcoded "sh -c" fails on every Windows call. cmd /c is always present.
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "cmd", "/c", input.Command)
	} else {
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", input.Command)
	}
	cmd.Dir = tctx.SessionDir

	// Bound how long cmd.Run() can block after the context is cancelled or times
	// out. Without this, a command that spawns a background/daemon process (a dev
	// server, the Python sidecar, etc.) leaves the stdout pipe open, so cmd.Run()
	// waits for EOF forever even after the parent shell is killed — hanging the
	// tool and the whole agent loop. WaitDelay force-closes the pipes shortly
	// after cancellation; setProcessGroup (platform-specific) additionally kills
	// the whole process group so those children are stopped, not left orphaned
	// holding the pipe.
	cmd.WaitDelay = 10 * time.Second
	setProcessGroup(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n" + stderr.String()
	}

	if err != nil && cmdCtx.Err() == context.DeadlineExceeded {
		output += "\n[command timed out]"
	}

	// Cap the output keeping the tail: for build/test/log commands the end
	// carries the error summary and exit status, which is what the model needs.
	// Without this a single verbose command (npm install, go test -v, cat) can
	// dump tens of thousands of tokens that then ride along on every later step.
	output, truncated := TruncateOutput(output, KeepTail)

	return Result{
		Title:     input.Command,
		Output:    output,
		Truncated: truncated,
	}, nil
}