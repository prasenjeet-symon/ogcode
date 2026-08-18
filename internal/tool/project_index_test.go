package tool

import (
	"fmt"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

func textEntry(path string, labels ...string) *docindex.PageEntry {
	return &docindex.PageEntry{DocPath: path, PageNum: 1, Labels: labels}
}

// The map is an indented outline, not JSON: folders carry a trailing slash and
// a file's labels stay on the file's own line. Keeping labels inline is where
// roughly half the token saving over MarshalIndent comes from.
func TestProjectIndex_RendersIndentedTree(t *testing.T) {
	tree := buildProjectTree("/proj", []*docindex.PageEntry{
		textEntry("/proj/internal/tool/read.go", "file reading", "line offsets"),
		textEntry("/proj/internal/tool/write.go", "file writing"),
		textEntry("/proj/main.go", "entrypoint"),
	}, nil, nil)

	out := renderProjectMap(tree, "")

	for _, want := range []string{
		"internal/",
		"  tool/",
		"    read.go  file reading, line offsets",
		"    write.go  file writing",
		"main.go  entrypoint",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"{", "}", "\": ["} {
		if strings.Contains(out, unwanted) {
			t.Errorf("output still carries JSON syntax %q:\n%s", unwanted, out)
		}
	}
}

// Labels are the bulk of this output, so text/code files are capped.
func TestProjectIndex_CapsTextFileLabels(t *testing.T) {
	many := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}
	tree := buildProjectTree("/proj", []*docindex.PageEntry{textEntry("/proj/a.go", many...)}, nil, nil)

	out := renderProjectMap(tree, "")

	if !strings.Contains(out, "a.go  one, two, three, four, five") {
		t.Errorf("expected exactly %d labels:\n%s", textLabelCap, out)
	}
	if strings.Contains(out, "six") {
		t.Errorf("label cap not applied:\n%s", out)
	}
}

// A file with no labels is still listed — knowing it exists is the point.
func TestProjectIndex_UnlabeledFileStillListed(t *testing.T) {
	tree := buildProjectTree("/proj", []*docindex.PageEntry{textEntry("/proj/empty.go")}, nil, nil)

	if out := renderProjectMap(tree, ""); !strings.Contains(out, "empty.go") {
		t.Errorf("unlabeled file dropped from the map:\n%s", out)
	}
}

// bigIndex fabricates a project of n files with realistically long labels
// (~30 chars, matching the average measured on this repo's real index).
func bigIndex(n int) []*docindex.PageEntry {
	entries := make([]*docindex.PageEntry, 0, n)
	for i := 0; i < n; i++ {
		labels := make([]string, 8)
		for j := range labels {
			labels[j] = fmt.Sprintf("subsystem behaviour topic %d-%d", i, j)
		}
		entries = append(entries,
			textEntry(fmt.Sprintf("/proj/internal/pkg%02d/file%03d.go", i%20, i), labels...))
	}
	return entries
}

// The map must stay inside MaxToolOutputBytes on its own, or the generic
// backstop cuts it mid-tree and the agent never learns what it lost. This is
// the check that would have caught the 86 KB JSON output overflowing the cap.
func TestProjectIndex_StaysUnderOutputCap(t *testing.T) {
	for _, files := range []int{100, 300, 800, 2000} {
		t.Run(fmt.Sprintf("%dfiles", files), func(t *testing.T) {
			tree := buildProjectTree("/proj", bigIndex(files), nil, nil)
			out := renderProjectMap(tree, "")

			if len(out) > MaxToolOutputBytes {
				t.Errorf("map is %d bytes, over the %d cap — it will be truncated by the backstop",
					len(out), MaxToolOutputBytes)
			}
			// Structure survives at every size; that is what degrades last.
			if !strings.Contains(out, "file000.go") {
				t.Errorf("map lost its file listing at %d files", files)
			}
		})
	}
}

// Past the budget the labels go, not the structure — and the agent is told how
// to get them back rather than being left with a silently shorter tree.
func TestProjectIndex_LargeProjectDropsLabelsWithGuidance(t *testing.T) {
	small := renderProjectMap(buildProjectTree("/proj", bigIndex(50), nil, nil), "")
	large := renderProjectMap(buildProjectTree("/proj", bigIndex(2000), nil, nil), "")

	if !strings.Contains(small, "subsystem behaviour topic 0-0") {
		t.Error("a small project should still show labels")
	}
	if strings.Contains(large, "subsystem behaviour topic 0-0") {
		t.Error("an oversized project should drop labels, not keep them")
	}
	if !strings.Contains(large, "subdir=") {
		t.Errorf("oversized map does not tell the agent how to get labels back:\n%s", large[:300])
	}
}

// A repo-sized project must keep its labels — they are what makes this tool
// more than a file listing, and dropping them is the heaviest loss available.
//
// This pins the budget against being set too conservatively. An earlier 40 KB
// budget silently dropped labels for this repo's own 276-file index, which
// renders to ~44 KB — comfortably inside the 50 KB output cap. Every other test
// still passed, because they only checked the two extremes.
func TestProjectIndex_RepoSizedProjectKeepsLabels(t *testing.T) {
	out := renderProjectMap(buildProjectTree("/proj", bigIndex(280), nil, nil), "")

	if strings.Contains(out, "too large to show topic labels") {
		t.Errorf("a 280-file project dropped its labels at %d bytes, well inside the %d cap",
			len(out), MaxToolOutputBytes)
	}
	if !strings.Contains(out, "subsystem behaviour topic 0-0") {
		t.Error("labels missing from a repo-sized map")
	}
}
