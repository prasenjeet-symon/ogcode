package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// engineGateway answers a completion with the given X-Ogx-Engine header (when
// non-empty) and a one-word stream, so the header's round trip through
// StreamChat is the thing under test.
func engineGateway(t *testing.T, engine string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if engine != "" {
			w.Header().Set(engineHeader, engine)
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"SAFE\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// anthropicEngineGateway is engineGateway in Anthropic's SSE dialect: the same
// header on a /v1/messages response, because a gateway in front of Anthropic
// sets the same X-Ogx-Engine.
func anthropicEngineGateway(t *testing.T, engine string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if engine != "" {
			w.Header().Set(engineHeader, engine)
		}
		_, _ = w.Write([]byte("event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"SAFE"}}` + "\n\n" +
			"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestStreamChatReportsRouterEngine pins the reporting itself on both
// transports: a header from the gateway becomes an EventEngine carrying the
// same value, a padded or mixed-case value is normalised, and a response with
// no header produces no engine event rather than an empty one. The Auto-mode
// risk gate keys its "do not cache" decision on this event (§16.3 of the
// router's jev-risk-gate plan), so a silently dropped header — on either
// transport — would re-enable the durable cache it exists to avoid.
func TestStreamChatReportsRouterEngine(t *testing.T) {
	transports := []struct {
		name string
		new  func(t *testing.T, engine string) Provider
	}{
		{"openai-compatible", func(t *testing.T, engine string) Provider {
			t.Setenv("OGX_GATEWAY_URL", engineGateway(t, engine).URL)
			p, err := NewOGXProvider("tok")
			if err != nil {
				t.Fatalf("NewOGXProvider: %v", err)
			}
			return p
		}},
		{"anthropic", func(t *testing.T, engine string) Provider {
			return &AnthropicProvider{apiKey: "test-key", baseURL: anthropicEngineGateway(t, engine).URL}
		}},
	}
	cases := []struct {
		name      string
		header    string
		want      string
		wantEvent bool
	}{
		{name: "clef arm", header: "clef", want: "clef", wantEvent: true},
		{name: "relayed arm", header: "llm", want: "llm", wantEvent: true},
		{name: "padded mixed case", header: "  CLEF ", want: "clef", wantEvent: true},
		{name: "no header", header: "", wantEvent: false},
	}
	for _, tr := range transports {
		for _, tc := range cases {
			t.Run(tr.name+"/"+tc.name, func(t *testing.T) {
				p := tr.new(t, tc.header)
				ch, err := p.StreamChat(context.Background(), StreamRequest{
					Model:    "m",
					Messages: []ModelMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}},
				})
				if err != nil {
					t.Fatalf("StreamChat: %v", err)
				}

				var got string
				var seen bool
				for evt := range ch {
					if evt.Type == EventEngine {
						if seen {
							t.Errorf("more than one engine event: %q then %q", got, evt.Engine)
						}
						got, seen = evt.Engine, true
					}
				}
				if seen != tc.wantEvent {
					t.Fatalf("engine event seen = %v, want %v (engine %q)", seen, tc.wantEvent, got)
				}
				if got != tc.want {
					t.Errorf("engine = %q, want %q", got, tc.want)
				}
			})
		}
	}
}

// TestReportedEngineNormalises covers the trimming directly: the HTTP client
// trims header values on the way in, so a gateway-level case cannot prove that
// the provider trims a padded value itself.
func TestReportedEngineNormalises(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"clef", "clef"},
		{"  CLEF ", "clef"},
		{"LLM", "llm"},
		{"", ""},
		{"   ", ""},
	} {
		if got := reportedEngine(tc.in); got != tc.want {
			t.Errorf("reportedEngine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
