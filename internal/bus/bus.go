package bus

import (
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type Event struct {
	Type string `json:"type"`
	// Seq is a monotonic, bus-global sequence number stamped on every published
	// event. Delivery to a slow subscriber is still lossy by design (a full
	// buffer drops rather than blocking the hot path), but the seq makes drops
	// DETECTABLE: a client that sees the sequence jump knows it missed events and
	// can resync. Control frames sent directly by the SSE handler
	// (server.connected/config/heartbeat) do not go through Publish and carry no
	// seq (0), so clients must ignore seq 0 for gap detection.
	Seq        int64           `json:"seq"`
	Properties json.RawMessage `json:"properties"`
}

type Bus struct {
	mu      sync.RWMutex
	subs    []chan Event
	closed  bool
	bufSize int
	seq     atomic.Int64
	// lastWarnUnixNano rate-limits the drop warning from warnDropped below:
	// nanos of the last warn, guarded by compare-and-swap.
	lastWarnUnixNano atomic.Int64
	dropped          atomic.Int64 // total events dropped to full subscriber buffers
}

func New(bufSize int) *Bus {
	if bufSize <= 0 {
		bufSize = 1024
	}
	return &Bus{bufSize: bufSize}
}

func (b *Bus) Publish(eventType string, properties any) {
	data, err := json.Marshal(properties)
	if err != nil {
		return
	}
	// Stamp a global seq before fan-out so every subscriber sees the same
	// numbering; a gap in a subscriber's received seqs means events were dropped.
	evt := Event{Type: eventType, Seq: b.seq.Add(1), Properties: data}
	b.mu.RLock()
	for _, ch := range b.subs {
		select {
		case ch <- evt:
		default:
			// Buffer full — drop rather than block the publisher (often the agent
			// loop's hot path). The seq gap lets the client detect and resync.
			total := b.dropped.Add(1)
			b.warnDropped(evt.Type, evt.Seq, total)
		}
	}
	b.mu.RUnlock()
}

// dropWarnInterval rate-limits warnDropped.
const dropWarnInterval = time.Second

// warnDropped reports an event lost to a full subscriber buffer at most once
// per interval across the whole bus: Publish sits on the agent loop's hot
// path, and a full buffer can shed events faster than anyone should hear
// about it. The last-warned timestamp is guarded by a compare-and-swap, so
// concurrent publishers racing on the window cannot each log — a loser
// observes the winner's fresh stamp and stays quiet. eventType is a bounded
// constant, never user content, and the event's properties are not logged.
func (b *Bus) warnDropped(eventType string, seq, total int64) {
	now := time.Now().UnixNano()
	last := b.lastWarnUnixNano.Load()
	if now-last < int64(dropWarnInterval) {
		return
	}
	if !b.lastWarnUnixNano.CompareAndSwap(last, now) {
		return
	}
	slog.Warn("sse event dropped: subscriber buffer full", "type", eventType, "seq", seq, "dropped", total)
}

// Dropped returns the cumulative number of events dropped to full subscriber
// buffers, for observability.
func (b *Bus) Dropped() int64 { return b.dropped.Load() }

func (b *Bus) SubscribeAll() <-chan Event {
	ch := make(chan Event, b.bufSize)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

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
