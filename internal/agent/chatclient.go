package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// synthClient is a blocking, one-shot chat wrapper over a streaming provider: it
// sends a single system+user request and returns the full concatenated text. It
// is used only to synthesize a turn summary in writeTurnMemory.
//
// It was relocated from the removed internal/memory package (its
// ChatClient/NewChatClient/Chat), which the turn-memory writer is now the only
// caller of — recall goes through the read-only sub-agent, not a chat client.
type synthClient struct {
	provider provider.Provider
	model    string
}

func newSynthClient(p provider.Provider, model string) *synthClient {
	return &synthClient{provider: p, model: model}
}

func (c *synthClient) Chat(ctx context.Context, system, prompt string) (string, error) {
	model := c.model
	if model == "" {
		models := c.provider.Models()
		if len(models) == 0 {
			return "", fmt.Errorf("synth: provider %q has no models to synthesize with", c.provider.ID())
		}
		model = models[0].ID
	}
	req := provider.StreamRequest{
		Model:    model,
		System:   []string{system},
		Messages: []provider.ModelMessage{{Role: "user", Content: json.RawMessage(fmt.Sprintf("%q", prompt))}},
	}
	ch, err := c.provider.StreamChat(ctx, req)
	if err != nil {
		return "", err
	}
	var parts []string
	for ev := range ch {
		switch ev.Type {
		case provider.EventTextDelta:
			parts = append(parts, ev.Text)
		case provider.EventError:
			return "", fmt.Errorf("chat error: %s", ev.Error)
		}
	}
	return strings.Join(parts, ""), nil
}
