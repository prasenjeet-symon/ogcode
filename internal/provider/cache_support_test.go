package provider

import (
	"testing"
)

// fakeURLProvider adds a reportable endpoint to the package's fakeProvider,
// so the ollama branch of StaticCacheVerdict can be driven both ways.
type fakeURLProvider struct {
	*fakeProvider
	url string
}

func (f fakeURLProvider) BaseURL() string { return f.url }

func TestStaticCacheVerdict(t *testing.T) {
	tests := []struct {
		name string
		p    Provider
		want CacheVerdict
	}{
		{"nil provider is unknown", nil, CacheUnknown},
		{"anthropic sets breakpoints itself", newFake("anthropic"), CacheSupported},
		{"openai slot could be anything", newFake("openai"), CacheUnknown},
		{"openrouter slot could be anything", newFake("openrouter"), CacheUnknown},
		{
			// The case that matters: a cloud-marked model is forwarded to the
			// hosted backend and billed, even though the request goes to
			// localhost. A URL-based rule reads this as "local, therefore cheap"
			// and withholds compaction from a paid session.
			"localhost ollama still routes cloud models",
			fakeURLProvider{newFake("ollama"), "http://localhost:11434/v1"},
			CacheAbsent,
		},
		{
			"LAN ollama routes them too",
			fakeURLProvider{newFake("ollama"), "http://192.168.1.20:11434/v1"},
			CacheAbsent,
		},
		{
			"ollama cloud is paid and does not cache",
			fakeURLProvider{newFake("ollama"), "https://ollama.com/v1"},
			CacheAbsent,
		},
		{
			"ollama with no reportable URL",
			newFake("ollama"),
			CacheAbsent,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StaticCacheVerdict(tt.p); got != tt.want {
				t.Errorf("StaticCacheVerdict = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCacheObserverDeclaresAbsentAfterQuietSteps(t *testing.T) {
	o := NewCacheObserver(newFake("openai"))
	if got := o.Verdict(); got != CacheUnknown {
		t.Fatalf("fresh observer = %q, want unknown", got)
	}
	for i := 0; i < cacheObservationSteps-1; i++ {
		o.Observe(0, 0, true)
		if got := o.Verdict(); got != CacheUnknown {
			t.Fatalf("after %d quiet steps = %q, want unknown", i+1, got)
		}
	}
	o.Observe(0, 0, true)
	if got := o.Verdict(); got != CacheAbsent {
		t.Errorf("after %d quiet steps = %q, want none", cacheObservationSteps, got)
	}
}

func TestCacheObserverOnePositiveReportIsConclusive(t *testing.T) {
	o := NewCacheObserver(newFake("openai"))
	o.Observe(0, 0, true)
	o.Observe(4096, 0, true)
	if got := o.Verdict(); got != CacheSupported {
		t.Fatalf("after a cache read = %q, want caching", got)
	}
	// A later quiet step must not undo a proven-caching endpoint: a cache can
	// legitimately miss (TTL expiry, a changed prefix) without ceasing to exist.
	for i := 0; i < cacheObservationSteps+2; i++ {
		o.Observe(0, 0, true)
	}
	if got := o.Verdict(); got != CacheSupported {
		t.Errorf("after later quiet steps = %q, want caching to stick", got)
	}
}

func TestCacheObserverIgnoresStepsWithoutARepeatedPrefix(t *testing.T) {
	o := NewCacheObserver(newFake("openai"))
	// A turn whose every step rewrote the prefix carries no evidence, no matter
	// how many steps it ran.
	for i := 0; i < cacheObservationSteps*3; i++ {
		o.Observe(0, 0, false)
	}
	if got := o.Verdict(); got != CacheUnknown {
		t.Errorf("without a repeated prefix = %q, want unknown", got)
	}
}

func TestCacheObserverSkipsObservationWhenIdentitySettlesIt(t *testing.T) {
	o := NewCacheObserver(newFake("anthropic"))
	for i := 0; i < cacheObservationSteps*2; i++ {
		o.Observe(0, 0, true)
	}
	if got := o.Verdict(); got != CacheSupported {
		t.Errorf("anthropic = %q, want caching regardless of reported usage", got)
	}
}

func TestNilCacheObserverIsUnknown(t *testing.T) {
	var o *CacheObserver
	if got := o.Verdict(); got != CacheUnknown {
		t.Errorf("nil observer = %q, want unknown", got)
	}
}
