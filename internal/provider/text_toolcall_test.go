package provider

import (
	"encoding/json"
	"testing"
)

func TestParseTextToolCall(t *testing.T) {
	offered := map[string]bool{"memory_recall": true, "read": true, "bash": true}

	tests := []struct {
		name     string
		text     string
		wantOK   bool
		wantName string
		wantArgs string // canonical JSON; "" when wantOK is false
	}{
		{
			name:     "exact glm-5.2 failure from the DB",
			text:     "Since there isn't a specific function provided, I will generate one.\nHere is the response:\n\n{\"name\":\"memory_recall\",\"parameters\":{\"question\":\"How does the system remind the user of the date?\"}}",
			wantOK:   true,
			wantName: "memory_recall",
			wantArgs: `{"question":"How does the system remind the user of the date?"}`,
		},
		{
			name:     "arguments key instead of parameters",
			text:     `Let me read it: {"name": "read", "arguments": {"path": "main.go"}}`,
			wantOK:   true,
			wantName: "read",
			wantArgs: `{"path":"main.go"}`,
		},
		{
			name:     "double-encoded arguments (stringified JSON)",
			text:     `{"name":"read","arguments":"{\"path\":\"x.go\"}"}`,
			wantOK:   true,
			wantName: "read",
			wantArgs: `{"path":"x.go"}`,
		},
		{
			name:     "no-arg call becomes empty object",
			text:     `I'll call {"name": "read"} now`,
			wantOK:   true,
			wantName: "read",
			wantArgs: `{}`,
		},
		{
			name:     "fenced json block",
			text:     "```json\n{\"name\": \"bash\", \"parameters\": {\"command\": \"ls\"}}\n```",
			wantOK:   true,
			wantName: "bash",
			wantArgs: `{"command":"ls"}`,
		},
		{
			name:     "tool key variant",
			text:     `{"tool":"read","arguments":{"path":"a"}}`,
			wantOK:   true,
			wantName: "read",
			wantArgs: `{"path":"a"}`,
		},
		{
			name:     "picks the object naming an offered tool",
			text:     `{"foo":"bar"} then {"name":"unknown","parameters":{}} then {"name":"read","parameters":{"path":"z"}}`,
			wantOK:   true,
			wantName: "read",
			wantArgs: `{"path":"z"}`,
		},
		// --- must NOT fire ---
		{name: "name not among offered tools", text: `{"name":"delete_everything","parameters":{}}`, wantOK: false},
		{name: "plain prose, no json", text: `Sure, I'll read the file for you.`, wantOK: false},
		{name: "json object without a name", text: `{"path":"main.go","limit":100}`, wantOK: false},
		{name: "braces only inside a string", text: `The syntax is like "{name}" in templates.`, wantOK: false},
		{name: "empty", text: ``, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotArgs, gotOK := parseTextToolCall(tt.text, offered)
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %v, want %v (name=%q args=%s)", gotOK, tt.wantOK, gotName, gotArgs)
			}
			if !tt.wantOK {
				return
			}
			if gotName != tt.wantName {
				t.Errorf("name = %q, want %q", gotName, tt.wantName)
			}
			if !jsonEqual(t, gotArgs, tt.wantArgs) {
				t.Errorf("args = %s, want %s", gotArgs, tt.wantArgs)
			}
		})
	}
}

// jsonEqual compares two JSON documents for semantic equality (key order-independent).
func jsonEqual(t *testing.T, a json.RawMessage, b string) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("got args not valid JSON: %s (%v)", a, err)
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		t.Fatalf("want args not valid JSON: %s (%v)", b, err)
	}
	aj, _ := json.Marshal(av)
	bj, _ := json.Marshal(bv)
	return string(aj) == string(bj)
}
