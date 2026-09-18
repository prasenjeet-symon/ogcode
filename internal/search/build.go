package search

import (
	"os"
	"strings"
)

// BuildBackend assembles the search backend chain a deep-research run uses.
// provider is matched against ProviderTavily; anything else — the empty string
// included — selects the native chain. It is matched here rather than against
// the session package's copy of the id so this stays a leaf package any entry
// point can build a backend from.
//
// The native chain is the owned default and always the last link, so an
// answerable query is never lost to a third-party outage: a bad Tavily key, an
// exhausted quota or a network error falls through to it rather than failing
// the call. OGCODE_SEARCH_BROWSER picks which native engine leads — "native"
// for the HTTP path alone, "safari" to drive a real browser first — and the
// default puts the HTTP path in front with Safari behind it.
//
// tavilyAPIKey is the stored key; TAVILY_API_KEY overrides it, mirroring the
// provider-key env overlay so scripted and CI runs can supply one without
// touching stored config. An empty key means no Tavily link at all — the
// provider selection alone is not enough to build one.
//
// It takes plain strings rather than a config struct because both the server
// and the headless CLI build a backend from their own config sources, and the
// prompt they hand the agent names deep_search either way: whichever entry
// point cannot build a backend is the one whose agent is told about a tool it
// will never be offered.
func BuildBackend(provider, tavilyAPIKey string) Backend {
	native := NewNativeBackend()

	var nativeChain Backend
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OGCODE_SEARCH_BROWSER"))) {
	case "native":
		nativeChain = native
	case "safari":
		nativeChain = NewFallbackBackend(NewSafariBackend(), native)
	default:
		nativeChain = NewFallbackBackend(native, NewSafariBackend())
	}

	if strings.EqualFold(strings.TrimSpace(provider), ProviderTavily) {
		if key := resolveTavilyKey(tavilyAPIKey); key != "" {
			return NewFallbackBackend(NewTavilyBackend(key), nativeChain)
		}
	}
	return nativeChain
}

// resolveTavilyKey returns the Tavily key in effect: the environment overrides
// the stored value.
func resolveTavilyKey(stored string) string {
	if env := strings.TrimSpace(os.Getenv("TAVILY_API_KEY")); env != "" {
		return env
	}
	return strings.TrimSpace(stored)
}
