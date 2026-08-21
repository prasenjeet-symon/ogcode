package provider

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DefaultOllamaBaseURL is the default local Ollama OpenAI-compatible endpoint.
const DefaultOllamaBaseURL = "http://localhost:11434/v1"

// defaultOllamaFallbackURLs are probed, in priority order, when no base URL is
// configured and the default local endpoint is not answering. They cover the
// deployment where Ollama is not installed on this machine at all and requests
// are served by a proxy in front of remote instances.
var defaultOllamaFallbackURLs = []string{
	"http://localhost:8090/v1", // quota-aware multi-account router
	"http://localhost/llm/v1",  // path-routed reverse proxy pool
}

// PrimaryOllamaBaseURL is the first candidate probed. A package-level var
// rather than a constant so tests can point it at a dead address and exercise
// the fallback path deterministically.
var PrimaryOllamaBaseURL = DefaultOllamaBaseURL

// defaultOllamaHealthURL is the root Ollama endpoint used for the liveness probe.
// We probe the root (not /v1/models) because it is the cheapest 200 the server
// returns and does not require any authentication or model knowledge.
const defaultOllamaHealthURL = "http://localhost:11434"

// OllamaStatus describes the runtime detection state of a local Ollama
// install. It is computed by DetectOllama and surfaced to the frontend so the
// onboarding gate can treat a running instance as already configured.
type OllamaStatus struct {
	// Installed reports whether the `ollama` binary is found on $PATH (via
	// exec.LookPath — cross-platform, unlike hardcoded install paths).
	Installed bool `json:"installed"`
	// Running reports whether the Ollama server responded to a health probe
	// (GET http://localhost:11434 with a short timeout). This is the reliable
	// signal that the endpoint is actually usable right now.
	Running bool `json:"running"`
	// BaseURL is the detected/expected Ollama base URL. It honours
	// OLLAMA_BASE_URL when set, otherwise defaults to the localhost endpoint.
	BaseURL string `json:"baseUrl"`
}

// OllamaBinaryInstalled reports whether the `ollama` executable is on $PATH.
// Uses exec.LookPath (cross-platform) rather than probing a fixed set of
// install directories.
func OllamaBinaryInstalled() bool {
	_, err := exec.LookPath("ollama")
	return err == nil
}

// ollamaHealthURL derives the liveness-probe URL from a base URL. When the
// base URL is empty or the default localhost endpoint, we probe the root
// http://localhost:11434. For a custom base URL we strip the trailing /v1 (or
// /v1/) so the probe hits the server root, which is the cheapest 200.
func ollamaHealthURL(baseURL string) string {
	if baseURL == "" {
		return defaultOllamaHealthURL
	}
	// If the user configured a custom OLLAMA_BASE_URL, honour it: probe the
	// root of that host rather than localhost. Strip a trailing /v1 segment.
	u := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(u, "/v1") {
		u = strings.TrimSuffix(u, "/v1")
	}
	return u
}

// OllamaRunning probes the Ollama server at the given base URL (or the
// default localhost endpoint when empty) with a short timeout. Returns true
// when the server responds with HTTP 200. The probe is best-effort: any
// transport error or non-200 status is treated as "not running".
func OllamaRunning(baseURL string) bool {
	probeURL := ollamaHealthURL(baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ollamaFallbackURLs returns the fallback candidates to probe after the
// primary endpoint. OLLAMA_FALLBACK_URLS overrides the built-in list
// (comma-separated); setting it to an empty value disables fallback probing.
func ollamaFallbackURLs() []string {
	raw, ok := os.LookupEnv("OLLAMA_FALLBACK_URLS")
	if !ok {
		return defaultOllamaFallbackURLs
	}
	var out []string
	for _, u := range strings.Split(raw, ",") {
		if u = strings.TrimSpace(u); u != "" {
			out = append(out, u)
		}
	}
	return out
}

// probeCandidates probes every candidate concurrently and returns the first
// one in priority order that responded. Concurrency is the point: probing
// serially would add a full timeout per dead candidate to every startup, and
// the common case (nothing running) is exactly the all-dead case.
func probeCandidates(urls []string) (string, bool) {
	live := make([]bool, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			live[i] = OllamaRunning(u)
		}(i, u)
	}
	wg.Wait()
	for i, ok := range live {
		if ok {
			return urls[i], true
		}
	}
	return "", false
}

// DetectOllama performs a combined detection: binary presence + liveness
// probe. This is the single source of truth used by both the server
// (loadProviderMap / provider config endpoint) and the CLI (index command) so
// detection logic is never duplicated.
//
// Resolution order:
//  1. OLLAMA_BASE_URL, when set, is authoritative — we never probe elsewhere
//     when the user has named a target.
//  2. The default local endpoint (localhost:11434).
//  3. The fallback candidates, so a machine with no local Ollama install still
//     finds a router or proxy serving remote instances.
func DetectOllama() OllamaStatus {
	installed := OllamaBinaryInstalled()

	if env := strings.TrimSpace(os.Getenv("OLLAMA_BASE_URL")); env != "" {
		return OllamaStatus{
			Installed: installed,
			Running:   OllamaRunning(env),
			BaseURL:   env,
		}
	}

	candidates := append([]string{PrimaryOllamaBaseURL}, ollamaFallbackURLs()...)
	if url, ok := probeCandidates(candidates); ok {
		return OllamaStatus{Installed: installed, Running: true, BaseURL: url}
	}

	return OllamaStatus{
		Installed: installed,
		Running:   false,
		BaseURL:   PrimaryOllamaBaseURL,
	}
}

// PreferLiveOllamaEndpoint resolves which endpoint to actually use when a base
// URL was configured (a persisted config row) and detection found a different
// live one.
//
// A configured endpoint wins while it is still answering. A configured
// endpoint that has gone dead yields to whatever detection found, so a row
// persisted from an earlier launch cannot permanently shadow a working
// endpoint — the "local Ollama was uninstalled, but a router is up" case.
//
// An explicit OLLAMA_BASE_URL must never be passed here: it is authoritative
// by definition and callers should use it directly.
func PreferLiveOllamaEndpoint(configured string, st OllamaStatus) string {
	if configured == "" {
		return st.BaseURL
	}
	if !st.Running || st.BaseURL == configured {
		return configured
	}
	if OllamaRunning(configured) {
		return configured
	}
	return st.BaseURL
}
