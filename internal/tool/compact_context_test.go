package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const validSummary = "Task: add a refresh path to auth. Established that middleware/auth.go:40-120 " +
	"holds the token check and that session storage is untouched by this change. Ruled out " +
	"changing the store. Remaining: write the refresh handler and its test."

func TestParseCompactContextArgs(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"valid summary", `{"summary":"` + validSummary + `"}`, validSummary, false},
		{"surrounding whitespace is trimmed", `{"summary":"  ` + validSummary + `  "}`, validSummary, false},
		{"missing summary", `{}`, "", true},
		{"empty summary", `{"summary":""}`, "", true},
		{"whitespace-only summary", `{"summary":"      "}`, "", true},
		{"too short to stand in for the work", `{"summary":"did some stuff"}`, "", true},
		{"malformed json", `{"summary":`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCompactContextArgs(json.RawMessage(tt.raw))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got summary %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("summary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompactContextRejectionIsNotATurnFailure(t *testing.T) {
	// A bad summary must leave the context untouched AND let the agent recover.
	// Returning an error here would fail the whole turn over a fixable mistake.
	res, err := NewCompactContextTool().Execute(context.Background(), json.RawMessage(`{"summary":"nope"}`), Context{})
	if err != nil {
		t.Fatalf("rejection surfaced as an error: %v", err)
	}
	if !strings.Contains(res.Output, "NOT compacted") {
		t.Errorf("output does not say the compaction was refused: %q", res.Output)
	}
	if !strings.Contains(res.Output, "Nothing was dropped") {
		t.Error("output does not reassure the agent its context is intact")
	}
}

func TestCompactContextAcceptsAGoodSummary(t *testing.T) {
	res, err := NewCompactContextTool().Execute(context.Background(), json.RawMessage(`{"summary":"`+validSummary+`"}`), Context{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(res.Output, "NOT compacted") {
		t.Errorf("a valid summary was refused: %q", res.Output)
	}
	if got := res.Metadata["summaryChars"]; got != len(validSummary) {
		t.Errorf("summaryChars = %v, want %d", got, len(validSummary))
	}
}

func TestCompactContextParametersAreValidJSONSchema(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(NewCompactContextTool().Parameters(), &schema); err != nil {
		t.Fatalf("parameters are not valid JSON: %v", err)
	}
	req, ok := schema["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "summary" {
		t.Errorf("required = %v, want [summary]", schema["required"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties")
	}
	if _, ok := props["summary"]; !ok {
		t.Error("schema does not declare a summary property")
	}
}

func TestCompactContextIDIsStable(t *testing.T) {
	// The agent loop matches this name to decide whether to record a watermark.
	// Renaming the tool without updating the loop would silently disable the
	// whole feature: the call would succeed and nothing would be compacted.
	if got := NewCompactContextTool().ID(); got != "compact_context" {
		t.Errorf("ID = %q, want compact_context", got)
	}
}
