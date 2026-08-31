package memory

import (
	"context"
	"testing"
	"time"
)

func TestBackfillRestScalesWithWorkAndIsCapped(t *testing.T) {
	if got, want := backfillRest(100*time.Millisecond), 300*time.Millisecond; got != want {
		t.Errorf("rest after a 100ms embed = %v, want %v (%dx the work)", got, want, backfillRestRatio)
	}
	if got := backfillRest(10 * time.Second); got != backfillMaxRest {
		t.Errorf("rest after a 10s embed = %v, want it capped at %v", got, backfillMaxRest)
	}
	if got := backfillRest(0); got != 0 {
		t.Errorf("rest after no measurable work = %v, want 0", got)
	}
}

// Shutdown must not have to wait out a pause.
func TestRestAfterEmbedAbandonsThePauseOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	// A rest this long would be capped to backfillMaxRest, which is still far
	// longer than a cancelled context should take to return.
	if restAfterEmbed(ctx, time.Hour) {
		t.Error("restAfterEmbed said to continue after cancellation")
	}
	if elapsed := time.Since(start); elapsed > backfillMaxRest/2 {
		t.Errorf("waited %v before noticing cancellation, want it to return immediately", elapsed)
	}
}

func TestBackfillReportsProgress(t *testing.T) {
	g := newTestGraph(t)
	for _, key := range []string{"bare1", "bare2", "bare3"} {
		if _, err := g.Store.AddNode(Node{
			SessionID: "s1", ProjectID: "/proj", SessionType: "build", Type: TypeFact,
			Key: key, Content: "MATCH never embedded", Question: "q", TopicName: "Auth",
		}); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	m := &Memory{Store: g.Store, Graph: g, enabled: true}

	var done, total []int
	embedded, failed, err := m.BackfillEmbeddings(context.Background(), func(d, tt int) {
		done = append(done, d)
		total = append(total, tt)
	})
	if err != nil {
		t.Fatalf("BackfillEmbeddings: %v", err)
	}
	if embedded != 3 || failed != 0 {
		t.Fatalf("embedded %d (%d failed), want 3 and 0", embedded, failed)
	}

	if want := []int{1, 2, 3}; len(done) != 3 || done[0] != want[0] || done[1] != want[1] || done[2] != want[2] {
		t.Errorf("progress reported done=%v, want %v", done, want)
	}
	for _, tt := range total {
		if tt != 3 {
			t.Errorf("progress reported total=%d, want 3 for every call (got %v)", tt, total)
			break
		}
	}
}
