package agent

import (
	"encoding/json"
	"log/slog"

	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// descentGate is the enforcement point behind the map-before-search rule. The
// prompt says codebase_map is the first exploration step and shows one
// directory level per call, so the descent is what turns folder labels into
// file names. An agent is free to skip it: a grep across the whole tree returns
// matches in one call, reads as a successful step, and nothing below the prompt
// notices that the map was handed over and never descended.
//
// It watches the turn's tool results and appends a reminder to the first search
// that runs while the project index is live and nothing has located files yet —
// no codebase_map call with subdir, and no read or file_map naming a file. Like
// intentGate it is a nudge, not a block: the search has already run, and the
// matches it returned are still true. What the model is missing is the map's
// answer to a different question — which branch the file lives in.
//
// The reminder rides the tool result because the model-facing history is
// rebuilt from the stored output on every later step; a note added outside that
// output is a note the model never sees. One shot per turn: a second reminder
// adds context without adding a decision.
type descentGate struct {
	sessionID session.SessionID
	live      bool
	descended bool
	located   bool
	nudged    bool
}

// newDescentGate arms the gate only when the session's project index actually
// holds files — a nudge to descend sends the agent to a tool that would come
// back empty — and the agent was offered codebase_map at all.
func newDescentGate(sessionID session.SessionID, live bool) *descentGate {
	return &descentGate{sessionID: sessionID, live: live}
}

// observe inspects one executed call and appends the reminder below the result
// of a search that got ahead of the map. It must run before the result is
// written to the store, for the same reason readPressure.observe must: later
// steps rebuild the request from the stored output.
func (g *descentGate) observe(name string, input json.RawMessage, res *tool.Result) {
	if g == nil || res == nil || res.Denied || !g.live || g.nudged {
		return
	}
	switch name {
	case "codebase_map":
		// Any subdir call is a descent, whatever the level: the rule is that
		// the map was navigated, not that it was navigated a set number of
		// times.
		if codebaseMapSubdir(input) != "" {
			g.descended = true
		}
		return
	case "read", "file_map":
		// A named file is a location. The agent has something concrete to work
		// from, so a later search in this turn is about that file, not a guess
		// at the tree.
		g.located = true
		return
	case "grep", "glob":
	default:
		return
	}
	if g.descended || g.located {
		return
	}
	res.Output += descentGateReminder
	if res.Metadata == nil {
		res.Metadata = map[string]any{}
	}
	// Marks the appended text as harness-injected rather than something the
	// tool produced, so the UI can tell them apart.
	res.Metadata["descentReminder"] = true
	g.nudged = true
	slog.Info("descent reminder attached to tool result", "session", g.sessionID, "tool", name)
}

// codebaseMapSubdir returns the subdir the call was scoped to, empty for a
// call at the project root or an argument payload that does not decode.
func codebaseMapSubdir(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var params struct {
		Subdir string `json:"subdir"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return ""
	}
	return params.Subdir
}

// descentGateReminder is a directive, not a report: the model reads it in the
// block it is about to act on, so it says what to do next. The closing
// sentence keeps a legitimate search from reading it as a rebuke — a grep in a
// location the agent already knows is not what this is about.
const descentGateReminder = `

**The search ran before the map did.** This project has an index, and nothing in this turn has used it to locate files: "codebase_map" shows ONE directory level per call, so a search across the whole tree replaces the descent instead of doing it. Call "codebase_map" — at the project root if you have not yet, otherwise with "subdir" set to the folder whose labels match — and descend until the files you want are listed. Then read the ones that matter. If you were searching one location you already know, carry on: this is about guessing at paths the map would have named.`
