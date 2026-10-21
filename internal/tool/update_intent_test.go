package tool

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func TestParseIntentArgs(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []session.IntentStep
		wantErr bool
	}{
		{
			"a plan with every status",
			`{"steps":[{"step":"read the spec","status":"done"},{"step":"write the tool","status":"active"},{"step":"add tests","status":"pending"},{"step":"ship it","status":"dropped"}]}`,
			[]session.IntentStep{
				{Step: "read the spec", Status: session.IntentDone},
				{Step: "write the tool", Status: session.IntentActive},
				{Step: "add tests", Status: session.IntentPending},
				{Step: "ship it", Status: session.IntentDropped},
			},
			false,
		},
		{
			"a step may carry a description",
			`{"steps":[{"step":"read the spec","status":"active","description":"open the xlsx and list its sheet names"}]}`,
			[]session.IntentStep{{Step: "read the spec", Status: session.IntentActive, Description: "open the xlsx and list its sheet names"}},
			false,
		},
		{
			"labels are trimmed and statuses lowercased",
			`{"steps":[{"step":"  read the spec  ","status":" Done "}]}`,
			[]session.IntentStep{{Step: "read the spec", Status: session.IntentDone}},
			false,
		},
		{"no steps", `{"steps":[]}`, nil, true},
		{"missing steps", `{}`, nil, true},
		{"blank label", `{"steps":[{"step":"   ","status":"active"}]}`, nil, true},
		{"unknown status", `{"steps":[{"step":"go","status":"sideways"}]}`, nil, true},
		{"missing status", `{"steps":[{"step":"go"}]}`, nil, true},
		{"malformed json", `{"steps":`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseIntentArgs(json.RawMessage(tt.raw))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got.Steps)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got.Steps) != len(tt.want) {
				t.Fatalf("steps = %+v, want %+v", got.Steps, tt.want)
			}
			for i := range tt.want {
				if !reflect.DeepEqual(got.Steps[i], tt.want[i]) {
					t.Errorf("step %d = %+v, want %+v", i, got.Steps[i], tt.want[i])
				}
			}
		})
	}
}

// TestParseIntentArgsEnforcesTheStepCap pins both edges of the cap: the plan is
// meant to be read at a glance, so a long one is refused rather than rendered,
// but a plan of exactly the cap must still be accepted.
func TestParseIntentArgsEnforcesTheStepCap(t *testing.T) {
	plan := func(n int) json.RawMessage {
		steps := make([]string, 0, n)
		for i := 0; i < n; i++ {
			steps = append(steps, `{"step":"s","status":"pending"}`)
		}
		return json.RawMessage(`{"steps":[` + strings.Join(steps, ",") + `]}`)
	}

	if _, err := ParseIntentArgs(plan(maxIntentSteps)); err != nil {
		t.Errorf("a plan of %d steps was refused: %v", maxIntentSteps, err)
	}
	if _, err := ParseIntentArgs(plan(maxIntentSteps + 1)); err == nil {
		t.Errorf("a plan of %d steps was accepted; the cap is %d", maxIntentSteps+1, maxIntentSteps)
	}
}

// TestParseIntentArgsTreatsTheDescriptionAsOptional pins both edges: a step may
// carry a description for the matcher, and a step without one is still a valid
// plan (the description is an aid, never a requirement).
func TestParseIntentArgsTreatsTheDescriptionAsOptional(t *testing.T) {
	bare, err := ParseIntentArgs(json.RawMessage(`{"steps":[{"step":"read","status":"active"}]}`))
	if err != nil {
		t.Fatalf("a step with no description was refused: %v", err)
	}
	if bare.Steps[0].Description != "" {
		t.Errorf("description = %q, want empty", bare.Steps[0].Description)
	}

	long := strings.Repeat("x", maxIntentDescriptionChars+100)
	got, err := ParseIntentArgs(json.RawMessage(`{"steps":[{"step":"read","status":"active","description":"` + long + `"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := len([]rune(got.Steps[0].Description)); n != maxIntentDescriptionChars {
		t.Errorf("description = %d runes, want it clamped to %d", n, maxIntentDescriptionChars)
	}
}

// TestParseIntentArgsAcceptsStatusSynonyms pins the fold from the words a model
// reaches for onto the four statuses the plan understands. A whole list used to
// be discarded because one step said "in_progress"; a synonym must now land on
// its canonical status instead. An unmappable word still fails (pinned above).
func TestParseIntentArgsAcceptsStatusSynonyms(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"in_progress", session.IntentActive},
		{"In Progress", session.IntentActive},
		{"in-progress", session.IntentActive},
		{"started", session.IntentActive},
		{"completed", session.IntentDone},
		{"Finished", session.IntentDone},
		{"todo", session.IntentPending},
		{"queued", session.IntentPending},
		{"skipped", session.IntentDropped},
		{"cancelled", session.IntentDropped},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			raw := `{"steps":[{"step":"go","status":"` + tt.status + `"}]}`
			got, err := ParseIntentArgs(json.RawMessage(raw))
			if err != nil {
				t.Fatalf("a synonym was refused: %v", err)
			}
			if got.Steps[0].Status != tt.want {
				t.Errorf("status %q folded to %q, want %q", tt.status, got.Steps[0].Status, tt.want)
			}
		})
	}
}

func TestUpdateIntentRejectionIsNotATurnFailure(t *testing.T) {
	// A malformed plan must leave the plan the user is reading untouched AND let
	// the agent recover. Returning an error here would fail the whole turn.
	res, err := NewUpdateIntentTool().Execute(context.Background(), json.RawMessage(`{"steps":[]}`), Context{})
	if err != nil {
		t.Fatalf("rejection surfaced as an error: %v", err)
	}
	if !strings.Contains(res.Output, "NOT updated") {
		t.Errorf("output does not say the plan was refused: %q", res.Output)
	}
	if !strings.Contains(res.Output, "Nothing changed") {
		t.Error("output does not reassure the agent the plan is intact")
	}
	if _, ok := res.Metadata["intentSet"]; ok {
		t.Error("a rejected plan set intentSet; the loop would have persisted it")
	}
}

func TestUpdateIntentAcceptsAPlan(t *testing.T) {
	res, err := NewUpdateIntentTool().Execute(context.Background(), json.RawMessage(
		`{"steps":[{"step":"read","status":"done"},{"step":"write","status":"active"}]}`), Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Metadata["intentSet"] != true {
		t.Errorf("intentSet = %v, want true", res.Metadata["intentSet"])
	}
	if got := res.Metadata["steps"]; got != 2 {
		t.Errorf("steps = %v, want 2", got)
	}
}

// The description is the only place the timing rule reaches the model when the
// per-turn prompt section is not in view, so it must state the first-call rule
// rather than the deferred boundary it replaced.
func TestUpdateIntentDescriptionPutsThePlanFirst(t *testing.T) {
	desc := NewUpdateIntentTool().Description()
	for _, want := range []string{
		"first, alone in your first block of every turn",
		"before codebase_map, grep or any read",
		"Call it on its own",
	} {
		if !strings.Contains(desc, want) {
			t.Errorf("Description missing %q", want)
		}
	}
	for _, gone := range []string{
		"before the first edit, write or bash call",
		"by your fifth tool call",
		"otherwise orient first",
	} {
		if strings.Contains(desc, gone) {
			t.Errorf("Description still carries the deferred rule %q", gone)
		}
	}
}

// TestUpdateIntentOutputEchoesThePlan pins the continuity surface. The tool
// result is the only place the model sees its own plan again, so it must come
// back verbatim: without it a later call re-derives a re-worded list from memory
// and every step the agent had closed is lost. It carries no invitation to call
// the tool again: that invitation sat freshest in context and sent the model back
// into this tool instead of on to the turn's first exploration call, so closing
// the plan is left to the prompt and the description, which are re-sent every
// step. Only a fully closed plan gets a line of its own.
func TestUpdateIntentOutputEchoesThePlan(t *testing.T) {
	res, err := NewUpdateIntentTool().Execute(context.Background(), json.RawMessage(
		`{"steps":[{"step":"read the spec","status":"done"},{"step":"write the tool","status":"active"},{"step":"add tests","status":"pending"}]}`), Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"1. [done] read the spec", "2. [active] write the tool", "3. [pending] add tests"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("output does not echo %q: %q", want, res.Output)
		}
	}
	if strings.Contains(res.Output, "call it once more") || strings.Contains(res.Output, "before you send the message that ends the turn") {
		t.Errorf("the result invites another update_intent call: %q", res.Output)
	}

	closed, err := NewUpdateIntentTool().Execute(context.Background(), json.RawMessage(
		`{"steps":[{"step":"read the spec","status":"done"},{"step":"write the tool","status":"Dropped"}]}`), Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(closed.Output, "Every step is now done or dropped") {
		t.Errorf("a closed plan did not say so: %q", closed.Output)
	}
	if strings.Contains(closed.Output, "before you send the message that ends the turn") {
		t.Errorf("a closed plan still carried the closing nudge: %q", closed.Output)
	}
}

// TestUpdateIntentDescriptionPointsAtTheMap pins the hand-off the same way,
// from the description the model reads in every request: without it the plan
// can swallow the map call, and the turn starts reading files blind. The plan
// now comes first and the map follows it, so the hand-off has to survive the
// reordering rather than be dropped with it.
func TestUpdateIntentDescriptionPointsAtTheMap(t *testing.T) {
	got := NewUpdateIntentTool().Description()
	for _, want := range []string{
		"exploration opens with codebase_map",
		"the plan says where the turn is going, the map says where the code is",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description does not hand off to codebase_map (%q missing): %q", want, got)
		}
	}
	if strings.Contains(got, "otherwise orient first") {
		t.Error("description still lets orientation precede the plan")
	}
}

func TestUpdateIntentIDIsStable(t *testing.T) {
	// The agent loop matches this name to decide whether to persist a plan.
	// Renaming the tool without updating the loop would silently disable the
	// feature: the call would succeed and nothing would be shown.
	if got := NewUpdateIntentTool().ID(); got != "update_intent" {
		t.Errorf("ID = %q, want update_intent", got)
	}
}

// TestUpdateIntentDescriptionClosesThePlan pins the rule that nothing but the
// agent's own call closes the plan: the description must tell it to make a final
// call before the message that ends the turn, or the last step lingers as active
// after the turn finishes (nothing at turn end touches the intent).
func TestUpdateIntentDescriptionClosesThePlan(t *testing.T) {
	got := NewUpdateIntentTool().Description()
	for _, want := range []string{
		"before the message that ends the turn",
		// The closing call is only worth making if it moves the statuses: a
		// list re-sent unchanged leaves the pill on the plan it opened with.
		"with every step's real status",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description does not tell the agent to close its plan (%q missing): %q", want, got)
		}
	}
}

// TestUpdateIntentDescriptionAsksForItsOwnBlock pins the sequencing rule. The
// plan is what tells the user where the turn is heading, so a call batched with
// the work it describes arrives after that work has already run — and any tool
// the matcher names in the plan's result is offered too late to be used in it.
func TestUpdateIntentDescriptionAsksForItsOwnBlock(t *testing.T) {
	if got := NewUpdateIntentTool().Description(); !strings.Contains(got, "a block that carries no other tool call") {
		t.Errorf("description does not ask for the call to stand alone: %q", got)
	}
}

func TestUpdateIntentParametersAreValidJSONSchema(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(NewUpdateIntentTool().Parameters(), &schema); err != nil {
		t.Fatalf("parameters are not valid JSON: %v", err)
	}
	req, ok := schema["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "steps" {
		t.Errorf("required = %v, want [steps]", schema["required"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties")
	}
	steps, ok := props["steps"].(map[string]any)
	if !ok {
		t.Fatal("schema does not declare a steps property")
	}
	if steps["maxItems"] != float64(maxIntentSteps) {
		t.Errorf("maxItems = %v, want %d", steps["maxItems"], maxIntentSteps)
	}
	items, ok := steps["items"].(map[string]any)
	if !ok {
		t.Fatal("steps does not declare an items object")
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("steps.items does not declare properties")
	}
	desc, ok := itemProps["description"].(map[string]any)
	if !ok {
		t.Fatal("a step does not declare a description property; the matcher would have nothing to read")
	}
	if desc["type"] != "string" {
		t.Errorf("description type = %v, want string", desc["type"])
	}
}
