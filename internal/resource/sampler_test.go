package resource

import (
	"context"
	"runtime"
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

// A turn is summarised from the samples taken while it ran: peak and last RSS,
// mean and peak CPU, goroutines, and how much of the turn was measured. Samples
// from before the turn belong to whatever ran previously and must not count.
func TestEndTurnSummarisesTheTurnWindow(t *testing.T) {
	s := NewSampler(time.Second, 100)
	start := time.Now()
	base := start.UnixMilli()

	s.append(Sample{At: base - 5_000, RSS: 100 << 20, CPUPercent: 900, Goroutines: 200}) // before the turn
	s.append(Sample{At: base + 1_000, RSS: 300 << 20, HeapInUse: 50 << 20, CPUPercent: 120, Goroutines: 100})
	s.append(Sample{At: base + 2_000, RSS: 250 << 20, HeapInUse: 45 << 20, CPUPercent: 60, Goroutines: 40})
	s.append(Sample{At: base + 3_000, RSS: 200 << 20, HeapInUse: 40 << 20, CPUPercent: 120, Goroutines: 60})

	got := s.EndTurn(start)

	if got.Samples != 3 {
		t.Errorf("samples = %d, want the 3 taken during the turn", got.Samples)
	}
	if got.RSSMB != 200 || got.PeakRSSMB != 300 {
		t.Errorf("rss = %.1f MB, peak = %.1f MB; want 200 and 300", got.RSSMB, got.PeakRSSMB)
	}
	if got.GoHeapMB != 40 {
		t.Errorf("go heap = %.1f MB, want 40", got.GoHeapMB)
	}
	if got.CPUAvgPercent != 100 || got.CPUMaxPercent != 120 {
		t.Errorf("cpu = %.1f%%, peak = %.1f%%; want 100 and 120", got.CPUAvgPercent, got.CPUMaxPercent)
	}
	if got.Goroutines != 60 || got.PeakGoroutines != 100 {
		t.Errorf("goroutines = %d, peak = %d; want 60 and 100", got.Goroutines, got.PeakGoroutines)
	}
	if got.CoveredMS != 3_000 {
		t.Errorf("covered = %d ms, want 3000", got.CoveredMS)
	}
	if got.Cores != runtime.NumCPU() {
		t.Errorf("cores = %d, want %d", got.Cores, runtime.NumCPU())
	}
	if got.DurationMS < 0 {
		t.Errorf("duration = %d ms, want a non-negative figure", got.DurationMS)
	}
}

// A watcher is what keeps the sampler collecting, so a turn that reports must
// hold one for its whole length and release it when it ends.
func TestBeginTurnHoldsAWatcherUntilEndTurn(t *testing.T) {
	s := NewSampler(time.Second, 10)

	start := s.BeginTurn()
	if n := s.watchers.Load(); n != 1 {
		t.Fatalf("watchers = %d during a turn, want 1", n)
	}

	got := s.EndTurn(start)
	if got.Samples != 0 {
		t.Errorf("a turn with no samples reported %d, want 0", got.Samples)
	}
	if n := s.watchers.Load(); n != 0 {
		t.Errorf("watchers = %d after the turn, want 0", n)
	}
}
