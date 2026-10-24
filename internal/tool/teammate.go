package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/board"
)

// TeammateFunc carries out one teammate operation for the calling session.
// Implemented by the server and wired in from server.go to avoid the
// tool→server import cycle.
type TeammateFunc func(ctx context.Context, sessionID string, req TeammateRequest) (TeammateResult, error)

// TeammateRequest is one operation on a spawned teammate.
type TeammateRequest struct {
	// Op is spawn, wait, stop or status.
	Op string
	// Name is the slug a spawn asks for. A name already on the roster, or a
	// reserved one (user, system, board), is refused rather than renamed.
	Name   string
	Prompt string
	// Handles is who a wait is watching; empty means every teammate this
	// session spawned.
	Handles []string
	// Handle is the single teammate stop and status act on.
	Handle string
	// Timeout bounds a wait, in seconds.
	Timeout int
}

// TeammateReport is one teammate's final status and the message it left.
type TeammateReport struct {
	Handle    string
	Status    string
	SessionID string
	// Text is the teammate's last message: what it was asked for, its result,
	// or the question it stopped on.
	Text string
}

// TeammateResult is what one teammate operation produced.
type TeammateResult struct {
	Handle    string
	SessionID string
	URL       string
	// Notice is the one-line outcome the tool prints.
	Notice string
	// Reports are the teammates a wait or status watched, with their result.
	Reports []TeammateReport
	// Roster is the board's current view of the caller's teammates.
	Roster []board.Handle
	// Live is the number of agents still working after the operation, so the
	// blast radius of a spawn is visible in the transcript.
	Live int
}

// TeammateTool spawns and supervises the agents that work alongside the build
// agent in the same project directory.
type TeammateTool struct {
	Run TeammateFunc
}

func (TeammateTool) ID() string { return "teammate" }

func (TeammateTool) Description() string {
	return "Spawn and supervise a teammate: another build agent in this same project directory, working at the same time as you. " +
		"`spawn` starts one with a complete standalone prompt and returns its handle and session immediately — work continues in parallel, so it is for work that can happen now, on different files, not for research (`task` is cheaper and read-only). " +
		"`wait` blocks for a bounded time until the teammates it names are done, then reports each one's status and its last message; timing out is normal and reports \"still running\". " +
		"A teammate reports back by finishing — its last message is its result and is handed to you when its turn ends, whether or not you called `wait`. " +
		"`status` shows one teammate's row and last message; `stop` cancels its session and cascades to the teammates it spawned. " +
		"Give it a slug name so the tree stays readable. Two agents editing the same file see a refusal and have to re-read, so split the work by file. " +
		"A teammate can spawn teammates of its own, and there is no limit on how many run at once; spawning costs money and is asked of the user when permissions require it."
}

func (TeammateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"op": {
				"type": "string",
				"enum": ["spawn", "wait", "stop", "status"],
				"description": "The operation — spawn starts a teammate from name + prompt, wait blocks for spawned teammates to finish, stop cancels one by handle, status peeks at one by handle. Required."
			},
			"name": {
				"type": "string",
				"description": "spawn: a short slug for the handle, e.g. auth-worker."
			},
			"prompt": {
				"type": "string",
				"description": "spawn: the complete, standalone task. The teammate does not see this conversation, so include the files, the goal and what to report back."
			},
			"handles": {
				"type": "array",
				"items": {"type": "string"},
				"description": "wait: the teammates to watch. Omit for every teammate you spawned."
			},
			"handle": {
				"type": "string",
				"description": "stop/status: the teammate's handle."
			},
			"timeout": {
				"type": "integer",
				"description": "wait: seconds to wait, at most 120. Defaults to 60."
			}
		},
		"required": ["op"]
	}`)
}

func (t TeammateTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Op      string   `json:"op"`
		Name    string   `json:"name"`
		Prompt  string   `json:"prompt"`
		Handles []string `json:"handles"`
		Handle  string   `json:"handle"`
		Timeout int      `json:"timeout"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	req := TeammateRequest{
		Op:      strings.TrimSpace(input.Op),
		Name:    input.Name,
		Prompt:  input.Prompt,
		Handles: input.Handles,
		Handle:  input.Handle,
		Timeout: input.Timeout,
	}
	// Some models drop the required op on teammate calls (ogx glm-5.3-flash
	// observed). Only spawn takes a prompt, so that shape is unambiguous: run
	// it as a spawn and say so, instead of bouncing the call.
	inferred := false
	if req.Op == "" && strings.TrimSpace(req.Prompt) != "" {
		req.Op = "spawn"
		inferred = true
	}
	switch req.Op {
	case "spawn":
		if strings.TrimSpace(req.Prompt) == "" {
			return Result{Output: "prompt is required — give the teammate a complete, standalone task"}, nil
		}
	case "wait":
	case "stop", "status":
		if strings.TrimSpace(req.Handle) == "" {
			return Result{Output: "handle is required to " + req.Op + " a teammate"}, nil
		}
	case "":
		return Result{Output: missingOpHint(req)}, nil
	default:
		return Result{Output: fmt.Sprintf("unknown op %q — use spawn, wait, stop or status", req.Op)}, nil
	}
	if req.Timeout > maxWaitSeconds {
		req.Timeout = maxWaitSeconds
	}

	if t.Run == nil {
		return Result{Output: "teammates are not available in this environment"}, nil
	}

	res, err := t.Run(ctx, string(tctx.SessionID), req)
	if err != nil {
		return Result{Title: "teammate", Output: fmt.Sprintf("Teammate error: %s", err)}, nil
	}
	out := formatTeammate(res)
	if inferred {
		out = "op omitted — treated as \"spawn\" (name and prompt given)\n\n" + out
	}
	return Result{Title: "teammate: " + req.Op, Output: out}, nil
}

// missingOpHint turns an op-less call into a hint the model can act on: the
// argument it did send names the operation it probably meant.
func missingOpHint(req TeammateRequest) string {
	switch {
	case len(req.Handles) > 0:
		return `"op" is required — "handles" is a wait argument: resend the call with "op": "wait"`
	case req.Timeout > 0:
		return `"op" is required — "timeout" is a wait argument: resend the call with "op": "wait"`
	case req.Handle != "":
		return `"op" is required — "handle" is a stop or status argument: resend the call with "op": "stop" or "op": "status"`
	case req.Name != "":
		return `"op" is required — "name" is a spawn argument: resend the call with "op": "spawn" plus the prompt`
	default:
		return "op is required — use spawn, wait, stop or status"
	}
}

// maxWaitSeconds bounds one wait so a tool call cannot hold the turn open
// longer than the work it is waiting on is worth.
const maxWaitSeconds = 120

func formatTeammate(res TeammateResult) string {
	var b strings.Builder
	if res.Notice != "" {
		b.WriteString(res.Notice)
		b.WriteString("\n")
	}
	if res.Handle != "" && res.SessionID != "" {
		fmt.Fprintf(&b, "handle=%s session=%s", res.Handle, res.SessionID)
		if res.URL != "" {
			fmt.Fprintf(&b, " url=%s", res.URL)
		}
		b.WriteString("\n")
	}
	if len(res.Reports) > 0 {
		b.WriteString("\n")
		for _, r := range res.Reports {
			fmt.Fprintf(&b, "- %s is %s", r.Handle, r.Status)
			if r.SessionID != "" {
				fmt.Fprintf(&b, " — /session/%s", r.SessionID)
			}
			b.WriteString("\n")
			if r.Text != "" {
				fmt.Fprintf(&b, "  last message:\n\n  %s\n", indent(r.Text, "  "))
			}
		}
	}
	if len(res.Roster) > 0 {
		fmt.Fprintf(&b, "\nTeammates (%d live):\n", res.Live)
		b.WriteString(treeText(res.Roster))
	}
	if b.Len() == 0 {
		return "No teammates."
	}
	return b.String()
}

// indent prefixes every line of s, so a multi-line teammate message stays under
// its report line instead of flattening the list around it.
func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
