package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func wantNote(t *testing.T, m oaiMessage, i int) {
	t.Helper()
	if got := partText(t, m, i); got != omittedImageNote() {
		t.Errorf("part %d = %q, want the omission note", i, got)
	}
}

func wantImage(t *testing.T, m oaiMessage, i int) {
	t.Helper()
	if got := partText(t, m, i); got != "" {
		t.Errorf("part %d = %q, want an image kept", i, got)
	}
}

// bodyLayout is what a posted request still carries: how many messages it has,
// which of them hold the omission note, which still hold an image, and the base64
// payload left on the wire.
type bodyLayout struct {
	messages int
	notes    []int
	images   []int
	payload  int
}

func layoutOf(t *testing.T, body []byte) bodyLayout {
	t.Helper()
	var req oaiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("posted body is not JSON: %v", err)
	}
	var layout bodyLayout
	layout.messages = len(req.Messages)
	for i, m := range req.Messages {
		parts, ok := m.Content.([]any)
		if !ok {
			continue
		}
		note, image := false, false
		for _, part := range parts {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			switch pm["type"] {
			case "image_url":
				image = true
				if u, ok := pm["image_url"].(map[string]any); ok {
					if url, ok := u["url"].(string); ok {
						layout.payload += len(url)
					}
				}
			case "text":
				if text, _ := pm["text"].(string); text == omittedImageNote() {
					note = true
				}
			}
		}
		if note {
			layout.notes = append(layout.notes, i)
		}
		if image {
			layout.images = append(layout.images, i)
		}
	}
	return layout
}

// TestShedOldestImagesKeepsANewestFramesWindow pins the window itself: the
// budget is spent on the newest tool-result frames, every older frame becomes a
// note, and the user's own attachment is never touched by it.
func TestShedOldestImagesKeepsANewestFramesWindow(t *testing.T) {
	messages := []oaiMessage{
		{Role: "user", Content: []any{
			map[string]any{"type": "text", "text": "look at this"},
			imageURLPart(bodyLimitImage("attachment")),
		}},
	}
	for _, name := range []string{"first", "second", "third", "fourth", "fifth"} {
		messages = append(messages, oaiMessage{Role: "user", Content: []any{
			map[string]any{"type": "text", "text": "Rendered image(s) for the preceding tool result:"},
			imageURLPart(bodyLimitImage(name)),
		}})
	}
	images := []wireImage{{message: 0, part: 1, bytes: 100}}
	for i := 1; i <= 5; i++ {
		images = append(images, wireImage{message: i, part: 1, tool: true, bytes: 100})
	}

	if shed := shedOldestImages(messages, images, 250); shed != 3 {
		t.Fatalf("shed %d frames, want 3 (a 250-byte budget keeps the newest two of 100 bytes each)", shed)
	}
	wantImage(t, messages[0], 1) // the user's attachment
	wantNote(t, messages[1], 1)
	wantNote(t, messages[2], 1)
	wantNote(t, messages[3], 1)
	wantImage(t, messages[4], 1)
	wantImage(t, messages[5], 1)

	// A second pass finds nothing left to shed: the window is already the answer.
	if shed := shedOldestImages(messages, images, 250); shed != 0 {
		t.Errorf("shed %d more frames on a second pass, want 0", shed)
	}
}

// TestShedOldestImagesKeepsTheNewestFrameOverBudget pins the one frame the
// window may not drop: a screenshot larger than the whole budget still goes,
// because it is the page the step is looking at.
func TestShedOldestImagesKeepsTheNewestFrameOverBudget(t *testing.T) {
	messages := []oaiMessage{
		{Role: "user", Content: []any{
			map[string]any{"type": "text", "text": "Rendered image(s) for the preceding tool result:"},
			imageURLPart(bodyLimitImage("newest")),
		}},
	}
	images := []wireImage{{message: 0, part: 1, tool: true, bytes: 8 << 20}}
	if shed := shedOldestImages(messages, images, ogxShedRetainBytes); shed != 0 {
		t.Fatalf("shed %d frames, want 0", shed)
	}
	wantImage(t, messages[0], 1)
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

// TestStreamChatShedsToANewestFramesWindow pins what the shed boundary does
// across steps. Session history keeps every screenshot, so each step rebuilds
// the full turn and sheds again: the window must leave every message it already
// shed alone, so the provider's cached prefix runs up to the newest frames
// instead of ending near the start of the request.
func TestStreamChatShedsToANewestFramesWindow(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies [][]byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
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

	frame := bodyLimitImage(strings.Repeat("A", 700<<10))
	turn := func(frames int) []ModelMessage {
		messages := []ModelMessage{{Role: "user", Content: json.RawMessage(`"screen the page"`)}}
		for i := 0; i < frames; i++ {
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
		return messages
	}

	// Two consecutive steps of one growing turn, both over the cap.
	for _, frames := range []int{30, 31} {
		ch, err := p.StreamChat(context.Background(), StreamRequest{Model: "ogx-deepseek-v4.1-flash", Messages: turn(frames)})
		if err != nil {
			t.Fatalf("StreamChat with %d frames: %v", frames, err)
		}
		for range ch {
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("posted %d requests, want 2", len(bodies))
	}
	first, next := layoutOf(t, bodies[0]), layoutOf(t, bodies[1])
	t.Logf("step 1: %d bytes posted, %d messages, %d notes, %d images (%d image bytes)", len(bodies[0]), first.messages, len(first.notes), len(first.images), first.payload)
	t.Logf("step 2: %d bytes posted, %d messages, %d notes, %d images (%d image bytes)", len(bodies[1]), next.messages, len(next.notes), len(next.images), next.payload)

	for i, layout := range []bodyLayout{first, next} {
		if len(bodies[i]) > ogxMaxRequestBytes {
			t.Errorf("step %d posted %d bytes, over the %d-byte limit", i+1, len(bodies[i]), ogxMaxRequestBytes)
		}
		if len(layout.images) == 0 {
			t.Fatalf("step %d kept no image", i+1)
		}
		// The retained window is bounded by the budget, not by the turn's size.
		if limit := ogxShedRetainBytes + 64*len(layout.images); layout.payload > limit {
			t.Errorf("step %d kept %d bytes of images, over the %d-byte window (plus URL prefixes)", i+1, layout.payload, ogxShedRetainBytes)
		}
		if got := layout.images[len(layout.images)-1]; got != layout.messages-1 {
			t.Errorf("step %d kept the newest frame at message %d, want the last message %d", i+1, got, layout.messages-1)
		}
	}

	// One more frame arrives, one more frame leaves the window — and every note
	// the previous step sent is still a note, in the same place.
	if got, want := len(next.notes), len(first.notes)+1; got != want {
		t.Errorf("second step sent %d notes, want %d", got, want)
	}
	for i := range first.notes {
		if first.notes[i] != next.notes[i] {
			t.Fatalf("note %d moved from message %d to %d: the cached prefix no longer ends where it did", i, first.notes[i], next.notes[i])
		}
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
