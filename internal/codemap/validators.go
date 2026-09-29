package codemap

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// validator checks a data format no tree-sitter grammar is compiled in for,
// with Go's own parser for it. These formats have no declarations to outline,
// so they sit beside the registry rather than in it: file_map is unaffected.
//
// check returns the problems found, or a non-empty reason when the file,
// although of this type, cannot honestly be checked.
type validator struct {
	name  string
	check func(path string, src []byte) (findings []finding, reason string)
}

var validators = map[string]*validator{
	".json":   {name: "json", check: checkJSONFile},
	".jsonc":  {name: "json", check: func(_ string, src []byte) ([]finding, string) { return checkJSON(src, true), "" }},
	".jsonl":  {name: "json lines", check: checkJSONLines},
	".ndjson": {name: "json lines", check: checkJSONLines},
	".yaml":   {name: "yaml", check: checkYAML},
	".yml":    {name: "yaml", check: checkYAML},
	".toml":   {name: "toml", check: checkTOML},
}

func validatorFor(path string) *validator {
	return validators[strings.ToLower(filepath.Ext(path))]
}

// strictJSON names the files whose readers reject comments and trailing
// commas. Everywhere else they are accepted: tsconfig.json, VS Code settings,
// devcontainer.json and most tool configs are read as JSON with comments, and
// flagging their comments would be flagging valid files.
var strictJSON = map[string]bool{
	"package.json":      true,
	"package-lock.json": true,
	"composer.json":     true,
}

func checkJSONFile(path string, src []byte) ([]finding, string) {
	return checkJSON(src, !strictJSON[strings.ToLower(filepath.Base(path))]), ""
}

// checkJSON validates one JSON document. With comments set, // and /* */
// comments and trailing commas are blanked out first — to spaces, so every
// byte keeps its offset and the error lands where it is in the file.
func checkJSON(src []byte, comments bool) []finding {
	doc := bytes.Clone(src)
	// A byte-order mark is not JSON, but editors write one and readers skip it.
	if bytes.HasPrefix(doc, []byte("\xef\xbb\xbf")) {
		copy(doc, "   ")
	}
	if comments {
		blankJSONComments(doc)
	}
	if len(bytes.TrimSpace(doc)) == 0 {
		return []finding{{start: 0, end: 0, message: "empty file: a JSON document needs a value"}}
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return []finding{jsonFinding(err, 0, len(doc))}
	}
	return nil
}

// checkJSONLines validates a file of one JSON value per line.
func checkJSONLines(_ string, src []byte) ([]finding, string) {
	var out []finding
	start := 0
	for start <= len(src) && len(out) < MaxDiagnostics {
		end := bytes.IndexByte(src[start:], '\n')
		if end < 0 {
			end = len(src)
		} else {
			end += start
		}
		line := src[start:end]
		if len(bytes.TrimSpace(line)) > 0 {
			var v any
			if err := json.Unmarshal(line, &v); err != nil {
				out = append(out, jsonFinding(err, start, len(line)))
			}
		}
		start = end + 1
	}
	return out, ""
}

// jsonFinding places a decode error. A SyntaxError's Offset counts the bytes
// read before the error, so the offending byte is the one before it — except
// at the end of input, where there is no byte to blame.
func jsonFinding(err error, base, n int) finding {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		off := int(se.Offset)
		if off > 0 && off <= n && !strings.Contains(se.Error(), "end of JSON input") {
			off--
		}
		return finding{start: base + off, end: base + off, message: se.Error()}
	}
	return finding{start: base, end: base, message: err.Error()}
}

// blankJSONComments overwrites comments, and commas that close nothing, with
// spaces, leaving newlines so line numbers survive.
func blankJSONComments(doc []byte) {
	lastSig := -1 // last significant byte outside strings and comments
	for i := 0; i < len(doc); i++ {
		c := doc[i]
		switch {
		case c == '"':
			for i++; i < len(doc) && doc[i] != '"'; i++ {
				if doc[i] == '\\' {
					i++
				}
			}
			lastSig = i
		case c == '/' && at(doc, i+1) == '/':
			for ; i < len(doc) && doc[i] != '\n'; i++ {
				doc[i] = ' '
			}
		case c == '/' && at(doc, i+1) == '*':
			j := i
			for ; j < len(doc) && !(doc[j] == '*' && at(doc, j+1) == '/'); j++ {
				if doc[j] != '\n' {
					doc[j] = ' '
				}
			}
			for k := j; k < j+2 && k < len(doc); k++ {
				doc[k] = ' '
			}
			i = j + 1
		case c == '}' || c == ']':
			if lastSig >= 0 && doc[lastSig] == ',' {
				doc[lastSig] = ' '
			}
			lastSig = i
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		default:
			lastSig = i
		}
	}
}

// yamlLine pulls the line number out of a yaml.v3 error ("yaml: line 3: ...").
var yamlLine = regexp.MustCompile(`^yaml: line (\d+): `)

// yamlParserProblems are the messages yaml.v3's parser (as opposed to its
// scanner) fails with. It numbers those errors from 0, where scanner errors
// count from 1, and it reports the line where the unfinished construct began —
// the unclosed "[" rather than the line where it gave up looking for "]".
var yamlParserProblems = map[string]bool{
	"did not find expected ',' or ']'":       true,
	"did not find expected ',' or '}'":       true,
	"did not find expected '-' indicator":    true,
	"did not find expected <document start>": true,
	"did not find expected <stream-start>":   true,
	"did not find expected key":              true,
	"did not find expected node content":     true,
	"found duplicate %TAG directive":         true,
	"found duplicate %YAML directive":        true,
	"found incompatible YAML document":       true,
	"found undefined tag handle":             true,
}

// checkYAML validates every document in a YAML stream. A file holding
// template syntax is judged only when it parses anyway — Go templates inside a
// block scalar, as in .goreleaser.yaml, are plain YAML text. When it does not,
// the template is the likely reason, and the file is reported unchecked rather
// than broken.
func checkYAML(_ string, src []byte) ([]finding, string) {
	findings := parseYAML(src)
	if len(findings) > 0 && yamlTemplated(src) {
		return nil, "it holds template syntax ({{ }} or {% %}), which is not YAML until it is rendered"
	}
	return findings, ""
}

func parseYAML(src []byte) []finding {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	for {
		// Decoding into a Node parses without resolving tags or types, so
		// CloudFormation's !Ref and friends pass, as they should.
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			msg := err.Error()
			line := 1
			if m := yamlLine.FindStringSubmatch(msg); m != nil {
				msg = msg[len(m[0]):]
				line, _ = strconv.Atoi(m[1])
				if yamlParserProblems[msg] {
					line++
				}
			}
			msg = strings.TrimPrefix(msg, "yaml: ")
			off := 0
			if idx := newLineIndex(src); line >= 1 && line <= len(idx) {
				off = idx[line-1]
			}
			return []finding{{start: off, end: off, message: msg}}
		}
	}
}

// yamlTemplated reports whether a YAML file holds Go-template or Jinja syntax
// — a Helm chart's templates, an Ansible file — which does not parse until it
// is rendered. GitHub Actions' ${{ }} expressions sit inside plain scalars and
// parse fine, so they do not count.
func yamlTemplated(src []byte) bool {
	if bytes.Contains(src, []byte("{%")) {
		return true
	}
	for i := 0; ; {
		j := bytes.Index(src[i:], []byte("{{"))
		if j < 0 {
			return false
		}
		if i+j == 0 || src[i+j-1] != '$' {
			return true
		}
		i += j + 2
	}
}

// checkTOML validates a TOML document, duplicate keys and redefined tables
// included — Cargo and pip reject those as surely as a missing quote.
func checkTOML(_ string, src []byte) ([]finding, string) {
	var v map[string]any
	_, err := toml.Decode(string(src), &v)
	if err == nil {
		return nil, ""
	}
	var pe toml.ParseError
	if errors.As(err, &pe) {
		off := pe.Position.Start
		if off < 0 || off > len(src) {
			off = 0
		}
		return []finding{{start: off, end: off, message: pe.Message}}, ""
	}
	return []finding{{start: 0, end: 0, message: err.Error()}}, ""
}
