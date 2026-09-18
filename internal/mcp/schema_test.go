package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// decode is a shorthand for inspecting a sanitised schema.
func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("result is not a JSON object: %v", err)
	}
	return m
}

// TestSanitizeToolSchema_CalcomEmailPattern is the failure that prompted this.
// Cal.com declares `pattern` on an email field; a provider that compiles tool
// schemas into a decoding grammar rejected the ENTIRE request with
//
//	grammar rejected: tool "mcp_calcom_add_booking_attendee" parameter schema:
//	parameter "email": unsupported schema keyword "pattern"
//
// which killed every turn, not just calls to that tool, because the full tool
// list ships with every request.
func TestSanitizeToolSchema_CalcomEmailPattern(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"bookingUid": {"type": "string", "description": "Booking UID"},
			"email": {
				"type": "string",
				"description": "Attendee email",
				"pattern": "^[^@]+@[^@]+$",
				"minLength": 3
			}
		},
		"required": ["bookingUid", "email"]
	}`)

	out, stripped := sanitizeToolSchema(raw)
	if !reflect.DeepEqual(stripped, []string{"minLength", "pattern"}) {
		t.Errorf("stripped = %v, want [minLength pattern]", stripped)
	}
	if strings.Contains(string(out), "pattern") || strings.Contains(string(out), "minLength") {
		t.Fatalf("keyword survived sanitisation: %s", out)
	}

	// Everything the model actually needs has to survive.
	props := decode(t, out)["properties"].(map[string]any)
	email := props["email"].(map[string]any)
	if email["type"] != "string" {
		t.Errorf("email lost its type: %v", email)
	}
	if email["description"] != "Attendee email" {
		t.Errorf("email lost its description: %v", email)
	}
	if _, ok := props["bookingUid"]; !ok {
		t.Error("an untouched property was dropped")
	}
	req := decode(t, out)["required"].([]any)
	if len(req) != 2 {
		t.Errorf("required = %v, want both fields", req)
	}
}

// TestSanitizeToolSchema_PropertyNamedPattern is the trap a blind deep scan for
// the blacklisted names would fall into. A tool may legitimately have a
// property CALLED "pattern" or "format"; deleting those would silently change
// its signature, which is worse than the problem being solved.
func TestSanitizeToolSchema_PropertyNamedPattern(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "the glob to match"},
			"format":  {"type": "string", "enum": ["json", "yaml"]},
			"maximum": {"type": "integer"}
		},
		"required": ["pattern"]
	}`)

	out, stripped := sanitizeToolSchema(raw)
	if len(stripped) != 0 {
		t.Fatalf("stripped %v — these are property names, not keywords", stripped)
	}
	props := decode(t, out)["properties"].(map[string]any)
	for _, name := range []string{"pattern", "format", "maximum"} {
		if _, ok := props[name]; !ok {
			t.Errorf("property %q was deleted", name)
		}
	}
	// enum is meaning, not constraint, and must survive.
	if f := props["format"].(map[string]any); f["enum"] == nil {
		t.Error("enum was dropped")
	}
}

// TestSanitizeToolSchema_FormatFoldsIntoDescription covers the one keyword that
// is not simply dropped. "date-time" tells the model how to write the value, so
// losing it silently invites a wrongly-formatted argument — a worse outcome than
// the keyword the provider objected to.
func TestSanitizeToolSchema_FormatFoldsIntoDescription(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"start": {"type": "string", "format": "date-time", "description": "Start of the booking"},
			"bare":  {"type": "string", "format": "uri"}
		}
	}`)

	out, stripped := sanitizeToolSchema(raw)
	if !reflect.DeepEqual(stripped, []string{"format"}) {
		t.Errorf("stripped = %v, want [format]", stripped)
	}
	props := decode(t, out)["properties"].(map[string]any)

	start := props["start"].(map[string]any)
	if got := start["description"].(string); got != "Start of the booking (format: date-time)" {
		t.Errorf("description = %q, want the format folded in", got)
	}
	if start["format"] != nil {
		t.Error("format keyword survived")
	}
	// A field with no description gets the hint on its own.
	if got := props["bare"].(map[string]any)["description"]; got != "(format: uri)" {
		t.Errorf("bare description = %v, want the standalone hint", got)
	}
}

// TestSanitizeToolSchema_Nested checks the walk reaches every place a schema
// can hide, since one missed branch leaves the 400 in place.
func TestSanitizeToolSchema_Nested(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"attendees": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {"email": {"type": "string", "pattern": "@"}}
				},
				"maxItems": 10
			},
			"either": {
				"anyOf": [
					{"type": "string", "minLength": 1},
					{"type": "object", "additionalProperties": {"type": "string", "pattern": "x"}}
				]
			}
		},
		"$defs": {
			"helper": {"type": "string", "maxLength": 5}
		}
	}`)

	out, stripped := sanitizeToolSchema(raw)
	for _, kw := range []string{"pattern", "maxItems", "minLength", "maxLength"} {
		if strings.Contains(string(out), `"`+kw+`"`) {
			t.Errorf("%s survived in a nested position: %s", kw, out)
		}
	}
	if len(stripped) != 4 {
		t.Errorf("stripped = %v, want all four keywords reported", stripped)
	}
	// Structure must be intact.
	m := decode(t, out)
	props := m["properties"].(map[string]any)
	items := props["attendees"].(map[string]any)["items"].(map[string]any)
	if items["type"] != "object" {
		t.Errorf("items subschema damaged: %v", items)
	}
	if n := len(props["either"].(map[string]any)["anyOf"].([]any)); n != 2 {
		t.Errorf("anyOf has %d branches, want 2", n)
	}
	if m["$defs"] == nil {
		t.Error("$defs was dropped")
	}
}

// TestSanitizeToolSchema_LeavesCleanSchemasAlone pins that a schema needing no
// work is returned byte-identical, so the common case costs nothing and cannot
// be reformatted into something a provider likes less.
func TestSanitizeToolSchema_LeavesCleanSchemasAlone(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`)
	out, stripped := sanitizeToolSchema(raw)
	if len(stripped) != 0 {
		t.Errorf("stripped = %v, want none", stripped)
	}
	if string(out) != string(raw) {
		t.Errorf("clean schema was rewritten:\n got %s\nwant %s", out, raw)
	}
}

// TestSanitizeToolSchema_UnparseableIsPassedThrough prefers a tool offered with
// an awkward schema over one offered with no schema at all: the first may fail
// on some providers, the second fails everywhere.
func TestSanitizeToolSchema_UnparseableIsPassedThrough(t *testing.T) {
	raw := json.RawMessage(`{"type": "object", oops`)
	out, stripped := sanitizeToolSchema(raw)
	if string(out) != string(raw) || stripped != nil {
		t.Errorf("got (%s, %v), want the input returned untouched", out, stripped)
	}
}
