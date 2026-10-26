package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// maxIntentSteps caps the plan at a length the session header can show without
// becoming a wall. The plan is meant to be read at a glance, not to replace the
// transcript.
const maxIntentSteps = 7

// maxIntentDescriptionChars bounds a step's description. It is read by a
// matcher, not a person, so long prose buys nothing; a sentence or two is plenty
// and keeps the stored plan small. Past the cap the description is cut rather
// than rejected — a rejected list would wipe the plan the user is reading.
const maxIntentDescriptionChars = 400

// UpdateIntentTool lets the agent say where the turn is heading, ahead of doing
// the work. The transcript is retrospective — a tool row appears only once the
// work is done — so without this the user can only infer the agent's direction
// from a pile of finished actions. The plan is replaced whole on every call (last
// write wins), so it always describes what remains as the agent understands it
// now.
//
// The tool itself only validates. The agent loop does the work: seeing this call,
// it writes the plan onto the session row and announces it, and the session
// header renders it as a pill.
type UpdateIntentTool struct{}

func NewUpdateIntentTool() UpdateIntentTool { return UpdateIntentTool{} }

func (UpdateIntentTool) ID() string { return "update_intent" }

func (UpdateIntentTool) Description() string {
	return "State the plan for the current turn — the steps you mean to take and where you are in them — so the user can see where you are heading without reading the transcript. Call it before the first meaningful exploration or work of the turn — it may ride the first tool block alongside other calls — because the call runs the matcher that names the skills and MCP tools fitted to your steps, and a plan stated after the exploration has already happened spends that match on work that is done. Exploration follows the plan and is unchanged: when the project has an index, exploration opens with codebase_map — the plan says where the turn is going, the map says where the code is. Call it with the full list, then call it again only when the plan changes: when you start a step, finish one, or drop one. Mark a step active before that step's first block, done as soon as its last lands, and dropped the moment you know it is not needed — the statuses ride the work instead of being banked for the closing call. Every call must move the statuses too — they are what the user's pill counts, and a list re-sent with its statuses unchanged updates nothing. Nothing closes the plan for you; a closing status update is welcome but not mandatory — if you send one, give every step's real status. Replace the whole list every time — copy the list the tool echoed back to you and change only what moved, so a step you already finished stays finished. Keep each step's label short — a handful of words — and give it a description that says what the step actually involves: the file types, the library, the command. The description is never shown to the user; a matcher reads it to suggest the skills and MCP tools that could carry the step out, and it can only match what you name. Do not also narrate the steps in prose — this call is the narration."
}

func (UpdateIntentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["steps"],
		"properties": {
			"steps": {
				"type": "array",
				"minItems": 1,
				"maxItems": 7,
				"description": "The full plan, in order. Replaces any previous plan, so include every step still relevant — do not send only the steps that changed.",
				"items": {
					"type": "object",
					"required": ["step", "status"],
					"properties": {
						"step": {"type": "string", "description": "One step, a few words."},
						"status": {"type": "string", "enum": ["pending", "active", "done", "dropped"], "description": "Where the step actually is. pending: not started. active: doing it now. done: finished. dropped: no longer needed. Move it as the work moves — a list re-sent with its statuses unchanged updates nothing."},
						"description": {"type": "string", "description": "Optional. One or two sentences on what this step actually involves — the file types, the library, the command. Never shown to the user: a matcher reads it to suggest the skills and MCP tools that could do the step, so name the concrete things it touches."}
					}
				}
			}
		}
	}`)
}

// ParseIntentArgs extracts and validates the plan from a raw tool call. Exported
// so the agent loop reads the argument through exactly the same parser the tool
// validated it with, rather than a second, drifting copy.
func ParseIntentArgs(raw json.RawMessage) (session.SessionIntent, error) {
	var params struct {
		Steps []struct {
			Step        string `json:"step"`
			Status      string `json:"status"`
			Description string `json:"description"`
		} `json:"steps"`
	}
	if err := DecodeArgs(raw, &params); err != nil {
		return session.SessionIntent{}, err
	}
	if len(params.Steps) == 0 {
		return session.SessionIntent{}, errors.New("steps is required")
	}
	if len(params.Steps) > maxIntentSteps {
		return session.SessionIntent{}, fmt.Errorf("too many steps: %d (a plan holds at most %d)", len(params.Steps), maxIntentSteps)
	}
	steps := make([]session.IntentStep, 0, len(params.Steps))
	for i, s := range params.Steps {
		label := strings.TrimSpace(s.Step)
		if label == "" {
			return session.SessionIntent{}, fmt.Errorf("step %d has no text", i+1)
		}
		status := normalizeIntentStatus(s.Status)
		if !session.ValidIntentStatus(status) {
			return session.SessionIntent{}, fmt.Errorf("step %d has an unknown status %q — use one of pending, active, done, dropped", i+1, s.Status)
		}
		steps = append(steps, session.IntentStep{Step: label, Status: status, Description: clampIntentDescription(s.Description)})
	}
	return session.SessionIntent{Steps: steps}, nil
}

// intentStatusAliases maps the words a model reaches for onto the four statuses
// the plan understands. Keys are already normalised (lowercase, single dashes).
// "doing" is deliberately absent: an ambiguous word is better refused than
// guessed at.
var intentStatusAliases = map[string]string{
	"todo":        session.IntentPending,
	"queued":      session.IntentPending,
	"open":        session.IntentPending,
	"planned":     session.IntentPending,
	"next":        session.IntentPending,
	"in-progress": session.IntentActive,
	"inprogress":  session.IntentActive,
	"started":     session.IntentActive,
	"current":     session.IntentActive,
	"working":     session.IntentActive,
	"running":     session.IntentActive,
	"complete":    session.IntentDone,
	"completed":   session.IntentDone,
	"finished":    session.IntentDone,
	"closed":      session.IntentDone,
	"resolved":    session.IntentDone,
	"canceled":    session.IntentDropped,
	"cancelled":   session.IntentDropped,
	"skipped":     session.IntentDropped,
	"abandoned":   session.IntentDropped,
	"removed":     session.IntentDropped,
}

// normalizeIntentStatus folds a status onto one of the four the plan
// understands. A status is a natural-language slot, so "in_progress" or
// "completed" arrives as often as "active" or "done"; an unrecognised word
// still fails the call, but a synonym no longer discards the whole list over a
// spelling.
func normalizeIntentStatus(s string) string {
	n := strings.ToLower(strings.TrimSpace(s))
	n = strings.ReplaceAll(n, "_", "-")
	n = strings.ReplaceAll(n, " ", "-")
	for strings.Contains(n, "--") {
		n = strings.ReplaceAll(n, "--", "-")
	}
	if alias, ok := intentStatusAliases[n]; ok {
		return alias
	}
	return n
}

// clampIntentDescription trims a step description and cuts it to
// maxIntentDescriptionChars, rune-safe. An over-long one is truncated, not
// rejected: the description is a hint to the matcher, and losing it should not
// cost the agent the whole plan.
func clampIntentDescription(s string) string {
	d := strings.TrimSpace(s)
	if len([]rune(d)) <= maxIntentDescriptionChars {
		return d
	}
	return string([]rune(d)[:maxIntentDescriptionChars])
}

func (UpdateIntentTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	intent, err := ParseIntentArgs(args)
	if err != nil {
		// A rejected list must leave the plan the user is reading untouched.
		// Returning the reason as output (rather than an error) lets the agent
		// correct itself on the next step instead of failing the turn.
		return Result{
			Title:  "Update Intent",
			Output: "The plan was NOT updated: " + err.Error() + ". Nothing changed. Call update_intent again with the full list of steps, each a few words, status one of pending/active/done/dropped.",
		}, nil
	}

	// The list is echoed, not argued for. The rule that closes a turn's plan is
	// already stated in the per-step prompt and in this tool's description, both
	// re-sent on every step; a tail inviting another call here was the freshest
	// instruction in context, and it sent the model straight back into this tool
	// instead of on to the exploration the turn still needed.
	open := false
	for _, s := range intent.Steps {
		if s.Status == session.IntentActive || s.Status == session.IntentPending {
			open = true
		}
	}
	tail := ""
	if !open {
		tail = "\n\nEvery step is now done or dropped."
	}

	return Result{
		Title: "Update Intent",
		// "intentSet" is the agent loop's signal to persist the plan. It is set
		// only on this success path, so a rejected list can never overwrite the
		// plan the user is reading.
		Metadata: map[string]any{"intentSet": true, "steps": len(intent.Steps)},
		Output:   "Plan updated — the user sees these steps:" + renderIntentPlan(intent.Steps) + tail,
	}, nil
}

// renderIntentPlan writes the plan back the way the tool stored it. The tool
// result is the only place the model sees its own plan again, so echoing it
// here is what lets a later call copy the list and change only what moved
// instead of re-deriving a re-worded one and losing the steps it had closed.
func renderIntentPlan(steps []session.IntentStep) string {
	var b strings.Builder
	for i, s := range steps {
		fmt.Fprintf(&b, "\n  %d. [%s] %s", i+1, s.Status, s.Step)
	}
	return b.String()
}
