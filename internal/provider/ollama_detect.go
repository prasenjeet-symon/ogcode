package provider

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultOllamaBaseURL is the default local Ollama OpenAI-compatible endpoint.
const DefaultOllamaBaseURL = "http://localhost:11434/v1"

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

// DetectOllama performs a combined detection: binary presence + liveness
// probe. The base URL honours OLLAMA_BASE_URL when provided, otherwise the
// default localhost endpoint is used. This is the single source of truth used
// by both the server (loadProviderMap / provider config endpoint) and the CLI
// (index command) so detection logic is never duplicated.
func DetectOllama() OllamaStatus {
	baseURL := DefaultOllamaBaseURL
	if env := strings.TrimSpace(os.Getenv("OLLAMA_BASE_URL")); env != "" {
		baseURL = env
	}
	installed := OllamaBinaryInstalled()
	running := OllamaRunning(baseURL)
	return OllamaStatus{
		Installed: installed,
		Running:   running,
		BaseURL:   baseURL,
	}
}