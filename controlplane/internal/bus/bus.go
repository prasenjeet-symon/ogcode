// Package bus is the master's event fan-out for the operator panel's SSE stream.
//
// It is a deliberate re-implementation of ogcode's internal/bus (which cannot be
// imported across the module boundary). The contract is identical and
// load-bearing: every published event is stamped with a fresh master-global seq,
// delivery to a slow subscriber is lossy (a full buffer drops rather than
// blocking the publisher), and the seq lets a client detect the gap and resync.
//
// Worker-relayed session events are re-published here with FRESH master seqs —
// the worker's original bus numbering is discarded on purpose, because the SSE
// stream mixes master-local frames and relayed frames and exactly one numbering
// must cover both. Resource frames do NOT go through the bus; they ride the SSE
// stream as control frames (seq 0) so they never consume a seq.
package bus

import (
	"encoding/json"
	"sync"
	"sync/atomic"
)

// Event is one published, seq-stamped frame.
type Event struct {
	Type string `json:"type"`
	// Seq is a monotonic, bus-global sequence number stamped on every published
	// event. Delivery is lossy by design, but the seq makes drops DETECTABLE: a
	// client that sees the sequence jump knows it missed events and can resync.
	// Control frames sent directly by the SSE handler do not go through Publish
	// and carry no seq (0), so clients must ignore seq 0 for gap detection.
	Seq        int64           `json:"seq"`
	Properties json.RawMessage `json:"properties"`
}

// Bus is a lossy, seq-stamping publish/subscribe fan-out.
type Bus struct {
	mu      sync.RWMutex
	subs    []chan Event
	closed  bool
	bufSize int
	seq     atomic.Int64
	dropped atomic.Int64
}

// New returns a Bus whose subscriber channels buffer bufSize events (default
// 1024 when non-positive).
func New(bufSize int) *Bus {
	if bufSize <= 0 {
		bufSize = 1024
	}
	return &Bus{bufSize: bufSize}
}

// Publish marshals properties, stamps a fresh global seq, and fans the event out
// to every subscriber without blocking on a full buffer.
func (b *Bus) Publish(eventType string, properties any) {
	data, err := json.Marshal(properties)
	if err != nil {
		return
	}
	b.publishRaw(eventType, data)
}

// PublishRaw is Publish for an already-marshaled JSON payload (used by the stream
// relay, which forwards the worker's verbatim event properties). It still stamps
// a fresh master seq.
func (b *Bus) PublishRaw(eventType string, properties json.RawMessage) {
	b.publishRaw(eventType, properties)
}

func (b *Bus) publishRaw(eventType string, data json.RawMessage) {
	evt := Event{Type: eventType, Seq: b.seq.Add(1), Properties: data}
	b.mu.RLock()
	for _, ch := range b.subs {
		select {
		case ch <- evt:
		default:
			b.dropped.Add(1)
		}
	}
	b.mu.RUnlock()
}

// Dropped returns the cumulative number of events dropped to full buffers.
func (b *Bus) Dropped() int64 { return b.dropped.Load() }

// SubscribeAll registers a new subscriber and returns its receive channel.
func (b *Bus) SubscribeAll() <-chan Event {
	ch := make(chan Event, b.bufSize)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch
	}
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes and closes a previously-registered subscriber channel.
func (b *Bus) Unsubscribe(ch <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, s := range b.subs {
		if s == ch {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			close(s)
			return
		}
	}
}

// Close closes all subscriber channels and rejects further subscriptions.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, s := range b.subs {
		close(s)
	}
	b.subs = nil
}
