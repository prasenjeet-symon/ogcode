package bus

import (
	"encoding/json"
	"testing"
)

func TestPublishStampsMonotonicSeq(t *testing.T) {
	b := New(16)
	ch := b.SubscribeAll()
	for i := 0; i < 5; i++ {
		b.Publish("e", map[string]int{"i": i})
	}
	for want := int64(1); want <= 5; want++ {
		ev := <-ch
		if ev.Seq != want {
			t.Fatalf("seq: got %d want %d", ev.Seq, want)
		}
		if ev.Type != "e" {
			t.Fatalf("type: got %q", ev.Type)
		}
	}
}

func TestPublishRawPassesThroughProperties(t *testing.T) {
	b := New(4)
	ch := b.SubscribeAll()
	raw := json.RawMessage(`{"hello":"world"}`)
	b.PublishRaw("relayed", raw)
	ev := <-ch
	if ev.Seq != 1 {
		t.Fatalf("seq: got %d want 1", ev.Seq)
	}
	if string(ev.Properties) != string(raw) {
		t.Fatalf("properties: got %s want %s", ev.Properties, raw)
	}
}

func TestPublishIsLossyAndCountsDrops(t *testing.T) {
	b := New(2) // tiny buffer
	_ = b.SubscribeAll()
	// Never drain; the 3rd+ publishes must drop rather than block.
	for i := 0; i < 5; i++ {
		b.Publish("e", i)
	}
	if got := b.Dropped(); got != 3 {
		t.Fatalf("dropped: got %d want 3", got)
	}
}

func TestUnsubscribeClosesChannel(t *testing.T) {
	b := New(4)
	ch := b.SubscribeAll()
	b.Unsubscribe(ch)
	if _, ok := <-ch; ok {
		t.Fatal("expected closed channel after Unsubscribe")
	}
}

func TestCloseRejectsFurtherSubscriptions(t *testing.T) {
	b := New(4)
	b.Close()
	ch := b.SubscribeAll()
	if _, ok := <-ch; ok {
		t.Fatal("expected closed channel from SubscribeAll after Close")
	}
}
