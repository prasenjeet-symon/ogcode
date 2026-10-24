package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// teammateTestArgs marshals a teammate invocation the way the model writes
// one — including the required op when it forgets it.
func teammateTestArgs(t *testing.T, fields map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}

// teammateRun records what one call delivered and answers with a launch
// report, so tests can tell an executed spawn from a refused one.
func teammateRun(calls *int, got *TeammateRequest, caller *string) TeammateFunc {
	return func(_ context.Context, fromSession string, req TeammateRequest) (TeammateResult, error) {
		*calls++
		*got = req
		if caller != nil {
			*caller = fromSession
		}
		return TeammateResult{
			Handle:    "guard-review",
			SessionID: "ses_child",
			Notice:    "Started teammate guard-review (1 working).",
			Live:      1,
		}, nil
	}
}

// TestTeammateSpawnWithoutOpInfersSpawn pins the self-heal for the retry loop
// seen against ogx glm-5.3-flash: only spawn takes a prompt, so a call carrying
// name+prompt but no op is unambiguous — run it as a spawn and say so in the
// output instead of bouncing it.
func TestTeammateSpawnWithoutOpInfersSpawn(t *testing.T) {
	tctx := Context{SessionID: session.SessionID("ses_parent")}
	calls := 0
	var got TeammateRequest
	var fromSession string
	tt := TeammateTool{Run: teammateRun(&calls, &got, &fromSession)}

	res, err := tt.Execute(context.Background(),
		teammateTestArgs(t, map[string]any{"name": "guard-review", "prompt": "verify the guard"}), tctx)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 1 {
		t.Fatalf("the run hook ran %d times, want exactly one inferred spawn", calls)
	}
	if got.Op != "spawn" {
		t.Fatalf("delivered op = %q, want spawn", got.Op)
	}
	if got.Name != "guard-review" || got.Prompt != "verify the guard" {
		t.Fatalf("delivered name/prompt = (%q, %q), want the call's own", got.Name, got.Prompt)
	}
	if fromSession != string(tctx.SessionID) {
		t.Fatalf("delivered session = %q, want the caller's", fromSession)
	}
	if res.Title != "teammate: spawn" {
		t.Errorf("title = %q, want the inferred op", res.Title)
	}
	if !strings.Contains(res.Output, "op omitted") || !strings.Contains(res.Output, "spawn") {
		t.Errorf("output does not say the op was inferred: %q", res.Output)
	}
	if !strings.Contains(res.Output, "Started teammate guard-review") {
		t.Errorf("output lost the spawn report: %q", res.Output)
	}
}

// TestTeammateMissingOpNamesTheOperation pins the shape-aware refusal: an
// op-less call naming only one operation's argument is told which op that
// argument belongs to, and nothing runs. A prompt-less spawn shape ({name}
// alone) stays here rather than being inferred — inference keys on the prompt,
// and this refusal is the correction it needs.
func TestTeammateMissingOpNamesTheOperation(t *testing.T) {
	tctx := Context{SessionID: session.SessionID("ses_parent")}
	cases := []struct {
		name   string
		fields map[string]any
		want   []string
	}{
		{
			"handles names wait",
			map[string]any{"handles": []string{"a", "b"}},
			[]string{`"op": "wait"`},
		},
		{
			"timeout names wait",
			map[string]any{"timeout": 30},
			[]string{`"op": "wait"`},
		},
		{
			"handle names stop or status",
			map[string]any{"handle": "a"},
			[]string{`"op": "stop"`, `"op": "status"`},
		},
		{
			"name means an op-less spawn",
			map[string]any{"name": "guard-review"},
			[]string{`"op": "spawn"`, "prompt"},
		},
		{
			"empty call keeps the generic hint",
			map[string]any{},
			[]string{"op is required — use spawn, wait, stop or status"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			tt := TeammateTool{Run: teammateRun(&calls, &TeammateRequest{}, nil)}
			res, err := tt.Execute(context.Background(), teammateTestArgs(t, tc.fields), tctx)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if calls != 0 {
				t.Errorf("an op-less call ran a teammate operation")
			}
			for _, want := range tc.want {
				if !strings.Contains(res.Output, want) {
					t.Errorf("refusal for %v misses %q: %q", tc.fields, want, res.Output)
				}
			}
		})
	}
}

// TestTeammateExplicitOpsKeepTheirShape pins that the self-heal changed
// nothing for calls that carry op: an explicit spawn runs untouched and says
// nothing about inference, whitespace around op still trims, and an unknown op
// still refuses with the same wording.
func TestTeammateExplicitOpsKeepTheirShape(t *testing.T) {
	tctx := Context{SessionID: session.SessionID("ses_parent")}

	t.Run("an explicit spawn is unchanged", func(t *testing.T) {
		calls := 0
		var got TeammateRequest
		tt := TeammateTool{Run: teammateRun(&calls, &got, nil)}
		res, err := tt.Execute(context.Background(),
			teammateTestArgs(t, map[string]any{"op": "spawn", "name": "ok", "prompt": "do it"}), tctx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if calls != 1 || got.Op != "spawn" {
			t.Fatalf("run hook got %d calls, op %q; want one spawn", calls, got.Op)
		}
		if res.Title != "teammate: spawn" {
			t.Errorf("title = %q, want teammate: spawn", res.Title)
		}
		if strings.Contains(res.Output, "op omitted") {
			t.Errorf("an explicit spawn was reported as inferred: %q", res.Output)
		}
	})

	t.Run("an op with surrounding whitespace still trims", func(t *testing.T) {
		calls := 0
		var got TeammateRequest
		tt := TeammateTool{Run: teammateRun(&calls, &got, nil)}
		if _, err := tt.Execute(context.Background(),
			teammateTestArgs(t, map[string]any{"op": " spawn ", "prompt": "do it"}), tctx); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if got.Op != "spawn" {
			t.Errorf("delivered op = %q, want the trimmed spawn", got.Op)
		}
	})

	t.Run("an unknown op still refuses", func(t *testing.T) {
		calls := 0
		tt := TeammateTool{Run: teammateRun(&calls, &TeammateRequest{}, nil)}
		res, err := tt.Execute(context.Background(), teammateTestArgs(t, map[string]any{"op": "pause"}), tctx)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if calls != 0 {
			t.Errorf("an unknown op ran a teammate operation")
		}
		if !strings.Contains(res.Output, `unknown op "pause"`) {
			t.Errorf("refusal = %q, want the unknown-op wording", res.Output)
		}
	})
}
