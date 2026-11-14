package bus

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestPublishAssignsMonotonicSeq(t *testing.T) {
	b := New(8)
	ch := b.SubscribeAll()
	b.Publish("a", nil)
	b.Publish("b", nil)
	e1 := <-ch
	e2 := <-ch
	if e1.Seq <= 0 {
		t.Fatalf("first seq should be positive, got %d", e1.Seq)
	}
	if e2.Seq != e1.Seq+1 {
		t.Errorf("seqs not monotonic: %d then %d", e1.Seq, e2.Seq)
	}
}

// TestSeqGapVisibleAfterDrop verifies a dropped event (full buffer) leaves a
// detectable gap in the seq stream, and is counted.
func TestSeqGapVisibleAfterDrop(t *testing.T) {
	b := New(2) // tiny buffer
	ch := b.SubscribeAll()

	b.Publish("a", nil) // seq 1 → buffered
	b.Publish("b", nil) // seq 2 → buffered (now full)
	b.Publish("c", nil) // seq 3 → DROPPED

	if got := (<-ch).Seq; got != 1 {
		t.Fatalf("first received seq = %d, want 1", got)
	}
	if got := (<-ch).Seq; got != 2 {
		t.Fatalf("second received seq = %d, want 2", got)
	}

	b.Publish("d", nil) // seq 4 → buffered
	got := (<-ch).Seq
	if got != 4 {
		t.Errorf("received seq = %d, want 4 (seq 3 was dropped, leaving a gap)", got)
	}
	if b.Dropped() != 1 {
		t.Errorf("Dropped() = %d, want 1", b.Dropped())
	}
}

func TestMultipleSubscribersEachGetSeq(t *testing.T) {
	b := New(8)
	a := b.SubscribeAll()
	c := b.SubscribeAll()
	b.Publish("x", nil)
	ea := <-a
	ec := <-c
	if ea.Seq != ec.Seq {
		t.Errorf("subscribers saw different seqs for the same event: %d vs %d", ea.Seq, ec.Seq)
	}
}

// TestDropWarnIsRateLimited captures slog output and verifies a dropped event
// warns once with the cumulative count, and that a second drop immediately
// after stays quiet (the warning rate-limits to one per second bus-wide).
func TestDropWarnIsRateLimited(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	b := New(2) // tiny buffer: two events fill it
	b.SubscribeAll()

	b.Publish("note.created", nil)    // seq 1 → buffered
	b.Publish("note.created", nil)    // seq 2 → buffered (now full)
	b.Publish("session.updated", nil) // seq 3 → dropped: the warn fires
	b.Publish("session.updated", nil) // seq 4 → dropped, but rate-limited

	out := buf.String()
	if n := strings.Count(out, "sse event dropped: subscriber buffer full"); n != 1 {
		t.Fatalf("drop warn lines = %d, want 1\nlog:\n%s", n, out)
	}
	if !strings.Contains(out, "dropped=1") {
		t.Errorf("cumulative drop count missing from warn\nlog:\n%s", out)
	}
}
