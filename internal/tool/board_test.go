package tool

import (
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/board"
)

// TestTreeTextDrawsTheNewestRowsFirst pins the render order the tool depends on.
// A roster arrives in join order — oldest first — and the output is capped by
// keeping its head, so a cut has to land on the oldest rows: the handles a
// spawner was just given, and the subtrees they sit in, must be near the top.
func TestTreeTextDrawsTheNewestRowsFirst(t *testing.T) {
	roster := []board.Handle{
		{Handle: "old-root", Status: board.StatusDone},
		{Handle: "old-child", SpawnedBy: "old-root", Status: board.StatusDone},
		{Handle: "new-root", Status: board.StatusRunning},
		{Handle: "new-child", SpawnedBy: "new-root", Status: board.StatusRunning},
	}
	want := "- new-root  running  role=  session=\n" +
		"  - new-child  running  role=  session=\n" +
		"- old-root  done  role=  session=\n" +
		"  - old-child  done  role=  session=\n"
	if got := treeText(roster); got != want {
		t.Fatalf("treeText =\n%q\nwant\n%q", got, want)
	}
}
