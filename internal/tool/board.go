package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/board"
)

// BoardFunc carries out one board operation for the calling session.
// Implemented by the server and wired in from server.go to avoid the
// tool→server import cycle.
type BoardFunc func(ctx context.Context, sessionID string, req BoardRequest) (BoardResult, error)

// BoardRequest is one operation on the project board.
type BoardRequest struct {
	// Op is roster. Empty means roster, so a bare call is useful instead of an
	// error.
	Op string
}

// BoardResult is what one board operation produced.
type BoardResult struct {
	// Roster is every agent on the board; the handles' spawned_by edges make it
	// a tree, which is what the tool prints.
	Roster []board.Handle
	// Notice is the one-line outcome the tool prints.
	Notice string
}

// BoardTool shows the build agent the tree of agents working in the same
// project directory: who spawned whom, and what each one is doing.
type BoardTool struct {
	Run BoardFunc
}

func (BoardTool) ID() string { return "board" }

func (BoardTool) Description() string {
	return "See the tree of agents working in this project directory: every build session and the teammates it spawned, plus background task children, each with its status and session. " +
		"`roster` draws the tree — an indented child is a teammate its parent spawned, running and finished ones both. " +
		"It is how you see who is still working, how many are running, and where to look when one of them stops. " +
		"A child reports back by finishing: its last message is its result, and you are told when it is done. " +
		"There is nothing to post here — coordinate by editing files, and use `teammate` to spawn, wait, stop or inspect one."
}

func (BoardTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"op": {
				"type": "string",
				"enum": ["roster"],
				"description": "The operation. Omit it to get the roster."
			}
		}
	}`)
}

func (t BoardTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var input struct {
		Op string `json:"op"`
	}
	if err := DecodeArgs(args, &input); err != nil {
		return Result{}, fmt.Errorf("parse args: %w", err)
	}

	req := BoardRequest{Op: strings.TrimSpace(input.Op)}
	if req.Op != "" && req.Op != "roster" {
		return Result{Output: fmt.Sprintf("unknown op %q — the board only has roster", req.Op)}, nil
	}

	if t.Run == nil {
		return Result{Output: "the board is not available in this environment"}, nil
	}

	res, err := t.Run(ctx, string(tctx.SessionID), req)
	if err != nil {
		return Result{Title: "board", Output: fmt.Sprintf("Board error: %s", err)}, nil
	}
	return Result{Title: "board: roster", Output: formatBoard(res.Roster, res.Notice)}, nil
}

// formatBoard renders the roster as a tree and appends the notice under it.
func formatBoard(roster []board.Handle, notice string) string {
	var b strings.Builder
	if notice != "" {
		b.WriteString(notice)
		b.WriteString("\n")
	}
	if len(roster) == 0 {
		if b.Len() == 0 {
			return "The board is empty: no build session has joined this project yet."
		}
		return b.String()
	}
	b.WriteString(treeText(roster))
	return b.String()
}

// treeText indents a roster by its spawned_by edges: a handle whose spawner is
// not on the roster is a root, and every other one sits under its parent. A
// cycle — two rows naming each other, which nothing writes but a hand-edited
// database could hold — cannot hang the render: a handle is drawn once, and the
// second visit is skipped.
func treeText(roster []board.Handle) string {
	byHandle := make(map[string]board.Handle, len(roster))
	order := make([]string, 0, len(roster))
	for _, h := range roster {
		byHandle[h.Handle] = h
		order = append(order, h.Handle)
	}
	children := make(map[string][]string, len(roster))
	var roots []string
	for _, name := range order {
		h := byHandle[name]
		if h.SpawnedBy == "" {
			roots = append(roots, name)
			continue
		}
		if _, ok := byHandle[h.SpawnedBy]; !ok {
			roots = append(roots, name)
			continue
		}
		children[h.SpawnedBy] = append(children[h.SpawnedBy], name)
	}

	var b strings.Builder
	drawn := make(map[string]bool, len(roster))
	var walk func(name string, depth int)
	walk = func(name string, depth int) {
		if drawn[name] {
			return
		}
		drawn[name] = true
		h := byHandle[name]
		b.WriteString(strings.Repeat("  ", depth))
		fmt.Fprintf(&b, "- %s  %s  role=%s  session=%s\n", h.Handle, h.Status, h.Role, h.SessionID)
		for _, child := range children[name] {
			walk(child, depth+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	// A roster whose every row names a spawner inside it has no root; draw what
	// is left flat rather than losing it.
	for _, name := range order {
		walk(name, 0)
	}
	return b.String()
}

// liveCount is how many handles are still working: running, or idle between
// turns of an interactive session.
func liveCount(roster []board.Handle) int {
	n := 0
	for _, h := range roster {
		if h.Status == board.StatusRunning || h.Status == board.StatusIdle {
			n++
		}
	}
	return n
}
