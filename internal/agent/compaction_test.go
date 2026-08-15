package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

func TestCompactionKeepRecent_ScalesWithWindow(t *testing.T) {
	cases := map[int]int{
		0:       12, // unknown window → historical default
		8000:    8,  // clamped to the minimum
		80000:   8,
		128000:  12,
		200000:  20,
		1000000: 40, // clamped to the maximum
	}
	for window, want := range cases {
		if got := compactionKeepRecent(window); got != want {
			t.Errorf("compactionKeepRecent(%d) = %d, want %d", window, got, want)
		}
	}
}

func TestReplaceOrAppendSummary(t *testing.T) {
	// Prior summary is the last entry → replaced in place (no duplication).
	got := replaceOrAppendSummary([]string{"base", "date", "OLD"}, "OLD", "NEW")
	if len(got) != 3 || got[2] != "NEW" {
		t.Errorf("replace-in-place failed: %v", got)
	}
	// Prior summary not present → appended.
	got = replaceOrAppendSummary([]string{"base", "date"}, "OLD", "NEW")
	if len(got) != 3 || got[2] != "NEW" {
		t.Errorf("append failed: %v", got)
	}
	// Empty prior summary → appended (first compaction).
	got = replaceOrAppendSummary([]string{"base", "date"}, "", "NEW")
	if len(got) != 3 || got[2] != "NEW" {
		t.Errorf("append on empty prev failed: %v", got)
	}
}

// captureSummarizerProvider records the summarizer request's user prompt and
// returns a canned summary, so a test can assert what llmCompact sent.
type captureSummarizerProvider struct {
	mu              sync.Mutex
	lastUserContent string
	summary         string
}

func (m *captureSummarizerProvider) ID() string { return "mock" }
func (m *captureSummarizerProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock"}}
}
func (m *captureSummarizerProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.mu.Lock()
	if len(req.Messages) > 0 {
		var c string
		json.Unmarshal(req.Messages[0].Content, &c)
		m.lastUserContent = c
	}
	m.mu.Unlock()
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: m.summary}
		fr := "stop"
		ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
	}()
	return ch, nil
}

func makeMessages(n int) []provider.ModelMessage {
	msgs := make([]provider.ModelMessage, 0, n)
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		c, _ := json.Marshal(fmt.Sprintf("message %d content with detail", i))
		msgs = append(msgs, provider.ModelMessage{Role: role, Content: c})
	}
	return msgs
}

func TestLLMCompact_FoldsPriorSummaryAndScalesKeepRecent(t *testing.T) {
	lr := &LoopRunner{} // llmCompact uses only the provider, not lr's fields
	mock := &captureSummarizerProvider{summary: "MERGED SUMMARY TEXT"}
	msgs := makeMessages(20)
	const prior = "PRIOR-SUMMARY-BODY-XYZ"

	addendum, recent := lr.llmCompact(context.Background(), mock, "mock-model", msgs, prior, 0)

	// The returned addendum carries the canned summary.
	if !strings.Contains(addendum, "MERGED SUMMARY TEXT") {
		t.Errorf("addendum missing summary text: %q", addendum)
	}
	// The prior summary was folded into the summarizer input (non-destructive).
	mock.mu.Lock()
	sent := mock.lastUserContent
	mock.mu.Unlock()
	if !strings.Contains(sent, "### PRIOR SUMMARY") || !strings.Contains(sent, prior) {
		t.Errorf("summarizer prompt did not fold the prior summary; got:\n%s", sent)
	}
	// Window 0 keeps the default 12 most-recent messages verbatim.
	if len(recent) != 12 {
		t.Errorf("expected 12 recent messages kept (window 0), got %d", len(recent))
	}
}

func TestLLMCompact_NoPriorSummary_LargeWindowKeepsMore(t *testing.T) {
	lr := &LoopRunner{}
	mock := &captureSummarizerProvider{summary: "SUMMARY"}
	msgs := makeMessages(60)

	_, recent := lr.llmCompact(context.Background(), mock, "mock-model", msgs, "", 400000)

	// No prior summary → the summarizer prompt uses the plain history framing.
	mock.mu.Lock()
	sent := mock.lastUserContent
	mock.mu.Unlock()
	if strings.Contains(sent, "### PRIOR SUMMARY") {
		t.Error("no prior summary should have been folded in")
	}
	if !strings.Contains(sent, "Conversation history:") {
		t.Errorf("expected plain history framing; got:\n%s", sent)
	}
	// A 400k window keeps 40 recent messages verbatim (the cap).
	if len(recent) != 40 {
		t.Errorf("expected 40 recent messages kept (400k window), got %d", len(recent))
	}
}
