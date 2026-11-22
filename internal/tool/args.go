package tool

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// DecodeArgs unmarshals a tool call's raw argument JSON into v, coercing
// numbers written in float notation to integers when the value is exactly
// integral ("100.0" and "1e2" both decode as 100).
//
// Integer tool params are declared "type": "integer", but providers ignore
// the hint and some models emit such params with a decimal point anyway.
// Go's JSON decoder rejects any number carrying a decimal point or exponent
// for an int field even when the value is mathematically integral, so those
// calls fail with "parse args: json: cannot unmarshal number 100.0 into Go
// struct field .offset of type int" — an error the model then often retries
// verbatim, burning the rest of the turn.
//
// The coercion runs on the raw bytes before decoding, outside strings, and
// rewrites only literals that parse as integral float64s representable in an
// int64. Everything else — fractional values, quoted numbers, magnitudes
// beyond int64 — is left untouched for the decoder to reject exactly as
// before, so a genuinely wrong argument still fails loudly.
func DecodeArgs(args json.RawMessage, v any) error {
	data := coerceIntegralFloats(args)
	if err := json.Unmarshal(data, v); err == nil {
		return nil
	} else {
		// Stringified-array fallback: some models emit an array param as a
		// JSON string holding the array ("files": "[{\"path\":...}]") rather
		// than the array itself. The plain decode above then fails with a
		// type error that names neither the field nor the fix, and the model
		// retries verbatim. Rewriting the stringified value into the native
		// array/object it holds — and only then, only when the plain decode
		// already failed — recovers the call without touching a string field
		// whose value legitimately looks like JSON (write content, edit
		// anchors), because those decode cleanly on the first pass.
		unwrapped := unwrapStringifiedJSON(args)
		if unwrapped == nil || bytes.Equal(unwrapped, args) {
			return err
		}
		if retryErr := json.Unmarshal(coerceIntegralFloats(unwrapped), v); retryErr == nil {
			return nil
		}
		return err
	}
}

// unwrapStringifiedJSON rewrites a call whose array/object param arrived as a
// JSON string holding the value into the native value it holds, at every
// level. Returns nil when nothing rewritten — so caller keeps original error.
func unwrapStringifiedJSON(args json.RawMessage) []byte {
	var root any
	if json.Unmarshal(args, &root) != nil {
		return nil
	}
	if !unwrapValue(root) {
		return nil
	}
	out, err := json.Marshal(root)
	if err != nil {
		return nil
	}
	return out
}

// unwrapValue recursively replaces string values that parse as a JSON
// array/object with the parsed value. Returns whether anything changed.
func unwrapValue(v any) bool {
	switch node := v.(type) {
	case map[string]any:
		changed := false
		for key, val := range node {
			if s, ok := val.(string); ok {
				if parsed := decodeStringified(s); parsed != nil {
					node[key] = parsed
					changed = true
					continue
				}
			}
			if unwrapValue(val) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for i, elem := range node {
			if s, ok := elem.(string); ok {
				if parsed := decodeStringified(s); parsed != nil {
					node[i] = parsed
					changed = true
					continue
				}
			}
			if unwrapValue(elem) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

// decodeStringified parses s as a JSON array or object, returning nil unless
// it parses cleanly as one of those two composite shapes. Plain strings,
// numbers, and unparseable text are left alone so the decoder's own error
// — and the caller's original intent — survives.
func decodeStringified(s string) any {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) == 0 || (trimmed[0] != '[' && trimmed[0] != '{') {
		return nil
	}
	var parsed any
	dec := json.NewDecoder(strings.NewReader(trimmed))
	if err := dec.Decode(&parsed); err != nil {
		return nil
	}
	if dec.More() {
		return nil
	}
	// Reject trailing garbage after valid JSON.
	if dec.InputOffset() < int64(len(trimmed)) {
		// dec.More only checks within same nesting; ensure all consumed beyond whitespace.
		if strings.TrimSpace(trimmed[dec.InputOffset():]) != "" {
			return nil
		}
	}
	// Must be array or object, not scalar (first byte already ensures).
	switch parsed.(type) {
	case []any, map[string]any:
		return parsed
	default:
		return nil
	}
}

// stringifiedArrayFields names top-level fields whose value arrived as a
// string that parses as a JSON array/object, for schema-shaped error messages
// that restate via example payload rather than error alone.
func stringifiedArrayFields(args json.RawMessage) []string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(args, &fields) != nil {
		return nil
	}
	var names []string
	for key, raw := range fields {
		trimmed := strings.TrimSpace(string(raw))
		if len(trimmed) == 0 || trimmed[0] != '"' {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		if decodeStringified(s) != nil {
			names = append(names, key)
		}
	}
	return names
}

// coerceIntegralFloats returns data with integral float-notation number
// literals rewritten to plain integer form. The scan is byte-level and
// string-aware: literals inside JSON strings are never touched, and data is
// returned unchanged (same slice) when nothing needs rewriting.
func coerceIntegralFloats(data []byte) []byte {
	var out []byte
	copyFrom := 0 // start of the not-yet-copied region of data
	inString := false
	escaped := false
	for i := 0; i < len(data); {
		c := data[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			i++
			continue
		}
		switch {
		case c == '"':
			inString = true
			i++
		case c == '-' || (c >= '0' && c <= '9'):
			start := i
			for i < len(data) && isNumberByte(data[i]) {
				i++
			}
			if repl, ok := integralFloatReplacement(data[start:i]); ok {
				if out == nil {
					out = make([]byte, 0, len(data)+8)
				}
				out = append(out, data[copyFrom:start]...)
				out = append(out, repl...)
				copyFrom = i
			}
		default:
			i++
		}
	}
	if out == nil {
		return data
	}
	return append(out, data[copyFrom:]...)
}

func isNumberByte(c byte) bool {
	return (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}

// integralFloatReplacement reports the plain-integer rewrite of a JSON number
// literal when the literal is mathematically integral but not already written
// as one. ok is false for plain integers (nothing to coerce), genuinely
// fractional values, malformed literals, and magnitudes int64 cannot hold
// exactly — those stay as-is so the decoder produces its usual error.
func integralFloatReplacement(lit []byte) (string, bool) {
	if !bytes.ContainsAny(lit, ".eE") {
		return "", false
	}
	f, err := strconv.ParseFloat(string(lit), 64)
	if err != nil || f != math.Trunc(f) {
		return "", false
	}
	// Reject magnitudes int64 cannot hold before converting: float64(int64(f))
	// is unspecified for out-of-range f, and the round-trip check below must
	// never see it. MinInt64 (-2^63) is exactly representable and stays allowed.
	if f < -float64(math.MaxInt64)-1 || f >= float64(math.MaxInt64) {
		return "", false
	}
	whole := int64(f)
	if float64(whole) != f {
		return "", false
	}
	return strconv.FormatInt(whole, 10), true
}
