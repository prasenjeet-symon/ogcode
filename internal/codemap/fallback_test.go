package codemap

import (
	"strings"
	"testing"
)

// The heuristic scanner numbers lines as read does: a file ending in a newline
// has no extra empty line after it, so the last entry ends on the last line.
func TestFallbackLastRangeEndsAtLastLine(t *testing.T) {
	for name, src := range map[string]string{
		"run.sh":   "#!/bin/sh\nfoo() {\n  echo hi\n}\nbar() {\n  echo bye\n}\n",
		"guide.md": "# Guide\n\nintro\n\n## Install\n\nsteps\n",
	} {
		fm, err := Outline(write(t, name, src))
		if err != nil {
			t.Fatal(err)
		}
		last := fm.Symbols[len(fm.Symbols)-1]
		if last.EndLine != fm.TotalLines {
			t.Errorf("%s: last entry ends at %d, the file has %d lines", name, last.EndLine, fm.TotalLines)
		}
	}
}

// A ~~~ fence is a code block like a ``` one, and a closing run of #s must be
// set off by a space — "# Using C#" is about C#.
func TestMarkdownTildeFenceAndClosingHashes(t *testing.T) {
	src := "# Using C#\n\n~~~sh\n# not a heading\n~~~\n\n````\n```\n# still code\n````\n\n## Real ##\n"
	fm, err := Outline(write(t, "notes.md", src))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(fm), "|"); got != "Using C#|Real" {
		t.Errorf("headings = %q, want %q", got, "Using C#|Real")
	}
}

// YAML front matter is not part of the document, and its comments start with #.
func TestMarkdownSkipsFrontMatter(t *testing.T) {
	fm, err := Outline(write(t, "post.md", "---\ntitle: Post\n# a yaml comment\n---\n\n# Title\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(fm), "|"); got != "Title" {
		t.Errorf("headings = %q, want %q", got, "Title")
	}
}

// Kotlin stacks modifiers ahead of `fun` and `class`, and names an extension
// function after its receiver.
func TestFallbackKotlin(t *testing.T) {
	src := "package demo\n\ndata class User(val id: Int)\n\nfun String.slug(): String = lowercase()\n\nclass Repo {\n    override suspend fun load(id: Int): User? = null\n    private fun helper() {}\n}\n\nobject Registry\n"
	fm, err := Outline(write(t, "Repo.kt", src))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"User", "slug", "Repo", "load", "helper", "Registry"} {
		find(t, fm, want)
	}
}
