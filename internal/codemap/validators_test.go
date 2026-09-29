package codemap

import (
	"strings"
	"testing"
)

// check runs CheckSource and fails the test on an error.
func check(t *testing.T, path, src string) *CheckResult {
	t.Helper()
	res, err := CheckSource(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func wantClean(t *testing.T, res *CheckResult) {
	t.Helper()
	if !res.OK() {
		t.Errorf("%s: valid file flagged (checked=%v reason=%q): %+v", res.Path, res.Checked, res.Reason, res.Diagnostics)
	}
}

func wantError(t *testing.T, res *CheckResult, line int, msg string) {
	t.Helper()
	if res.OK() || !res.Checked {
		t.Fatalf("%s: broken file not reported (checked=%v)", res.Path, res.Checked)
	}
	d := res.Diagnostics[0]
	if d.Line != line || !strings.Contains(d.Message, msg) {
		t.Errorf("%s: first diagnostic = %d:%d %q, want line %d containing %q", res.Path, d.Line, d.Column, d.Message, line, msg)
	}
}

func TestCheckJSON(t *testing.T) {
	// Most JSON configs are read with comments and trailing commas allowed —
	// tsconfig.json, VS Code settings — so those are valid here.
	wantClean(t, check(t, "tsconfig.json", `{
  // compiler options
  "compilerOptions": {
    "strict": true, /* keep */
    "paths": { "@/*": ["src/*"], },
  },
}
`))
	wantClean(t, check(t, "data.json", "\ufeff{\"a\": [1, 2, {\"b\": null}]}\n"))
	wantClean(t, check(t, "settings.jsonc", "// c\n{\"a\": 1,}\n"))

	// A missing comma is the classic break; the error lands on its line.
	res := check(t, "data.json", "{\n  \"a\": 1\n  \"b\": 2\n}\n")
	wantError(t, res, 3, "invalid character")
	if d := res.Diagnostics[0]; d.Column != 3 {
		t.Errorf("column = %d, want 3 (the stray key)", d.Column)
	}
	wantError(t, check(t, "data.json", "{\"a\": [1, 2}\n"), 1, "invalid character")
	wantError(t, check(t, "data.json", "{\"a\": \"unterminated}\n"), 1, "")
	wantError(t, check(t, "data.json", "   \n"), 1, "empty file")

	// npm reads package.json strictly: a comment there breaks the install.
	wantError(t, check(t, "package.json", "{\n  // no\n  \"name\": \"x\"\n}\n"), 2, "invalid character")
	wantError(t, check(t, "package.json", "{\"name\": \"x\",}\n"), 1, "invalid character")
}

func TestCheckJSONLines(t *testing.T) {
	wantClean(t, check(t, "events.jsonl", "{\"a\":1}\n\n[1,2]\n\"x\"\n"))
	res := check(t, "events.ndjson", "{\"a\":1}\n{\"a\":}\n{\"b\":2}\n{oops}\n")
	wantError(t, res, 2, "invalid character")
	if len(res.Diagnostics) != 2 || res.Diagnostics[1].Line != 4 {
		t.Errorf("want one diagnostic per bad line (2 and 4), got %+v", res.Diagnostics)
	}
}

func TestCheckYAML(t *testing.T) {
	wantClean(t, check(t, "ci.yml", `name: ci
on: [push]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: echo ${{ github.sha }}
---
second: document
`))
	// CloudFormation tags parse without being resolved.
	wantClean(t, check(t, "stack.yaml", "Resources:\n  B:\n    Properties:\n      Name: !Sub '${AWS::StackName}-b'\n      Arn: !GetAtt Role.Arn\n"))

	// An unclosed bracket is reported where it opened.
	wantError(t, check(t, "conf.yaml", "a: 1\nb: [unclosed\nc: 3\n"), 2, "expected ',' or ']'")
	wantError(t, check(t, "conf.yaml", "a:\n  b: 1\n c: 2\n"), 3, "")
	wantError(t, check(t, "conf.yaml", "a: 1\n\tb: 2\n"), 2, "")

	// Template syntax inside a block scalar is text, and the file is plain
	// valid YAML as written.
	wantClean(t, check(t, ".goreleaser.yaml", "archives:\n  - name_template: >-\n      {{ .ProjectName }}_\n      {{- .Version }}\n"))

	// A Helm template is not YAML until rendered: it is not checked, and says
	// why, rather than being reported as broken or as fine.
	res := check(t, "deployment.yaml", "replicas: {{ .Values.replicas }}\n{{- if .Values.x }}\nx: 1\n{{- end }}\n")
	if res.Checked || res.OK() {
		t.Errorf("templated YAML checked = %v, OK = %v; want neither", res.Checked, res.OK())
	}
	if out := RenderCheck(res); !strings.Contains(out, "template syntax") || !strings.Contains(out, "not a passing result") {
		t.Errorf("render does not say why the file was skipped:\n%s", out)
	}
}

func TestCheckTOML(t *testing.T) {
	wantClean(t, check(t, "Cargo.toml", `[package]
name = "x"
version = "0.1.0"

[dependencies]
serde = { version = "1", features = ["derive"] }

[[bin]]
name = "a"
`))
	wantError(t, check(t, "pyproject.toml", "[project]\nname = \"x\nversion = \"1\"\n"), 2, "")
	wantError(t, check(t, "Cargo.toml", "[package]\nname = \"a\"\nname = \"b\"\n"), 3, "")
	wantError(t, check(t, "conf.toml", "[a]\nx = 1\n[a]\ny = 2\n"), 3, "")
}
