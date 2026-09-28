package server

import (
	"context"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

type usageTextProvider struct{ events []provider.StreamEvent }

func (p usageTextProvider) ID() string { return "test" }
func (p usageTextProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "test-model"}}
}
func (p usageTextProvider) StreamChat(context.Context, provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, len(p.events))
	for _, e := range p.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// Plan auto-naming used to drop its usage; collectStreamText now hands it back
// (the last cumulative report) so autoNamePlan can charge the plan's session.
func TestCollectStreamTextReturnsUsage(t *testing.T) {
	p := usageTextProvider{events: []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: "Ship the login page"},
		{Type: provider.EventUsage, Usage: &provider.TokenUsage{InputTokens: 120, OutputTokens: 3}},
		{Type: provider.EventUsage, Usage: &provider.TokenUsage{InputTokens: 120, OutputTokens: 9}},
	}}
	text, usage, err := collectStreamText(context.Background(), p, "test-model", "sys", "user")
	if err != nil || text != "Ship the login page" {
		t.Fatalf("collectStreamText = %q, %v", text, err)
	}
	if usage == nil || usage.InputTokens != 120 || usage.OutputTokens != 9 {
		t.Errorf("usage = %+v, want the last report (120 in, 9 out)", usage)
	}
}
