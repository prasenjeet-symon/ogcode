package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// deadURL is a port nothing listens on, used to force a failed probe.
const deadURL = "http://127.0.0.1:1/v1"

// liveOllama starts a stub that answers the root health probe with 200, the
// same way a real Ollama server (or a proxy in front of one) does.
func liveOllama(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

func TestOllamaFallbackURLs(t *testing.T) {
	t.Run("built-in list when unset", func(t *testing.T) {
		got := ollamaFallbackURLs()
		if len(got) != len(defaultOllamaFallbackURLs) {
			t.Fatalf("got %d fallbacks, want %d", len(got), len(defaultOllamaFallbackURLs))
		}
	})

	t.Run("env overrides and trims", func(t *testing.T) {
		t.Setenv("OLLAMA_FALLBACK_URLS", " http://a/v1 , http://b/v1 ")
		got := ollamaFallbackURLs()
		want := []string{"http://a/v1", "http://b/v1"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("empty env disables fallbacks", func(t *testing.T) {
		t.Setenv("OLLAMA_FALLBACK_URLS", "")
		if got := ollamaFallbackURLs(); len(got) != 0 {
			t.Errorf("got %v, want no fallbacks", got)
		}
	})
}

func TestProbeCandidatesPrefersPriorityOrder(t *testing.T) {
	first := liveOllama(t)
	second := liveOllama(t)

	got, ok := probeCandidates([]string{deadURL, first, second})
	if !ok {
		t.Fatal("expected a live candidate")
	}
	if got != first {
		t.Errorf("got %q, want the highest-priority live candidate %q", got, first)
	}
}

func TestProbeCandidatesAllDead(t *testing.T) {
	if got, ok := probeCandidates([]string{deadURL, deadURL}); ok {
		t.Errorf("got %q, want no live candidate", got)
	}
}

func TestDetectOllamaFallsBackWhenPrimaryIsDead(t *testing.T) {
	router := liveOllama(t)

	// No local Ollama listening, but a router is up.
	orig := PrimaryOllamaBaseURL
	PrimaryOllamaBaseURL = deadURL
	t.Cleanup(func() { PrimaryOllamaBaseURL = orig })
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_FALLBACK_URLS", router)

	st := DetectOllama()
	if !st.Running {
		t.Fatal("expected the fallback endpoint to register as running")
	}
	if st.BaseURL != router {
		t.Errorf("got base URL %q, want the fallback %q", st.BaseURL, router)
	}
}

func TestDetectOllamaPrefersPrimaryWhenLive(t *testing.T) {
	primary := liveOllama(t)
	fallback := liveOllama(t)

	orig := PrimaryOllamaBaseURL
	PrimaryOllamaBaseURL = primary
	t.Cleanup(func() { PrimaryOllamaBaseURL = orig })
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_FALLBACK_URLS", fallback)

	if got := DetectOllama().BaseURL; got != primary {
		t.Errorf("got %q, want the primary endpoint %q", got, primary)
	}
}

func TestDetectOllamaExplicitEnvIsAuthoritative(t *testing.T) {
	fallback := liveOllama(t)

	orig := PrimaryOllamaBaseURL
	PrimaryOllamaBaseURL = deadURL
	t.Cleanup(func() { PrimaryOllamaBaseURL = orig })
	// A named endpoint must never be silently swapped for a live one.
	t.Setenv("OLLAMA_BASE_URL", deadURL)
	t.Setenv("OLLAMA_FALLBACK_URLS", fallback)

	st := DetectOllama()
	if st.BaseURL != deadURL {
		t.Errorf("got %q, want the explicitly configured %q", st.BaseURL, deadURL)
	}
	if st.Running {
		t.Error("explicit dead endpoint should report not running")
	}
}

func TestPreferLiveOllamaEndpoint(t *testing.T) {
	live := liveOllama(t)

	t.Run("no configured value uses detection", func(t *testing.T) {
		st := OllamaStatus{Running: true, BaseURL: live}
		if got := PreferLiveOllamaEndpoint("", st); got != live {
			t.Errorf("got %q, want %q", got, live)
		}
	})

	t.Run("stale configured endpoint yields to detected", func(t *testing.T) {
		st := OllamaStatus{Running: true, BaseURL: live}
		if got := PreferLiveOllamaEndpoint(deadURL, st); got != live {
			t.Errorf("got %q, want the detected live endpoint %q", got, live)
		}
	})

	t.Run("live configured endpoint wins", func(t *testing.T) {
		other := liveOllama(t)
		st := OllamaStatus{Running: true, BaseURL: other}
		if got := PreferLiveOllamaEndpoint(live, st); got != live {
			t.Errorf("got %q, want the configured endpoint %q", got, live)
		}
	})

	t.Run("nothing detected keeps configured", func(t *testing.T) {
		st := OllamaStatus{Running: false, BaseURL: DefaultOllamaBaseURL}
		if got := PreferLiveOllamaEndpoint(deadURL, st); got != deadURL {
			t.Errorf("got %q, want the configured endpoint unchanged", got)
		}
	})
}
