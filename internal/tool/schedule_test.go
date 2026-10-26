package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// scheduleTestArgs marshals a schedule invocation the way the model writes
// one — including the required op when it forgets it.
func scheduleTestArgs(t *testing.T, fields map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

// scheduleRun records what one call delivered and answers with a fixed
// scheduled timer, so tests can tell an executed create from a refused one.
func scheduleRun(calls *int, got *ScheduleRequest, caller *string) ScheduleFunc {
	return func(_ context.Context, fromSession string, req ScheduleRequest) (ScheduleResult, error) {
		*calls++
		*got = req
		if caller != nil {
			*caller = fromSession
		}
		// The handler owns the final id and fire time: delay-form creates
		// come back as now+delay in RFC3339, clamped as the handler likes.
		return ScheduleResult{
			ID:     "timer_1",
			FireAt: "2099-01-02T03:04:05Z",
		}, nil
	}
}

// TestScheduleSchema pins the tool's self-description: the id the agent list
// keys on, the three ops, and the required marker the schema carries.
func TestScheduleSchema(t *testing.T) {
	var tool ScheduleTool
	if tool.ID() != "schedule" {
		t.Fatalf("ID() = %q, want schedule", tool.ID())
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(ScheduleTool{}.Parameters(), &schema); err != nil {
		t.Fatalf("parameters are not valid JSON: %v", err)
	}
	for _, want := range []string{"op", "delaySeconds", "fireAt", "note", "id"} {
		if _, ok := schema.Properties[want]; !ok {
			t.Errorf("parameters miss the %q property", want)
		}
	}
	var op struct {
		Enum []string `json:"enum"`
	}
	if err := json.Unmarshal(schema.Properties["op"], &op); err != nil {
		t.Fatalf("op property is not valid JSON: %v", err)
	}
	for _, want := range []string{"create", "list", "cancel"} {
		var found bool
		for _, got := range op.Enum {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("op enum misses %q: %v", want, op.Enum)
		}
	}
}

// TestScheduleCreateRequiresExactlyOneFireTime pins create's refusals: a
// non-empty note, exactly one of delaySeconds or a parseable, strictly future
// RFC3339 fireAt, and a delaySeconds no longer than MaxDelaySeconds. A
// refused call runs nothing.
func TestScheduleCreateRequiresExactlyOneFireTime(t *testing.T) {
	tctx := Context{SessionID: session.SessionID("ses_parent")}
	cases := []struct {
		name   string
		fields map[string]any
		want   []string
	}{
		{
			"neither fire time names both candidates",
			map[string]any{"op": "create", "note": "check the build"},
			[]string{"delaySeconds", "fireAt", "exactly one"},
		},
		{
			"both fire times refuse",
			map[string]any{"op": "create", "note": "check the build", "delaySeconds": 90, "fireAt": "2099-01-02T03:04:05Z"},
			[]string{"delaySeconds", "fireAt", "exactly one"},
		},
		{
			"a past fireAt refuses",
			map[string]any{"op": "create", "note": "check the build", "fireAt": "2020-01-01T00:00:00Z"},
			[]string{"fireAt", "future"},
		},
		{
			"an unparseable fireAt refuses",
			map[string]any{"op": "create", "note": "check the build", "fireAt": "tomorrow at noon"},
			[]string{"fireAt", "RFC3339"},
		},
		{
			"an empty note refuses",
			map[string]any{"op": "create", "note": "   ", "delaySeconds": 90},
			[]string{"note is required"},
		},
		{
			"an over-cap delaySeconds refuses",
			map[string]any{"op": "create", "note": "check the build", "delaySeconds": 9_999_999_999},
			[]string{"too long", "366 days"},
		},
		{
			"a negative delaySeconds refuses with its own wording",
			map[string]any{"op": "create", "note": "check the build", "delaySeconds": -5},
			[]string{"delaySeconds must be a positive number of seconds"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			tt := ScheduleTool{Run: scheduleRun(&calls, &ScheduleRequest{}, nil)}
			res, err := tt.Execute(context.Background(), scheduleTestArgs(t, tc.fields), tctx)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if calls != 0 {
				t.Errorf("a refused create ran the schedule operation")
			}
			for _, want := range tc.want {
				if !strings.Contains(res.Output, want) {
					t.Errorf("refusal for %v misses %q: %q", tc.fields, want, res.Output)
				}
			}
		})
	}
}

// TestScheduleCreatePassesThrough pins both create forms reaching the handler
// with the call's own fields, and the result echoing the id and fire time the
// handler chose.
func TestScheduleCreatePassesThrough(t *testing.T) {
	tctx := Context{SessionID: session.SessionID("ses_parent")}

	t.Run("a delay-form create", func(t *testing.T) {
		calls := 0
		var got ScheduleRequest
		var fromSession string
		tt := ScheduleTool{Run: scheduleRun(&calls, &got, &fromSession)}
		res, err := tt.Execute(context.Background(),
			scheduleTestArgs(t, map[string]any{"op": "create", "note": "check the build", "delaySeconds": 90}), tctx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if calls != 1 {
			t.Fatalf("the run hook ran %d times, want exactly one", calls)
		}
		if got.Op != "create" || got.Note != "check the build" || got.DelaySeconds != 90 || got.FireAt != "" {
			t.Fatalf("delivered request = %+v, want op/note/delaySeconds from the call, no fireAt", got)
		}
		if fromSession != string(tctx.SessionID) {
			t.Fatalf("delivered session = %q, want the caller's", fromSession)
		}
		if res.Title != "schedule: create" {
			t.Errorf("title = %q, want schedule: create", res.Title)
		}
		if !strings.Contains(res.Output, "timer_1") || !strings.Contains(res.Output, "2099-01-02T03:04:05Z") {
			t.Errorf("output does not echo the id and fire time the handler returned: %q", res.Output)
		}
	})

	t.Run("a fireAt-form create", func(t *testing.T) {
		calls := 0
		var got ScheduleRequest
		tt := ScheduleTool{Run: scheduleRun(&calls, &got, nil)}
		if _, err := tt.Execute(context.Background(),
			scheduleTestArgs(t, map[string]any{"op": "create", "note": "check the build", "fireAt": "2099-06-07T08:09:10Z"}), tctx); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if calls != 1 || got.FireAt != "2099-06-07T08:09:10Z" || got.DelaySeconds != 0 {
			t.Fatalf("run hook got %d calls, request %+v; want the fireAt form passed through", calls, got)
		}
	})

	t.Run("a delay exactly at the cap", func(t *testing.T) {
		calls := 0
		var got ScheduleRequest
		tt := ScheduleTool{Run: scheduleRun(&calls, &got, nil)}
		if _, err := tt.Execute(context.Background(),
			scheduleTestArgs(t, map[string]any{"op": "create", "note": "check the build", "delaySeconds": MaxDelaySeconds}), tctx); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if calls != 1 || got.DelaySeconds != MaxDelaySeconds {
			t.Fatalf("run hook got %d calls, request %+v; want a delay at the cap passed through", calls, got)
		}
	})
}

// TestScheduleHintNamesTheOps pins the hint for an unknown op or an op-less
// call: it names create, list and cancel — and for a call carrying a create
// argument, tells the model create is the op it probably meant.
func TestScheduleHintNamesTheOps(t *testing.T) {
	tctx := Context{SessionID: session.SessionID("ses_parent")}
	cases := []struct {
		name   string
		fields map[string]any
		want   []string
	}{
		{
			"an unknown op refuses",
			map[string]any{"op": "pause", "note": "check the build"},
			[]string{`unknown op "pause"`, "create", "list", "cancel"},
		},
		{
			"a note without op names create",
			map[string]any{"note": "check the build"},
			[]string{`"op": "create"`, "delaySeconds", "fireAt"},
		},
		{
			"an empty call keeps the generic hint",
			map[string]any{},
			[]string{"op is required — use create, list or cancel"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			tt := ScheduleTool{Run: scheduleRun(&calls, &ScheduleRequest{}, nil)}
			res, err := tt.Execute(context.Background(), scheduleTestArgs(t, tc.fields), tctx)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if calls != 0 {
				t.Errorf("an op-less call ran a schedule operation")
			}
			for _, want := range tc.want {
				if !strings.Contains(res.Output, want) {
					t.Errorf("hint for %v misses %q: %q", tc.fields, want, res.Output)
				}
			}
		})
	}
}

// TestScheduleListAndCancelPassThrough pins list and cancel reaching the
// handler untouched and rendering what it answered.
func TestScheduleListAndCancelPassThrough(t *testing.T) {
	tctx := Context{SessionID: session.SessionID("ses_parent")}

	t.Run("list", func(t *testing.T) {
		calls := 0
		var got ScheduleRequest
		tt := ScheduleTool{Run: scheduleRun(&calls, &got, nil)}
		res, err := tt.Execute(context.Background(),
			scheduleTestArgs(t, map[string]any{"op": "list"}), tctx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if calls != 1 || got.Op != "list" || got.ID != "" || got.DelaySeconds != 0 {
			t.Fatalf("run hook got %d calls, request %+v; want an untouched list", calls, got)
		}
		if res.Title != "schedule: list" {
			t.Errorf("title = %q, want schedule: list", res.Title)
		}
		if !strings.Contains(res.Output, "timer_1") {
			t.Errorf("list output lost the timer the handler returned: %q", res.Output)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		calls := 0
		var got ScheduleRequest
		tt := ScheduleTool{Run: scheduleRun(&calls, &got, nil)}
		res, err := tt.Execute(context.Background(),
			scheduleTestArgs(t, map[string]any{"op": "cancel", "id": "timer_1"}), tctx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if calls != 1 || got.Op != "cancel" || got.ID != "timer_1" {
			t.Fatalf("run hook got %d calls, request %+v; want the cancel passed through", calls, got)
		}
		if res.Title != "schedule: cancel" {
			t.Errorf("title = %q, want schedule: cancel", res.Title)
		}
		if !strings.Contains(res.Output, "cancelled") || !strings.Contains(res.Output, "timer_1") {
			t.Errorf("cancel output does not echo the cancelled reminder: %q", res.Output)
		}
	})
}
