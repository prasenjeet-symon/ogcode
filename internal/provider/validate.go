package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ValidateCredentials makes a minimal chat request with the given credentials to
// confirm the provider accepts them. It returns nil when the credentials work,
// or an error describing the failure. Used by the settings/onboarding "test key"
// flow. The caller is responsible for any timeout via ctx.
func ValidateCredentials(ctx context.Context, providerID, apiKey, baseURL string) error {
	p, err := NewProviderWithConfig(providerID, apiKey, baseURL)
	if err != nil {
		return err
	}

	models := p.Models()
	if len(models) == 0 {
		return fmt.Errorf("no models available for %s — check your API key or base URL", providerID)
	}

	prompt, _ := json.Marshal("Hi")
	req := StreamRequest{
		Model:     models[0].ID,
		Messages:  []ModelMessage{{Role: "user", Content: prompt}},
		MaxTokens: 1,
	}

	ch, startErr := p.StreamChat(ctx, req)
	if startErr != nil {
		return startErr
	}

	var streamErr string
	for ev := range ch {
		if ev.Type == EventError {
			streamErr = ev.Error
		}
	}
	if streamErr != "" {
		return errors.New(streamErr)
	}
	return nil
}
