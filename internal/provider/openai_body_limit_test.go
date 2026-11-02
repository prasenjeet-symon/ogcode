package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// partText returns the text of content part i of a message assembled the way
// StreamChat assembles one.
func partText(t *testing.T, m oaiMessage, i int) string {
	t.Helper()
	parts, ok := m.Content.([]any)
	if !ok {
		t.Fatalf("message content is %T, want parts", m.Content)
	}
	if i >= len(parts) {
		t.Fatalf("message has %d parts, want index %d", len(parts), i)
	}
	pm, ok := parts[i].(map[string]any)
	if !ok {
		t.Fatalf("part %d is %T, want a map", i, parts[i])
	}
	text, _ := pm["text"].(string)
	return text
}

func bodyLimitImage(data string) MessageImage {
	return MessageImage{MediaType: "image/png", Data: data}
}

// TestDropOldestImageShedsToolFramesFirst pins the order the request sheds
// images in: a tool result's rendered screenshots go before the user's own
// attachment, and the oldest of each goes first. Shedding the user's image
// first would throw away the thing the turn was asked about.
func TestDropOldestImageShedsToolFramesFirst(t *testing.T) {
	messages := []oaiMessage{
		{Role: "user", Content: []any{
			map[string]any{"type": "text", "text": "look at this"},
			imageURLPart(bodyLimitImage("attachment")),
		}},
		{Role: "assistant", Content: "scrolling"},
		{Role: "user", Content: []any{
			map[string]any{"type": "text", "text": "Rendered image(s) for the preceding tool result:"},
			imageURLPart(bodyLimitImage("first")),
			imageURLPart(bodyLimitImage("second")),
		}},
	}
	images := []wireImage{
		{message: 0, part: 1},
		{message: 2, part: 1, tool: true},
		{message: 2, part: 2, tool: true},
	}

	steps := []struct {
		who  string
		text func() string
	}{
		{"oldest tool frame", func() string { return partText(t, messages[2], 1) }},
		{"newer tool frame", func() string { return partText(t, messages[2], 2) }},
		{"user attachment", func() string { return partText(t, messages[0], 1) }},
	}
	for _, step := range steps {
		if !dropOldestImage(messages, images) {
			t.Fatalf("shedding %s reported no image left", step.who)
		}
		if got := step.text(); got != omittedImageNote() {
			t.Errorf("%s = %q, want the omission note", step.who, got)
		}
	}
	if dropOldestImage(messages, images) {
		t.Error("shed an image after every image was shed")
	}
	// The message text around a shed image survives.
	if got := partText(t, messages[0], 0); got != "look at this" {
		t.Errorf("user text = %q, want it untouched", got)
	}
}

// TestStreamChatShedsImagesToFitOGXLimit pins the behaviour the gateway's 20 MiB
// cap demands: a turn that screens the browser several times no longer dies
// with ErrRequestTooLarge — the oldest frames are shed, a note takes their
// place, and the request that reaches the wire fits.
func TestStreamChatShedsImagesToFitOGXLimit(t *testing.T) {
	var (
		mu      sync.Mutex
		gotLen  int
		gotBody []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body := make([]byte, 0, r.ContentLength)
		buf := make([]byte, 64<<10)
		for {
			n, err := r.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
		mu.Lock()
		gotLen, gotBody = len(body), body
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OGX_GATEWAY_URL", srv.URL)

	p, err := NewOGXProvider("tok")
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	// Thirty-two ~700 KB frames: a long browser-screening turn well over the
	// cap, which used to fail outright at the last screenshot.
	frame := bodyLimitImage(strings.Repeat("A", 700<<10))
	messages := []ModelMessage{{Role: "user", Content: json.RawMessage(`"screen the page"`)}}
	for i := 0; i < 32; i++ {
		messages = append(messages,
			ModelMessage{Role: "assistant", Content: json.RawMessage(`"scrolling"`)},
			ModelMessage{
				Role:       "tool",
				ToolCallID: fmt.Sprintf("call_%d", i),
				Name:       "browser_screenshot",
				Content:    json.RawMessage(`"ok"`),
				Images:     []MessageImage{frame},
			},
		)
	}

	ch, err := p.StreamChat(context.Background(), StreamRequest{Model: "ogx-deepseek-v4.1-flash", Messages: messages})
	if err != nil {
		t.Fatalf("StreamChat refused an oversized turn: %v", err)
	}
	for range ch {
	}

	mu.Lock()
	defer mu.Unlock()
	if gotLen > ogxMaxRequestBytes {
		t.Fatalf("posted %d bytes, over the %d-byte limit", gotLen, ogxMaxRequestBytes)
	}
	if !strings.Contains(string(gotBody), omittedImageNote()) {
		t.Error("shed frames left no omission note in the body")
	}
	var req oaiRequest
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("posted body is not JSON: %v", err)
	}
	kept := 0
	for _, m := range req.Messages {
		parts, ok := m.Content.([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			if pm, ok := part.(map[string]any); ok && pm["type"] == "image_url" {
				kept++
			}
		}
	}
	if kept == 0 {
		t.Error("every image was shed, including the newest frame the step needed")
	}
}
