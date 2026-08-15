package bus

import "testing"

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
