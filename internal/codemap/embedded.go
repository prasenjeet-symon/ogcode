package codemap

import (
	"bytes"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// embeddedBlock is a script or style body inside an HTML file: a byte range
// the HTML grammar keeps as opaque text, and the language to check it as.
type embeddedBlock struct {
	start, end int
	kind       string // "js", "json" or "css"
}

// embeddedBlocks finds the script and style bodies worth checking.
//
// A script is checked as JavaScript when its type says it is one — none, a
// JavaScript MIME type, "module", or "text/babel" for JSX — and as JSON for
// the data types (JSON-LD, import maps); any other type is a template or data
// the page reads itself, and is left alone. A style is checked as CSS unless it
// names another type.
func embeddedBlocks(root *ts.Node, src []byte) []embeddedBlock {
	var out []embeddedBlock
	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		kind := n.Kind()
		if kind == "script_element" || kind == "style_element" {
			var typ string
			var body *ts.Node
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				switch c.Kind() {
				case "start_tag":
					typ = attrValue(c, src, "type")
				case "raw_text":
					body = c
				}
			}
			if body != nil {
				if k := embeddedKind(kind, typ); k != "" {
					out = append(out, embeddedBlock{start: int(body.StartByte()), end: int(body.EndByte()), kind: k})
				}
			}
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return out
}

func embeddedKind(element, typ string) string {
	typ = strings.ToLower(strings.TrimSpace(typ))
	if i := strings.IndexByte(typ, ';'); i >= 0 { // "text/javascript; charset=utf-8"
		typ = strings.TrimSpace(typ[:i])
	}
	if element == "style_element" {
		if typ == "" || typ == "text/css" {
			return "css"
		}
		return ""
	}
	switch typ {
	case "", "module", "text/javascript", "application/javascript", "text/ecmascript",
		"application/ecmascript", "text/jsx", "text/babel":
		return "js"
	case "application/json", "application/ld+json", "importmap", "speculationrules", "application/manifest+json":
		return "json"
	}
	return ""
}

// attrValue returns the value of the named attribute on a start tag.
func attrValue(tag *ts.Node, src []byte, name string) string {
	for i := uint(0); i < tag.ChildCount(); i++ {
		attr := tag.Child(i)
		if attr.Kind() != "attribute" {
			continue
		}
		var key, val string
		for j := uint(0); j < attr.ChildCount(); j++ {
			c := attr.Child(j)
			switch c.Kind() {
			case "attribute_name":
				key = c.Utf8Text(src)
			case "attribute_value":
				val = c.Utf8Text(src)
			case "quoted_attribute_value":
				val = strings.Trim(c.Utf8Text(src), `"'`)
			}
		}
		if strings.EqualFold(key, name) {
			return val
		}
	}
	return ""
}

// templateMarkers are the delimiters of the server-side templates an .html
// file is often really written in — Jinja, Django, Handlebars, ERB, EJS, PHP.
// A script or style holding one is not code until it is rendered.
var templateMarkers = [][]byte{[]byte("{{"), []byte("{%"), []byte("<%"), []byte("<?")}

// checkEmbedded checks each block as its own language and returns the
// problems at their offsets in the whole file.
func checkEmbedded(src []byte, blocks []embeddedBlock) ([]finding, error) {
	var out []finding
	for _, b := range blocks {
		body := src[b.start:b.end]
		if len(bytes.TrimSpace(body)) == 0 || containsAny(body, templateMarkers) {
			continue
		}
		var fs []finding
		switch b.kind {
		case "json":
			fs = checkJSON(body, false)
		default:
			// JavaScript goes through the TSX grammar, as .js files do.
			ext := ".tsx"
			if b.kind == "css" {
				ext = ".css"
			}
			var err error
			if fs, err = parseFindings(lookup(ext), body, nil); err != nil {
				return nil, err
			}
		}
		for _, f := range fs {
			f.start += b.start
			f.end += b.start
			out = append(out, f)
		}
	}
	return out, nil
}

func containsAny(b []byte, subs [][]byte) bool {
	for _, s := range subs {
		if bytes.Contains(b, s) {
			return true
		}
	}
	return false
}
