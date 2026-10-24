package agent

import (
	"testing"
)

// TestLoopBoardNoticesDeliveredMarkTheReport pins the mark-on-drain order the
// server's board push now relies on: a report tracked into a running turn stays
// pending until the step that drains its notice concludes it — the board row is
// stamped when the model is about to read the report, never at the push, and
// only once for the drained case.
func TestLoopBoardNoticesDeliveredMarkTheReport(t *testing.T) {
	lc := NewLoopControl()
	lr := &LoopRunner{}
	marked := 0
	lr.TrackBoardReport(lc, "s1", BoardReport{
		Text: "[Teammate t1] done",
		Mark: func() error { marked++; return nil },
	})

	// No drain yet: the push itself says nothing about the row.
	lr.BoardNoticesDelivered(lc, "s1", nil)
	if marked != 0 {
		t.Fatalf("an empty drain marked the report %d times", marked)
	}

	// The step that drains the notice into its request concludes the report.
	lc.PushNotice(Notice{Text: "[Teammate t1] done", Cause: "teammate"})
	batch := lc.DrainNotices()
	if len(batch) != 1 || batch[0].Text != "[Teammate t1] done" {
		t.Fatalf("drain = %+v, want the one report notice", batch)
	}
	lr.BoardNoticesDelivered(lc, "s1", batch)
	if marked != 1 {
		t.Fatalf("the draining step marked the report %d times, want 1", marked)
	}

	// At-most-once: the drained report was concluded; nothing runs again.
	lr.BoardNoticesDelivered(lc, "s1", batch)
	if marked != 1 {
		t.Fatalf("the marked report was concluded %d times, want 1", marked)
	}
}

// TestLoopBoardReportsDieWithTheirTurn pins the scoping the re-delivery needs:
// a superseded turn's queued report is not concluded by the next turn's drain,
// and a turn start trims it, so its row stays unreported for catch-up instead
// of being stamped by a drain that no longer exists.
func TestLoopBoardReportsDieWithTheirTurn(t *testing.T) {
	dead := NewLoopControl()
	live := NewLoopControl()
	lr := &LoopRunner{}
	marked := 0
	lr.TrackBoardReport(dead, "s1", BoardReport{
		Text: "[Teammate t1] done",
		Mark: func() error { marked++; return nil },
	})

	lr.BoardNoticesDelivered(live, "s1", []Notice{{Text: "[Teammate t1] done", Cause: "teammate"}})
	if marked != 0 {
		t.Fatal("the next turn's drain concluded a report pushed into the dead turn")
	}

	// A turn start trims the dead turn's reports; an out-of-turn drain after
	// that finds nothing.
	lr.TrimBoardReports("s1", live)
	lr.BoardNoticesDelivered(dead, "s1", []Notice{{Text: "[Teammate t1] done", Cause: "teammate"}})
	if marked != 0 {
		t.Fatal("a report trimmed with its turn was still concluded")
	}
}

// TestLoopBoardReportsNilSafe covers the shapes a server holds: a nil runner
// (no loop wired), a runner with no tracker yet, and reports missing their
// control, text or mark must all be quiet.
func TestLoopBoardReportsNilSafe(t *testing.T) {
	marked := 0
	mark := func() error { marked++; return nil }

	var absent *LoopRunner
	absent.TrackBoardReport(NewLoopControl(), "s1", BoardReport{Text: "x", Mark: mark})
	absent.BoardNoticesDelivered(NewLoopControl(), "s1", []Notice{{Text: "x"}})
	absent.TrimBoardReports("s1", NewLoopControl())

	lr := &LoopRunner{}
	lc := NewLoopControl()
	lr.TrackBoardReport(nil, "s1", BoardReport{Text: "x", Mark: mark})
	lr.TrackBoardReport(lc, "s1", BoardReport{Text: "", Mark: mark})
	lr.TrackBoardReport(lc, "s1", BoardReport{Text: "x"})
	// A drain for a session nothing was tracked under, and an empty batch.
	lr.BoardNoticesDelivered(lc, "s2", []Notice{{Text: "x"}})
	lr.BoardNoticesDelivered(lc, "s2", nil)
	if marked != 0 {
		t.Fatalf("a report missing its control, text or mark concluded %d times", marked)
	}
}
