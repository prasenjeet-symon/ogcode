package provider

import (
	"fmt"
	"os"
	"strings"
)

// OGX is the subscription plan sold by OG Lab. Inference runs through OG Lab's
// gateway, which speaks the OpenAI Chat Completions API, so the provider is an
// ordinary *OpenAIProvider pointed at the gateway with the token the connect
// flow stored — no protocol of its own.
//
// Two things make it different from every other OpenAI-compatible endpoint:
//
//   - The gateway's /v1/models IS the plan. It returns exactly the models the
//     account's plan grants, and an empty list when the plan grants none. So
//     Models() has no static fallback: a catalogue that comes back empty means
//     the plan carries nothing, and offering a fallback would present models
//     the account cannot reach.
//   - The token is durable and per-install, and the gateway reads the
//     prompt_cache_key field as the session identity for token spend, so every
//     request carries it.
//
// Registration is gated on the stored link carrying a plan (session.OGXAccount
// .HasPlan). A planless link contributes no provider at all, which is what keeps
// it from becoming the default. One residual case is left unguarded: a plan
// whose catalogue fetches empty registers a provider that can serve nothing, and
// ProviderPriority would place it ahead of every other provider. That only
// happens for a plan with no granted models, which the settings screen surfaces
// as an empty, explained list rather than a failure.
const OGXProviderID = "ogx"

// DefaultOGXGatewayURL is OG Lab's gateway, the endpoint the OGX token is valid
// against. OGX_GATEWAY_URL overrides it — development and staging point this at
// a local gateway.
const DefaultOGXGatewayURL = "https://ogx.ogcode.xyz/v1"

// OGXGatewayURL returns the gateway base URL to use, honouring OGX_GATEWAY_URL.
func OGXGatewayURL() string {
	if u := strings.TrimSpace(os.Getenv("OGX_GATEWAY_URL")); u != "" {
		return u
	}
	return DefaultOGXGatewayURL
}

// NewOGXProvider creates the provider for a connected OGX account. The model is
// left empty on purpose: unlike the other OpenAI-compatible providers there is
// no useful guess to make, because the gateway is the only thing that knows
// what the plan grants — Models() resolves a default from the fetched
// catalogue. The collection is empty too, so the UI groups the models under the
// provider id ("ogx") rather than inventing a label.
func NewOGXProvider(token string) (*OpenAIProvider, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("ogx: empty token")
	}
	return &OpenAIProvider{
		id:      OGXProviderID,
		apiKey:  token,
		baseURL: OGXGatewayURL(),
	}, nil
}
