package tool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The tool package binds model-supplied JSON into Go structs whose numeric
// fields are ints. Go's decoder rejects any literal carrying a decimal point
// for an int field even when the value is integral, and models that ignore
// "type": "integer" (grok emits "offset": 100.0) then fail every retry the
// same way. DecodeArgs is the tolerant seam; these tests pin both directions:
// integral float notation coerces, everything genuinely wrong still errors.

type decodeArgsFixture struct {
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
	Path   string `json:"path"`
}

func TestDecodeArgs_CoercesIntegralFloatNotation(t *testing.T) {
	for _, args := range []string{
		`{"offset": 100.0, "limit": 3}`,
		`{"offset": 100.000, "limit": 3}`,
		`{"offset": 1e2, "limit": 3}`,
		`{"offset": -2.0, "limit": 3}`,
		`{"offset": 2E1, "limit": 3}`,
	} {
		var in decodeArgsFixture
		if err := DecodeArgs(json.RawMessage(args), &in); err != nil {
			t.Errorf("DecodeArgs(%s): %v", args, err)
			continue
		}
		if in.Offset != 100 && !strings.Contains(args, `"offset": -2.0`) && !strings.Contains(args, `2E1`) {
			t.Errorf("DecodeArgs(%s): offset = %d, want 100", args, in.Offset)
		}
		if strings.Contains(args, `"offset": -2.0`) && in.Offset != -2 {
			t.Errorf("DecodeArgs(%s): offset = %d, want -2", args, in.Offset)
		}
		if strings.Contains(args, `2E1`) && in.Offset != 20 {
			t.Errorf("DecodeArgs(%s): offset = %d, want 20", args, in.Offset)
		}
	}
}

func TestDecodeArgs_LeavesPlainIntegersAlone(t *testing.T) {
	var in decodeArgsFixture
	if err := DecodeArgs(json.RawMessage(`{"offset": 100, "limit": 3, "path": "f.go"}`), &in); err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if in.Offset != 100 || in.Limit != 3 || in.Path != "f.go" {
		t.Errorf("got %+v, want offset 100 limit 3 path f.go", in)
	}
}

func TestDecodeArgs_RejectsFractionalValues(t *testing.T) {
	for _, args := range []string{
		`{"offset": 100.5}`,
		`{"offset": 1e-2}`,
		`{"limit": 0.0001}`,
	} {
		var in decodeArgsFixture
		if err := DecodeArgs(json.RawMessage(args), &in); err == nil {
			t.Errorf("DecodeArgs(%s) accepted a fractional value", args)
		}
	}
}

func TestDecodeArgs_RejectsStringAndMalformedValues(t *testing.T) {
	for _, args := range []string{
		`{"offset": "100"}`,     // string, not a number
		`{"offset": true}`,      // wrong type entirely
		`{"offset": 1e30}`,      // magnitude int64 cannot hold
		`{"offset": 1e999}`,     // ParseFloat range error
		`{"offset": 100.0 100}`, // trailing garbage
		`{`,                     // not JSON at all
	} {
		var in decodeArgsFixture
		if err := DecodeArgs(json.RawMessage(args), &in); err == nil {
			t.Errorf("DecodeArgs(%s) was accepted, want an error", args)
		}
	}
}

// Precision-lossy literals near 2^63 coerce to the nearest representable
// int64 — the same value a float64-typed field would have received — while
// anything beyond the float64 representation of int64's bounds is rejected.
func TestDecodeArgs_Int64Boundaries(t *testing.T) {
	var ok struct {
		N int64 `json:"n"`
	}
	if err := DecodeArgs(json.RawMessage(`{"n": 9007199254740993.0}`), &ok); err != nil {
		t.Fatalf("2^53+1 as float: %v", err)
	}
	if ok.N != 9007199254740992 {
		t.Errorf("got %d, want the nearest representable 9007199254740992", ok.N)
	}
	if err := DecodeArgs(json.RawMessage(`{"n": -9223372036854775808.0}`), &ok); err != nil || ok.N != -9223372036854775808 {
		t.Errorf("MinInt64 as float: err=%v n=%d", err, ok.N)
	}
	if err := DecodeArgs(json.RawMessage(`{"n": 9223372036854775808.0}`), &ok); err == nil {
		t.Error("2^63 was accepted, want an error")
	}
}

func TestDecodeArgs_NestedStructsAndArrays(t *testing.T) {
	var in struct {
		Pages []struct {
			PageNum int `json:"page_num"`
		} `json:"pages"`
	}
	if err := DecodeArgs(json.RawMessage(`{"pages": [{"page_num": 3.0}, {"page_num": 7}]}`), &in); err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if len(in.Pages) != 2 || in.Pages[0].PageNum != 3 || in.Pages[1].PageNum != 7 {
		t.Errorf("got %+v, want page_num 3 and 7", in.Pages)
	}
}

// The coercion is byte-level: number-notation text inside JSON string values
// is content, not syntax, and must survive untouched.
func TestDecodeArgs_LeavesNumberTextInsideStrings(t *testing.T) {
	var in decodeArgsFixture
	if err := DecodeArgs(json.RawMessage(`{"path": "v100.0/final", "limit": 3, "offset": 1.0}`), &in); err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if in.Path != "v100.0/final" {
		t.Errorf("string content rewritten: got %q", in.Path)
	}
	if in.Offset != 1 {
		t.Errorf("offset = %d, want 1", in.Offset)
	}
}

// End-to-end regression for the reported failure: a read call whose args
// carry decimal-point offsets ("offset": 100.0) failed with
// "parse args: json: cannot unmarshal number 100.0 into Go struct field
// .offset of type int" and the model kept retrying the same call.
func TestReadTool_AcceptsIntegralFloatArgs(t *testing.T) {
	path := numbered(t, 20)
	args := json.RawMessage(`{"path": ` + quoteJSON(t, path) + `, "offset": 5.0, "limit": 3.0}`)
	res, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: filepath.Dir(path)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"line6", "line7", "line8"} {
		if !strings.Contains(res.Output, lineNumberSep+want) {
			t.Errorf("window missing %s:\n%s", want, res.Output)
		}
	}
	if strings.Contains(res.Output, lineNumberSep+"line5") || strings.Contains(res.Output, lineNumberSep+"line9") {
		t.Errorf("window wrong:\n%s", res.Output)
	}
}

// Same tolerance, second shape: models that ignore "type": "array" and emit
// the array as a JSON string ("files": "[{...}]") fail Go's decoder with
// "cannot unmarshal string into Go struct field". DecodeArgs unwraps a
// stringified array/object in place on the generic value walk, so every tool
// with an array param — read, submit_doc_index, edit, ask_user,
// update_intent, teammate — recovers without a per-tool special case. The
// unwrap only runs when the plain decode has already failed, so a string
// field whose content merely looks like JSON keeps its bytes.

type stringifiedFilesFixture struct {
	Files []struct {
		Path   string `json:"path"`
		Ranges []struct {
			StartLine int `json:"start_line"`
		} `json:"ranges"`
	} `json:"files"`
}

func TestDecodeArgs_UnwrapsStringifiedArray(t *testing.T) {
	var in stringifiedFilesFixture
	args := `{"files": "[{\"path\": \"a.go\", \"ranges\": [{\"start_line\": 3}]}]"}`
	if err := DecodeArgs(json.RawMessage(args), &in); err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if len(in.Files) != 1 || in.Files[0].Path != "a.go" {
		t.Fatalf("got %+v, want one file a.go", in.Files)
	}
	if len(in.Files[0].Ranges) != 1 || in.Files[0].Ranges[0].StartLine != 3 {
		t.Errorf("nested ranges wrong: %+v", in.Files[0].Ranges)
	}
}

func TestDecodeArgs_UnwrapsNestedStringifiedArray(t *testing.T) {
	var in struct {
		Pages []struct {
			PageNum int      `json:"page_num"`
			Labels  []string `json:"labels"`
		} `json:"pages"`
	}
	args := `{"pages": [{"page_num": 1, "labels": "[\"auth\", \"indexing\"]"}]}`
	if err := DecodeArgs(json.RawMessage(args), &in); err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if len(in.Pages) != 1 || len(in.Pages[0].Labels) != 2 ||
		in.Pages[0].Labels[0] != "auth" || in.Pages[0].Labels[1] != "indexing" {
		t.Errorf("got %+v, want labels [auth indexing]", in.Pages)
	}
}

func TestDecodeArgs_UnwrapsStringifiedObject(t *testing.T) {
	var in struct {
		Context struct {
			Mode string `json:"mode"`
		} `json:"ctx"`
	}
	args := `{"ctx": "{\"mode\": \"fast\"}"}`
	if err := DecodeArgs(json.RawMessage(args), &in); err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if in.Context.Mode != "fast" {
		t.Errorf("got %q, want fast", in.Context.Mode)
	}
}

func TestDecodeArgs_PlainStringFieldUntouched(t *testing.T) {
	// The plain decode succeeds here, so the unwrap never runs: a string
	// field whose content looks like JSON is content, like the number text
	// above, and must not be rewritten to the array it resembles.
	var in decodeArgsFixture
	if err := DecodeArgs(json.RawMessage(`{"path": "[1,2,3]", "offset": 1}`), &in); err != nil {
		t.Fatalf("DecodeArgs: %v", err)
	}
	if in.Path != "[1,2,3]" {
		t.Errorf("string content rewritten: got %q", in.Path)
	}
}

func TestDecodeArgs_RejectsStringifiedGarbage(t *testing.T) {
	// Unwrapping is structural, not forgiving: a string that is not a clean
	// JSON array/object — or carries trailing garbage after one — is still an
	// error, and so is a number passed as a string.
	for _, args := range []string{
		`{"files": "[not json]"}`,
		`{"files": "[{\"path\": \"a.go\"}] trailing"}`,
		`{"files": "a.go"}`,
		`{"files": 3}`,
	} {
		var in stringifiedFilesFixture
		if err := DecodeArgs(json.RawMessage(args), &in); err == nil {
			t.Errorf("DecodeArgs(%s) was accepted, want an error", args)
		}
	}
}

// End-to-end regression for the reported failure: a read call whose args
// stringified the files array ("files": "[{...}]") failed with an unmarshal
// error and the model retried the same call. Now the call runs.
func TestReadTool_AcceptsStringifiedArrayArgs(t *testing.T) {
	path := numbered(t, 20)
	// Build the stringified form the way a model emits it: the files value is
	// the array's JSON text as a string, so the inner payload is marshaled
	// once as its own JSON and again as the string the tool receives.
	inner, err := json.Marshal([]map[string]any{
		{"path": path, "ranges": []map[string]any{{"start_line": 2, "end_line": 4}}},
	})
	if err != nil {
		t.Fatalf("marshal inner files: %v", err)
	}
	args, err := json.Marshal(map[string]any{"files": string(inner)})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	res, err := ReadTool{}.Execute(context.Background(), args, Context{SessionDir: filepath.Dir(path)})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"line2", "line3", "line4"} {
		if !strings.Contains(res.Output, lineNumberSep+want) {
			t.Errorf("window missing %s:\n%s", want, res.Output)
		}
	}
	if strings.Contains(res.Output, lineNumberSep+"line5") {
		t.Errorf("window wrong:\n%s", res.Output)
	}
}

func quoteJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal path: %v", err)
	}
	return string(b)
}

// Integer-bound params must be declared "type": "integer" in the schema so
// models that honour the hint never emit decimal notation in the first
// place; DecodeArgs is the safety net for the ones that don't.
func TestToolSchemas_IntegerParamsDeclareInteger(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params json.RawMessage
	}{
		{"read", ReadTool{}.Parameters()},
		{"bash", BashTool{}.Parameters()},
		{"web_search", WebSearchTool{}.Parameters()},
	} {
		if strings.Contains(string(tc.params), `"type": "number"`) {
			t.Errorf("%s schema still declares a number-typed param:\n%s", tc.name, tc.params)
		}
	}
}
