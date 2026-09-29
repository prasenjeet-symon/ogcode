package codemap

import (
	"strings"
	"testing"
)

// The tree-sitter grammar parses every one of these without an error, and
// CPython rejects every one: indentation is where an edit most often breaks a
// Python file, so each has to be reported, on the line CPython would name.
func TestCheckPythonIndentationErrors(t *testing.T) {
	cases := []struct {
		name, src string
		line      int
		want      string
	}{
		{"unexpected indent", "x = 1\n    y = 2\n", 2, "unexpected indent"},
		{"indented first line", "  import os\n", 1, "unexpected indent"},
		{"missing block", "def f():\nreturn 1\n", 2, "expected an indented block after line 1"},
		{"block missing at EOF", "x = 1\nif x:\n", 2, "expected an indented block after line 2"},
		{"block missing, comments only", "for i in r:\n    # todo\n\nprint(i)\n", 4, "expected an indented block after line 1"},
		{"nested block missing", "if a:\n    if b:\n    x = 1\n", 3, "expected an indented block after line 2"},
		{"dedent to no level", "def f():\n    if x:\n        pass\n      y = 1\n", 4, "unindent does not match any outer indentation level"},
		{"dedent out of a loop", "for i in r:\n    a = 1\n  b = 2\n", 3, "unindent does not match any outer indentation level"},
		{"dedent inside a class", "class A:\n    def f(self):\n        return 1\n    x = 2\n  y = 3\n", 5, "unindent does not match any outer indentation level"},
		{"tab then spaces", "if x:\n\tif y:\n        pass\n", 3, "inconsistent use of tabs and spaces"},
		{"block header after a multi-line call", "def f(\n    a,\n    b):\nreturn a\n", 4, "expected an indented block after line 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := CheckSource("a.py", []byte(c.src))
			if err != nil {
				t.Fatal(err)
			}
			if res.OK() {
				t.Fatalf("reported OK; CPython rejects this file")
			}
			d := res.Diagnostics[0]
			if d.Line != c.line || !strings.Contains(d.Message, c.want) {
				t.Errorf("first diagnostic = %d: %q, want line %d: %q", d.Line, d.Message, c.line, c.want)
			}
		})
	}
}

// Valid Python that a naive indentation check would trip on: indentation
// inside brackets, continuations, strings and comments means nothing, and an
// f-string can hold its own quotes, span lines, and pad with a quote character.
func TestCheckPythonIndentationAcceptsValidFiles(t *testing.T) {
	cases := map[string]string{
		"brackets and continuations": `total = (1 +
        2 +
  3)
items = [
            "a",
    "b",
]
if a and \
        b:
    pass
x = 1 + \
  2
`,
		"strings hide indentation": `def f():
    s = """
  not code
        at all
"""
    t = '''
x:'''
    return s + t
`,
		"f-strings, PEP 701": `name = "x"
d = {"k": 1}
a = f"{d["k"]}"
b = f"{name!r:>{10}}"
c = f"{name:'>10}"
e = f"{ {'a': 1}['a'] }"
g = f"""{
    name
}"""
h = rf"\d{name}"
i = t"{name}"
def after():
    return a
`,
		"comments at any indentation": `def f():
        # deep comment
    x = 1
# shallow comment
    return x
`,
		"one-line compound statements": `if x: pass
class A: pass
while False: break
f = lambda: 0
d = {1: 2}
s = x[1:]
y: int = 3
`,
		"blocks and dedents": `class A:
    def f(self):
        if self:
            for i in range(3):
                try:
                    pass
                except ValueError as e:
                    raise
                finally:
                    pass
            else:
                pass
        return 1

    @property
    def g(self):
        match self:
            case {"x": x}:
                return x
            case _:
                return None


async def h():
    async with a as b:
        await b
`,
		"tabs used consistently":   "def f():\n\tif x:\n\t\treturn 1\n\treturn 2\n",
		"crlf line endings":        "def f():\r\n    if x:\r\n        return 1\r\n    return 2\r\n",
		"form feed resets":         "def f():\n    return 1\n\f\nx = 2\n",
		"type parameters":          "def first[T](xs: list[T]) -> T:\n    return xs[0]\n\ntype Pair[T] = tuple[T, T]\n",
		"header ends with comment": "if x:  # why\n    pass\n",
		"empty file":               "",
		"only comments":            "# nothing\n#   here\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := CheckSource("a.py", []byte(src))
			if err != nil {
				t.Fatal(err)
			}
			if !res.OK() {
				t.Errorf("valid file flagged: %+v", res.Diagnostics)
			}
		})
	}
}

// A grammar error comes first, and the indentation it throws off after it is
// the same mistake, so only the grammar's report is kept.
func TestCheckPythonIndentationDoesNotDoubleReport(t *testing.T) {
	res, err := CheckSource("a.py", []byte("def f()\n    return 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Line != 1 {
		t.Errorf("diagnostics = %+v, want only the missing-colon error on line 1", res.Diagnostics)
	}
}
