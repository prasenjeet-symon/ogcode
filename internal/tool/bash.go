package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

type BashTool struct{}

func (BashTool) ID() string          { return "bash" }
func (BashTool) Description() string {
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

	timeout := time.Duration(input.Timeout) * time.Second
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", input.Command)
	cmd.Dir = tctx.SessionDir

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

	return Result{
		Title:  input.Command,
		Output: output,
	}, nil
}