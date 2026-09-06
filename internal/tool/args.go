package tool

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
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
	return json.Unmarshal(coerceIntegralFloats(args), v)
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
