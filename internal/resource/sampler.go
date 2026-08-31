// Package resource samples this process's own resource usage — CPU, resident
// memory, Go runtime memory — so the UI can tell the user what ogcode actually
// costs on their machine.
//
// The question is worth answering here because ogcode is not a thin CLI: it is
// a long-lived local daemon that loads an ONNX embedding model for memory
// indexing, links CGO parsers (MuPDF, tree-sitter), and spawns tool
// subprocesses. Most of that never shows up in Go's own heap numbers, and none
// of it is visible to the user without hunting through Activity Monitor.
//
// Sampling runs only while at least one client is watching. Measuring CPU has a
// cost of its own, and there is no point paying it with no UI open.
package resource

import (
	"context"
	"os"
	"runtime"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// Sample is one measurement of the running process.
type Sample struct {
	At int64 `json:"at"` // unix milliseconds

	// RSS is the resident set size in bytes: what the OS actually holds in
	// physical memory for this process. It is the figure that matches Activity
	// Monitor and top, and it covers the CGO allocations (ONNX weights, MuPDF,
	// tree-sitter) that never appear in the Go numbers below.
	RSS uint64 `json:"rss"`

	// HeapInUse is the bytes of live heap objects; GoTotal is all memory the Go
	// runtime currently holds from the OS (heap, stacks, metadata) minus what it
	// has released back. Go returns memory lazily, so RSS routinely sits well
	// above HeapInUse after a spike — that divergence is normal rather than a
	// leak, which is why both are reported instead of one.
	HeapInUse uint64 `json:"heapInUse"`
	GoTotal   uint64 `json:"goTotal"`

	// CPUPercent is top-style: 100 means one core fully saturated, so on a
	// multi-core machine it can legitimately exceed 100.
	CPUPercent float64 `json:"cpuPercent"`

	Goroutines int `json:"goroutines"`
}

// Activity names what the process is currently busy with. A spike in the graph
// with no explanation is worse than no graph at all — the user is left to guess
// whether their machine is being eaten by a bug — so long-running background
// work labels itself here and the UI can say what is going on.
type Activity struct {
	Label string `json:"label"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// Snapshot is the retained history plus the context needed to read it.
type Snapshot struct {
	Interval int       `json:"interval"` // milliseconds between samples
	Cores    int       `json:"cores"`
	Uptime   int64     `json:"uptime"` // milliseconds since process start
	Activity *Activity `json:"activity,omitempty"`
	Samples  []Sample  `json:"samples"`
}

const (
	metricHeapObjects = "/memory/classes/heap/objects:bytes"
	metricTotal       = "/memory/classes/total:bytes"
	metricReleased    = "/memory/classes/heap/released:bytes"
	metricGoroutines  = "/sched/goroutines:goroutines"
)

// Sampler collects a rolling window of Samples on a fixed interval.
type Sampler struct {
	interval time.Duration
	retain   int
	started  time.Time

	// proc and metricBuf are touched only by the Run goroutine. gopsutil's
	// Process carries the CPU baseline between calls and is not safe for
	// concurrent use, and metrics.Read writes through its argument.
	proc      *process.Process
	metricBuf []metrics.Sample
	// primed reports whether proc holds a CPU baseline from the previous tick.
	// It is cleared whenever sampling pauses, because a reading taken across an
	// idle gap averages that whole gap into one bucket and renders as a spike
	// that never happened.
	primed bool

	mu      sync.Mutex
	samples []Sample

	watchers atomic.Int64
	activity atomic.Pointer[Activity]
}

// NewSampler returns a sampler retaining `retain` samples taken `interval`
// apart. It never fails: if the OS process handle is unavailable, the Go
// runtime numbers are still collected and RSS/CPU report zero.
func NewSampler(interval time.Duration, retain int) *Sampler {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if retain <= 0 {
		retain = 120
	}
	s := &Sampler{
		interval: interval,
		retain:   retain,
		started:  time.Now(),
		metricBuf: []metrics.Sample{
			{Name: metricHeapObjects},
			{Name: metricTotal},
			{Name: metricReleased},
			{Name: metricGoroutines},
		},
	}
	if p, err := process.NewProcess(int32(os.Getpid())); err == nil {
		s.proc = p
		if createMS, err := p.CreateTime(); err == nil && createMS > 0 {
			s.started = time.UnixMilli(createMS)
		}
	}
	return s
}

// Interval is the gap between samples, so clients can size their own tick.
func (s *Sampler) Interval() time.Duration { return s.interval }

// AddWatcher and RemoveWatcher bracket a client that wants live samples.
// Sampling is suspended while the count is zero.
func (s *Sampler) AddWatcher()    { s.watchers.Add(1) }
func (s *Sampler) RemoveWatcher() { s.watchers.Add(-1) }

// SetActivity labels what the process is busy with; ClearActivity removes the
// label once the work is done. Both are safe to call from any goroutine.
func (s *Sampler) SetActivity(a Activity) { s.activity.Store(&a) }
func (s *Sampler) ClearActivity()         { s.activity.Store(nil) }

// Run samples until ctx is cancelled.
func (s *Sampler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.watchers.Load() <= 0 {
				s.primed = false
				continue
			}
			if !s.primed {
				// Establish the CPU baseline and throw the reading away: the
				// first Percent call after a pause covers everything since the
				// last one, which is not this tick.
				s.prime()
				continue
			}
			s.append(s.collect())
		}
	}
}

func (s *Sampler) prime() {
	if s.proc != nil {
		_, _ = s.proc.Percent(0)
	}
	s.primed = true
}

func (s *Sampler) collect() Sample {
	sample := Sample{At: time.Now().UnixMilli()}

	// runtime/metrics rather than ReadMemStats: the same numbers without the
	// stop-the-world pause, which matters for something that ticks forever.
	metrics.Read(s.metricBuf)
	var total, released uint64
	for _, m := range s.metricBuf {
		if m.Value.Kind() != metrics.KindUint64 {
			continue
		}
		switch m.Name {
		case metricHeapObjects:
			sample.HeapInUse = m.Value.Uint64()
		case metricTotal:
			total = m.Value.Uint64()
		case metricReleased:
			released = m.Value.Uint64()
		case metricGoroutines:
			sample.Goroutines = int(m.Value.Uint64())
		}
	}
	if total > released {
		sample.GoTotal = total - released
	}

	if s.proc != nil {
		if mem, err := s.proc.MemoryInfo(); err == nil && mem != nil {
			sample.RSS = mem.RSS
		}
		if pct, err := s.proc.Percent(0); err == nil {
			sample.CPUPercent = pct
		}
	}
	return sample
}

func (s *Sampler) append(sample Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, sample)
	if len(s.samples) > s.retain {
		s.samples = append(s.samples[:0], s.samples[len(s.samples)-s.retain:]...)
	}
	s.pruneLocked(sample.At)
}

// pruneLocked drops samples older than the retention window. Count-based
// trimming alone is not enough: after a pause with no watchers the buffer still
// holds samples from before the gap, and a sparkline plotted by index would
// splice across it as if the time were continuous.
func (s *Sampler) pruneLocked(now int64) {
	cutoff := now - s.interval.Milliseconds()*int64(s.retain)
	drop := 0
	for drop < len(s.samples) && s.samples[drop].At < cutoff {
		drop++
	}
	if drop > 0 {
		s.samples = append(s.samples[:0], s.samples[drop:]...)
	}
}

// Meta returns the reading context — cadence, core count, uptime — without
// copying the sample window, for callers that only need to frame a single
// sample.
func (s *Sampler) Meta() Snapshot {
	return Snapshot{
		Interval: int(s.interval.Milliseconds()),
		Cores:    runtime.NumCPU(),
		Uptime:   time.Since(s.started).Milliseconds(),
		Activity: s.activity.Load(),
	}
}

// Snapshot returns Meta plus a copy of the retained window.
func (s *Sampler) Snapshot() Snapshot {
	out := s.Meta()

	s.mu.Lock()
	s.pruneLocked(time.Now().UnixMilli())
	out.Samples = make([]Sample, len(s.samples))
	copy(out.Samples, s.samples)
	s.mu.Unlock()

	return out
}

// Latest returns the most recent sample, if any has been taken.
func (s *Sampler) Latest() (Sample, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.samples) == 0 {
		return Sample{}, false
	}
	return s.samples[len(s.samples)-1], true
}
