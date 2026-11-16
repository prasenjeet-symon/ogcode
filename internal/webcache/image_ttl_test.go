package webcache

import (
	"testing"
	"time"
)

// TestImageKindDefaultTTL pins the image kind's seven-day default: extracted
// text is worth keeping for a long session (so the same picture is never
// re-sent), but far less than a page's freshness matters, because the image
// itself usually outlives the session anyway.
func TestImageKindDefaultTTL(t *testing.T) {
	if got := defaultTTL(KindImage); got != DefaultImageTTL {
		t.Fatalf("defaultTTL(KindImage) = %v, want %v", got, DefaultImageTTL)
	}

	database := openTestDB(t)
	start := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	clock := newTestClock(start)

	first := openTestCache(t, database, clock, Options{})
	key := Key(KindImage, "image-content-hash")
	if err := first.Put(KindImage, key, []byte("extracted text")); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Just inside the seven-day window a restarted cache still serves it.
	clock.Advance(DefaultImageTTL - time.Minute)
	second := openTestCache(t, database, clock, Options{})
	if body, ok := second.Get(KindImage, key); !ok || string(body) != "extracted text" {
		t.Fatalf("after restart inside the TTL: body = %q, ok = %v; want the stored payload", body, ok)
	}

	// Past it the entry is gone, like every other kind.
	clock.Advance(2 * time.Minute)
	third := openTestCache(t, database, clock, Options{})
	if _, ok := third.Get(KindImage, key); ok {
		t.Fatal("expired image entry was served")
	}
}
