package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// unsupportedKeywords are JSON Schema validation constraints removed from a
// tool's input schema before it is offered to a model.
//
// They are removed because of how some providers consume a tool definition.
// Backends that constrain decoding compile every schema into a grammar, and a
// keyword their compiler does not implement is a hard 400 on the WHOLE request
// — not a warning, and not scoped to the offending tool. Since the complete tool
// list ships with every request of every turn, one such keyword anywhere makes
// that model unusable for the session, whether or not the tool is ever called.
// Cal.com declaring `pattern` on an email field is the case this was written
// for: valid JSON Schema, and fatal on a grammar-compiling provider.
//
// Every keyword here constrains a value without describing it, so removing it
// costs the model nothing: it still receives the type, the description and any
// enum. And the constraint is not actually lost — the MCP server validates the
// real arguments when the call executes, which is the only place validation can
// be enforced anyway.
var unsupportedKeywords = map[string]bool{
	// Strings.
	"pattern": true, "minLength": true, "maxLength": true,
	"contentEncoding": true, "contentMediaType": true,
	// Numbers.
	"minimum": true, "maximum": true, "exclusiveMinimum": true,
	"exclusiveMaximum": true, "multipleOf": true,
	// Arrays.
	"minItems": true, "maxItems": true, "uniqueItems": true,
	"minContains": true, "maxContains": true,
	// Objects. patternProperties and propertyNames carry regexes of their own,
	// and dependentRequired encodes conditional requirements no grammar can
	// express.
	"minProperties": true, "maxProperties": true,
	"patternProperties": true, "propertyNames": true, "dependentRequired": true,
	// Document metadata, meaningless to a tool call.
	"$schema": true, "$id": true, "$comment": true,
}

// schemaMapKeywords hold a map of NAME -> schema. Their keys are author data,
// not keywords — a tool may legitimately have a property called "pattern" — so
// only their values are walked.
var schemaMapKeywords = map[string]bool{
	"properties": true, "$defs": true, "definitions": true, "dependentSchemas": true,
}

// schemaKeywords hold a single subschema, or (for "items" in older drafts) a
// list of them.
var schemaKeywords = map[string]bool{
	"items": true, "additionalProperties": true, "not": true,
	"if": true, "then": true, "else": true, "contains": true,
	"unevaluatedItems": true, "unevaluatedProperties": true,
}

// schemaListKeywords hold a list of subschemas. These stay: they express what a
// value may be, which is meaning rather than constraint.
var schemaListKeywords = map[string]bool{
	"anyOf": true, "oneOf": true, "allOf": true, "prefixItems": true,
}

// sanitizeToolSchema removes the keywords above from a tool's input schema and
// reports which ones it removed, sorted so a log line is stable across runs.
//
// The original schema is returned untouched when it cannot be parsed or
// re-encoded: a tool offered with its awkward schema may fail on some
// providers, but one offered with no schema at all fails everywhere.
func sanitizeToolSchema(raw json.RawMessage) (json.RawMessage, []string) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw, nil
	}
	stripped := map[string]bool{}
	cleaned := sanitizeSchema(doc, stripped)
	if len(stripped) == 0 {
		return raw, nil
	}
	out, err := json.Marshal(cleaned)
	if err != nil {
		return raw, nil
	}
	names := make([]string, 0, len(stripped))
	for k := range stripped {
		names = append(names, k)
	}
	sort.Strings(names)
	return out, names
}

// sanitizeSchema walks one schema object. It is deliberately keyword-aware
// rather than a blind deep scan for the blacklisted names: a property named
// "pattern" or "format" is ordinary author data, and deleting it would silently
// change the tool's signature.
func sanitizeSchema(v any, stripped map[string]bool) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		switch {
		case unsupportedKeywords[k]:
			stripped[k] = true
		case k == "format":
			// Folded into the description instead of dropped. "date-time" tells
			// the model how to write the value, and losing it silently invites a
			// wrongly-formatted argument — a worse outcome than the keyword the
			// provider objected to. `pattern` gets no such treatment: a raw regex
			// in a description is noise the model rarely reads correctly, and the
			// server still rejects a bad value on the call itself.
			stripped[k] = true
			if s, ok := val.(string); ok && s != "" {
				out["description"] = appendFormatHint(m["description"], s)
			}
		case schemaMapKeywords[k]:
			out[k] = sanitizeSchemaMap(val, stripped)
		case schemaKeywords[k]:
			out[k] = sanitizeSchemaOrList(val, stripped)
		case schemaListKeywords[k]:
			out[k] = sanitizeSchemaList(val, stripped)
		default:
			// Everything else is kept verbatim: type, required, enum, const,
			// default, title, description, $ref and anything provider-specific.
			if _, taken := out[k]; !taken {
				out[k] = val
			}
		}
	}
	return out
}

// appendFormatHint states a removed format alongside whatever description the
// field already had.
func appendFormatHint(existing any, format string) string {
	desc, _ := existing.(string)
	hint := fmt.Sprintf("(format: %s)", format)
	if strings.TrimSpace(desc) == "" {
		return hint
	}
	if strings.Contains(desc, hint) {
		return desc
	}
	return desc + " " + hint
}

func sanitizeSchemaMap(v any, stripped map[string]bool) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for name, sub := range m {
		out[name] = sanitizeSchema(sub, stripped)
	}
	return out
}

func sanitizeSchemaList(v any, stripped map[string]bool) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, len(list))
	for i, sub := range list {
		out[i] = sanitizeSchema(sub, stripped)
	}
	return out
}

// sanitizeSchemaOrList covers keywords whose value may be a single schema or,
// in drafts before 2020-12, a list of them.
func sanitizeSchemaOrList(v any, stripped map[string]bool) any {
	if _, ok := v.([]any); ok {
		return sanitizeSchemaList(v, stripped)
	}
	return sanitizeSchema(v, stripped)
}
