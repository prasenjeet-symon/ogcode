package filechange

import (
	"strings"
	"testing"
)

func TestDiffLinesShowsOneHunk(t *testing.T) {
	oldText := "a\nb\nc\nd\ne\n"
	newText := "a\nb\nc\nD\ne\n"
	got, truncated := DiffLines(oldText, newText)
	if truncated {
		t.Fatal("small diff reported truncated")
	}
	want := "@@ -1,5 +1,5 @@\n a\n b\n c\n- d\n+ D\n e\n"
	if got != want {
		t.Fatalf("diff =\n%q\nwant\n%q", got, want)
	}
}

func TestDiffLinesOfIdenticalTextIsEmpty(t *testing.T) {
	if got, truncated := DiffLines("a\nb\n", "a\nb\n"); got != "" || truncated {
		t.Fatalf("identical text diffed: %q (truncated %v)", got, truncated)
	}
}

func TestDiffLinesOfNewFileHasNoRemoval(t *testing.T) {
	got, _ := DiffLines("", "one\ntwo\n")
	want := "@@ -1,0 +1,2 @@\n+ one\n+ two\n"
	if got != want {
		t.Fatalf("diff =\n%q\nwant\n%q", got, want)
	}
}

func TestDiffLinesTruncatesLargeChange(t *testing.T) {
	var b strings.Builder
	for i := 0; i < diffMaxChangedLines*2; i++ {
		b.WriteString("line\n")
	}
	got, truncated := DiffLines("", b.String())
	if !truncated {
		t.Fatal("large diff not reported truncated")
	}
	if lines := strings.Count(got, "\n+ "); lines != diffMaxChangedLines {
		t.Fatalf("showed %d changed lines, want %d", lines, diffMaxChangedLines)
	}
	if !strings.Contains(got, "diff truncated") {
		t.Errorf("truncated diff has no marker:\n%s", got)
	}
}
