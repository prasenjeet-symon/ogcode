package provider

import (
	"net/url"
	"strings"
)

// ModelPrice is what a model costs per million tokens on one provider. A zero
// cache price means "not published": callers derive it from the input price.
type ModelPrice struct {
	Input, Output         float64
	CacheRead, CacheWrite float64
}

// Cache-token prices as a multiple of the model's base input price, used only
// when a model publishes no cache prices of its own. Anthropic charges 1.25x to
// write an entry and 0.1x to read one; providers that never bill a write report
// a cache write count of 0, so the write multiplier costs them nothing.
const (
	CacheWriteMultiplier = 1.25
	CacheReadMultiplier  = 0.10
)

// Cost prices one set of token counts in USD. Cache traffic is billed at the
// published cache prices, or at the standard multiples of the input price when
// the model publishes none. Reasoning is not a parameter: providers bill it
// inside the output count.
func (p ModelPrice) Cost(input, output, cacheRead, cacheWrite int) float64 {
	read := p.CacheRead
	if read == 0 {
		read = p.Input * CacheReadMultiplier
	}
	write := p.CacheWrite
	if write == 0 {
		write = p.Input * CacheWriteMultiplier
	}
	return (float64(input)*p.Input +
		float64(cacheWrite)*write +
		float64(cacheRead)*read +
		float64(output)*p.Output) / 1_000_000
}

// billsPerToken reports whether a provider charges per token at all. Local
// Ollama is free and Ollama Cloud is a flat subscription: quoting a vendor's
// per-token API price against it would invent a bill. OGX meters the tokens it
// serves at the plan's own rates, so it bills per token — but only at a price
// the gateway itself publishes, never the vendor's (meteredByOwnListing).
func billsPerToken(providerID string) bool {
	switch providerID {
	case "ollama":
		return false
	}
	return true
}

// meteredByOwnListing reports whether a provider is the sole authority on what
// its models cost. For an ordinary metered endpoint the catalogue's
// first-party price is a fair stand-in — it is what the model's own vendor
// charges. The OGX gateway is not that: the plan prices the models itself at
// its own rates, so a catalogue price would invent a bill and only the
// gateway's published rate (or the user's edit of it) quotes.
func meteredByOwnListing(providerID string) bool {
	return providerID == OGXProviderID
}

// BillsPerToken is billsPerToken for callers that must tell an unmetered
// provider (nothing to bill) from a metered one whose model has no known price.
func BillsPerToken(providerID string) bool { return billsPerToken(providerID) }

// ListPrice is the model's first-party price from the catalogue, whatever served
// it: what the same tokens would cost on the vendor's own API. It is the
// yardstick for a flat plan or a local run, which bill nothing per token, and
// false when the catalogue does not price the model.
func ListPrice(modelID string) (ModelPrice, bool) {
	cm, ok := LookupCatalogModel(modelID)
	if !ok || (cm.InputPricePerM == 0 && cm.OutputPricePerM == 0) {
		return ModelPrice{}, false
	}
	return ModelPrice{
		Input:      cm.InputPricePerM,
		Output:     cm.OutputPricePerM,
		CacheRead:  cm.CacheReadPricePerM,
		CacheWrite: cm.CacheWritePricePerM,
	}, true
}

// PriceOn returns what modelID costs on providerID, and false when there is no
// per-token price to quote — an unmetered provider, or a model nobody prices.
//
// A price the provider lists itself (OpenRouter's, or Anthropic's and OpenAI's
// catalogue-backed listings) is what the user pays there and wins. Otherwise the
// catalogue's first-party price stands in: it is what an OpenAI-compatible
// endpoint of the model's own vendor charges, and a fair estimate elsewhere. An
// OpenRouter ":free" variant is free whatever the model costs anywhere else —
// and a plan that prices its models itself (OGX) takes no stand-in at all: the
// gateway's published rate, or the user's edit of it, is the only real quote.
//
// A price the user configured for the model wins over all of that, field by
// field — whether they added the model themselves or edited one the provider or
// catalogue already prices: a zero input or output price falls back to the
// lookup below, so a user who knows only one of the two fills in the other.
// Cache rates behave the same: a positive CacheReadPricePerM or
// CacheWritePricePerM the user recorded (the gateway's published cache rate on
// a self-metered plan, say) quotes instead of whatever stood for it before; 0
// keeps today's fallback exactly. Nothing is billed on an unmetered provider
// regardless — billsPerToken and the ":free" rule decide before any user price
// is read.
func (r *Registry) PriceOn(providerID, modelID string) (ModelPrice, bool) {
	if !billsPerToken(providerID) || modelID == "" {
		return ModelPrice{}, false
	}
	if strings.HasSuffix(strings.ToLower(modelID), ":free") {
		return ModelPrice{}, true
	}
	price, ok := r.listedPrice(providerID, modelID)
	// The user's price for THIS provider: an override is keyed (id, provider)
	// — the same id under another slot carries other facts — and a custom
	// record answers only for the provider it was defined on.
	info, configured := r.CustomModel(modelID)
	if configured && info.ProviderID != providerID {
		info, configured = CustomModelInfo{}, false
	}
	if !configured {
		info, configured = r.ModelOverride(modelID, providerID)
	}
	if configured &&
		(info.InputPricePerM > 0 || info.OutputPricePerM > 0 ||
			info.CacheReadPricePerM > 0 || info.CacheWritePricePerM > 0) {
		if info.InputPricePerM > 0 {
			price.Input = info.InputPricePerM
			// The catalogue's cache prices are multiples of the catalogue's
			// input price; they say nothing once the user's own price stands
			// in. A self-metered provider's cache rate is its own published
			// figure, not derived from the input price, and the user edits
			// only the input/output fields — so it survives the edit.
			if !meteredByOwnListing(providerID) {
				price.CacheRead, price.CacheWrite = 0, 0
			}
		}
		if info.OutputPricePerM > 0 {
			price.Output = info.OutputPricePerM
		}
		if info.CacheReadPricePerM > 0 {
			price.CacheRead = info.CacheReadPricePerM
		}
		if info.CacheWritePricePerM > 0 {
			price.CacheWrite = info.CacheWritePricePerM
		}
		return price, true
	}
	return price, ok
}

// listedPrice is the provider's own listing, else the catalogue's first-party
// price — the lookup PriceOn applies before any user-configured price. A
// provider that prices its own listing (meteredByOwnListing) never takes the
// catalogue's standing in.
func (r *Registry) listedPrice(providerID, modelID string) (ModelPrice, bool) {
	cm, catalogued := LookupCatalogModel(modelID)
	if p := r.Get(providerID); p != nil {
		for _, m := range p.Models() {
			if m.ID != modelID || (m.InputPricePerM == 0 && m.OutputPricePerM == 0) {
				continue
			}
			price := ModelPrice{Input: m.InputPricePerM, Output: m.OutputPricePerM}
			if m.CacheReadPricePerM > 0 || m.CacheWritePricePerM > 0 {
				// The listing publishes its own cache prices (OpenRouter's
				// per-token object, the OGX gateway) — those are the user's
				// bill there, whatever the vendor charges elsewhere.
				price.CacheRead, price.CacheWrite = m.CacheReadPricePerM, m.CacheWritePricePerM
			} else if catalogued && cm.InputPricePerM == price.Input && cm.OutputPricePerM == price.Output {
				// Listed at the vendor's own price: the vendor's cache prices hold too.
				price.CacheRead, price.CacheWrite = cm.CacheReadPricePerM, cm.CacheWritePricePerM
			}
			return price, true
		}
	}
	if catalogued && !meteredByOwnListing(providerID) && (cm.InputPricePerM > 0 || cm.OutputPricePerM > 0) {
		return ModelPrice{
			Input:      cm.InputPricePerM,
			Output:     cm.OutputPricePerM,
			CacheRead:  cm.CacheReadPricePerM,
			CacheWrite: cm.CacheWritePricePerM,
		}, true
	}
	return ModelPrice{}, false
}

// defaultHosts are the endpoints the protocol slots ship with. A provider on its
// own default endpoint is named by its protocol; one pointed elsewhere is named
// by the host it actually calls.
var defaultHosts = map[string]string{
	"anthropic":  "api.anthropic.com",
	"openai":     "api.openai.com",
	"openrouter": "openrouter.ai",
}

// EndpointHost is the host a provider sends its requests to when that says
// more than the provider's name — the OpenAI slot pointed at Z.ai, the
// Anthropic slot at DeepSeek's compatible API — and "" when it does not: a
// provider on its default endpoint, OGX, and an Ollama on this machine (or on
// ollama.com), whose name already says where it runs.
func EndpointHost(p Provider) string {
	if p == nil {
		return ""
	}
	withURL, ok := p.(interface{ BaseURL() string })
	if !ok {
		return ""
	}
	base := withURL.BaseURL()
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	switch p.ID() {
	case "ogx":
		return ""
	case "ollama":
		if !isCloudURL(base) || host == "ollama.com" {
			return ""
		}
		return host
	}
	if host == defaultHosts[p.ID()] {
		return ""
	}
	return host
}

// EndpointHost is EndpointHost for the provider registered as providerID.
func (r *Registry) EndpointHost(providerID string) string {
	if r == nil || providerID == "" {
		return ""
	}
	return EndpointHost(r.Get(providerID))
}
