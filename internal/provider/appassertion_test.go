package provider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

const testAppSecret = "app-secret-under-test"

// assertingGateway stands in for the OGX gateway and records the headers it
// received on each call, so a test can assert on what the provider presented.
func assertingGateway(t *testing.T, seen *http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Clone()
		switch r.URL.Path {
		case "/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data":   []map[string]any{{"id": "m", "object": "model", "owned_by": "oglab"}},
			})
		case "/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestAssertionPayloadMatchesTheRouterContract pins the exact signed string,
// which must stay byte-identical to the router's copy of the same contract.
func TestAssertionPayloadMatchesTheRouterContract(t *testing.T) {
	got := assertionPayload("ogcode", "POST", "/v1/chat/completions", 1700000000)
	want := "ogcode\n1700000000\nPOST\n/v1/chat/completions"
	if got != want {
		t.Errorf("payload = %q, want %q", got, want)
	}
}

// TestSignAssertionIsHMACOfThePayload recomputes the signature independently, so
// a change to either the payload or the encoding is caught here.
func TestSignAssertionIsHMACOfThePayload(t *testing.T) {
	mac := hmac.New(sha256.New, []byte(testAppSecret))
	mac.Write([]byte("ogcode\n1700000000\nPOST\n/v1/chat/completions"))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	got := signAssertion(testAppSecret, "ogcode", "POST", "/v1/chat/completions", 1700000000)
	if got != want {
		t.Errorf("signature = %q, want %q", got, want)
	}
}

// TestSignRequestAssertionStampsTheHeaders covers the happy path: all three
// headers land and the signature verifies against the payload over the request's
// own method and path.
func TestSignRequestAssertionStampsTheHeaders(t *testing.T) {
	req, err := http.NewRequest("GET", "https://ogx.ogcode.xyz/v1/models", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	before := time.Now().Unix()
	signRequestAssertion(req, "ogcode", testAppSecret)
	after := time.Now().Unix()

	if got := req.Header.Get(headerClientApp); got != "ogcode" {
		t.Errorf("%s = %q, want ogcode", headerClientApp, got)
	}
	ts, err := strconv.ParseInt(req.Header.Get(headerClientTimestamp), 10, 64)
	if err != nil {
		t.Fatalf("%s not a unix seconds value: %v", headerClientTimestamp, err)
	}
	if ts < before || ts > after {
		t.Errorf("timestamp %d outside [%d,%d]", ts, before, after)
	}
	want := signAssertion(testAppSecret, "ogcode", "GET", "/v1/models", ts)
	if got := req.Header.Get(headerClientSignature); got != want {
		t.Errorf("signature = %q, want %q", got, want)
	}
}

// TestSignRequestAssertionIsANoOpWhenUnconfigured is the deployment with no
// identity baked in: the request must go out exactly as it would have.
func TestSignRequestAssertionIsANoOpWhenUnconfigured(t *testing.T) {
	for _, tc := range []struct{ app, secret string }{
		{"", ""},
		{"ogcode", ""},
		{"", testAppSecret},
	} {
		req, _ := http.NewRequest("GET", "https://ogx.ogcode.xyz/v1/models", nil)
		signRequestAssertion(req, tc.app, tc.secret)
		if v := req.Header.Get(headerClientApp); v != "" {
			t.Errorf("app=%q secret=%q: unexpected %s = %q", tc.app, tc.secret, headerClientApp, v)
		}
	}
}

// TestOGXProviderAssertsBothCalls proves the provider presents its identity on
// the model fetch and on a chat request alike, since the gateway gates both.
func TestOGXProviderAssertsBothCalls(t *testing.T) {
	restore := OGXAppSecret
	OGXAppSecret = testAppSecret
	t.Cleanup(func() { OGXAppSecret = restore })

	var seen http.Header
	srv := assertingGateway(t, &seen)
	t.Setenv("OGX_GATEWAY_URL", srv.URL)

	p, err := NewOGXProvider("tok")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	p.RefreshCatalog(context.Background())
	if got := seen.Get(headerClientApp); got != OGXAppID {
		t.Errorf("models call: %s = %q, want %s", headerClientApp, got, OGXAppID)
	}
	assertSignatureVerifies(t, seen, "GET", "/models")

	ch, err := p.StreamChat(context.Background(), StreamRequest{
		Model:    "m",
		Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	for range ch {
	}
	if got := seen.Get(headerClientApp); got != OGXAppID {
		t.Errorf("chat call: %s = %q, want %s", headerClientApp, got, OGXAppID)
	}
	assertSignatureVerifies(t, seen, "POST", "/chat/completions")
}

// assertSignatureVerifies recomputes the signature the gateway would check,
// using the timestamp the provider actually sent.
func assertSignatureVerifies(t *testing.T, h http.Header, method, path string) {
	t.Helper()
	ts, err := strconv.ParseInt(h.Get(headerClientTimestamp), 10, 64)
	if err != nil {
		t.Fatalf("%s not a unix seconds value: %v", headerClientTimestamp, err)
	}
	want := signAssertion(testAppSecret, OGXAppID, method, path, ts)
	if got := h.Get(headerClientSignature); got != want {
		t.Errorf("signature = %q, want %q (payload over %s %s)", got, want, method, path)
	}
}

// TestOtherProvidersDoNotAssert is the counterpart: an ordinary
// OpenAI-compatible endpoint carries no client identity, so no assertion header
// may appear on requests to it.
func TestOtherProvidersDoNotAssert(t *testing.T) {
	var seen http.Header
	srv := assertingGateway(t, &seen)

	p := &OpenAIProvider{id: "openai", apiKey: "sk-test", baseURL: srv.URL}
	p.fetchDynamicModels(context.Background())

	if v := seen.Get(headerClientApp); v != "" {
		t.Errorf("unexpected %s = %q on a non-first-party endpoint", headerClientApp, v)
	}
	if v := seen.Get(headerClientSignature); v != "" {
		t.Errorf("unexpected %s = %q on a non-first-party endpoint", headerClientSignature, v)
	}
}
