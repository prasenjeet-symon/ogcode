package resource

import (
	"context"
	"testing"
	"time"
)

func TestSamplerRetainsOnlyTheWindow(t *testing.T) {
	s := NewSampler(time.Second, 3)
	now := time.Now().UnixMilli()
	for i := 0; i < 6; i++ {
		s.append(Sample{At: now + int64(i*1000), Goroutines: i})
	}

	got := s.Snapshot().Samples
	if len(got) != 3 {
		t.Fatalf("retained %d samples, want 3", len(got))
	}
	if got[len(got)-1].Goroutines != 5 {
		t.Errorf("newest sample is %d, want 5", got[len(got)-1].Goroutines)
	}
	if got[0].Goroutines != 3 {
		t.Errorf("oldest retained sample is %d, want 3", got[0].Goroutines)
	}
}

// After a pause with no watchers the buffer still holds pre-gap samples. They
// must be dropped: a sparkline plotted by index would otherwise splice across
// the gap and show a jump that never happened.
func TestSamplerDropsSamplesFromBeforeAGap(t *testing.T) {
	s := NewSampler(time.Second, 10) // 10s window
	now := time.Now().UnixMilli()

	s.append(Sample{At: now - 60_000, Goroutines: 1}) // a minute old
	s.append(Sample{At: now, Goroutines: 2})

	got := s.Snapshot().Samples
	if len(got) != 1 {
		t.Fatalf("retained %d samples, want 1", len(got))
	}
	if got[0].Goroutines != 2 {
		t.Errorf("kept the stale sample (%d), want the fresh one (2)", got[0].Goroutines)
	}
}

func TestSamplerIdlesUntilWatched(t *testing.T) {
	s := NewSampler(10*time.Millisecond, 100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	time.Sleep(80 * time.Millisecond)
	if n := len(s.Snapshot().Samples); n != 0 {
		t.Fatalf("sampled %d times with nobody watching, want 0", n)
	}

	s.AddWatcher()
	defer s.RemoveWatcher()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := s.Latest(); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no sample recorded after a watcher subscribed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The first CPU reading after a pause covers everything since the previous
// call, so it must not be recorded as if it belonged to one tick.
func TestSamplerDiscardsThePrimingTick(t *testing.T) {
	s := NewSampler(time.Hour, 10)
	if s.primed {
		t.Fatal("sampler starts primed, want unprimed")
	}
	s.prime()
	if !s.primed {
		t.Fatal("prime() did not set the baseline flag")
	}
	if n := len(s.Snapshot().Samples); n != 0 {
		t.Fatalf("prime() recorded %d samples, want 0", n)
	}
}
