package cli

import (
	"context"
	"math"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// priced returns what a catalogued model costs on the Claude API, so these
// tests follow the catalogue's prices instead of pinning numbers that change
// with it.
func priced(t *testing.T, id string) *provider.ModelPrice {
	t.Helper()
	price, ok := provider.NewRegistry().PriceOn("anthropic", id)
	if !ok || price.Input == 0 || price.Output == 0 {
		t.Fatalf("%s: want a priced catalogue entry", id)
	}
	return &price
}

func TestEstimateCost(t *testing.T) {
	price := priced(t, "claude-opus-4-7")
	tokens := runTokens{Input: 1_000_000, Output: 1_000_000}
	got := estimateCost(tokens, price)
	if got == nil {
		t.Fatal("want a price for a catalogued model, got nil")
	}
	if want := price.Input + price.Output; math.Abs(*got-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", *got, want)
	}
}

// Cache traffic is priced at the published cache prices, or at the standard
// multiples of the input price when a model publishes none.
func TestEstimateCostPricesCacheTraffic(t *testing.T) {
	tokens := runTokens{CacheWrite: 1_000_000, CacheRead: 1_000_000}
	for name, price := range map[string]*provider.ModelPrice{
		"published": priced(t, "claude-opus-4-7"),
		"derived":   {Input: 2, Output: 8},
	} {
		read, write := price.CacheRead, price.CacheWrite
		if read == 0 {
			read = price.Input * cacheReadMultiplier
		}
		if write == 0 {
			write = price.Input * cacheWriteMultiplier
		}
		got := estimateCost(tokens, price)
		if got == nil {
			t.Fatalf("%s: want a price, got nil", name)
		}
		if want := read + write; math.Abs(*got-want) > 1e-9 {
			t.Errorf("%s: cost = %v, want %v", name, *got, want)
		}
	}
}

func TestEstimateCostReasoningIsNotBilledTwice(t *testing.T) {
	// Providers count reasoning inside the output total, so carrying it in the
	// breakdown must not move the price.
	price := priced(t, "claude-opus-4-7")
	base := runTokens{Output: 1_000_000}
	withReasoning := runTokens{Output: 1_000_000, Reasoning: 400_000}
	a, b := estimateCost(base, price), estimateCost(withReasoning, price)
	if a == nil || b == nil {
		t.Fatal("want prices, got nil")
	}
	if *a != *b {
		t.Errorf("reasoning tokens changed the price: %v vs %v", *a, *b)
	}
}

func TestEstimateCostUnknownIsNilNotZero(t *testing.T) {
	if got := estimateCost(runTokens{Input: 1_000_000, Output: 1_000_000}, nil); got != nil {
		t.Errorf("want nil for an unpriced run, got %v", *got)
	}
}

// fixedProvider serves a fixed model list under a given provider id.
type fixedProvider struct {
	id     string
	models []provider.ModelInfo
}

func (p fixedProvider) ID() string                   { return p.id }
func (p fixedProvider) Models() []provider.ModelInfo { return p.models }
func (p fixedProvider) StreamChat(context.Context, provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	return nil, nil
}

// The run is priced where its model runs: a host's spelling of a catalogued
// model finds the catalogue's price, a local model costs nothing to quote, and
// a model with no --model or no price anywhere has no cost rather than $0.
func TestRunPriceFollowsTheServingProvider(t *testing.T) {
	want := priced(t, "claude-opus-4-7")
	reg := provider.NewRegistry()
	reg.Register(fixedProvider{id: "anthropic", models: []provider.ModelInfo{{ID: "claude-opus-4-7", InputPricePerM: want.Input, OutputPricePerM: want.Output}}})
	reg.Register(fixedProvider{id: "ollama", models: []provider.ModelInfo{{ID: "gpt-oss:20b"}}})

	if got := runPrice(reg, "claude-opus-4-7"); got == nil || *got != *want {
		t.Errorf("catalogued model = %+v, want %+v", got, want)
	}
	reg.RegisterCustomModel("anthropic/claude-opus-4.7", "anthropic")
	if got := runPrice(reg, "anthropic/claude-opus-4.7"); got == nil || *got != *want {
		t.Errorf("host alias = %+v, want %+v", got, want)
	}
	for _, id := range []string{"", "gpt-oss:20b", "some-openrouter/model-we-do-not-price"} {
		if got := runPrice(reg, id); got != nil {
			t.Errorf("%q: want no price, got %+v", id, *got)
		}
	}
}

func TestCollectUsageSumsAssistantTurnsOnly(t *testing.T) {
	database, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store := session.NewStore(database)

	sess := &session.Session{
		ID:        session.NewSessionID(),
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	add := func(role session.MessageRole, tokens *session.TokenCounts, finish string) {
		msg := &session.MessageInfo{
			ID:        session.NewMessageID(),
			SessionID: sess.ID,
			Role:      role,
			Tokens:    tokens,
			CreatedAt: session.Now(),
		}
		if finish != "" {
			msg.Finish = &finish
		}
		if err := store.CreateMessage(msg); err != nil {
			t.Fatalf("create message: %v", err)
		}
		if err := store.UpdateMessage(msg); err != nil {
			t.Fatalf("update message: %v", err)
		}
	}

	// A user turn carries no usage and must not count as a turn.
	add(session.RoleUser, nil, "")
	add(session.RoleAssistant, &session.TokenCounts{Input: 10, Output: 5, CacheRead: 100, Total: 115}, "tool_calls")
	// A turn whose usage the provider never reported must not crash the sum.
	add(session.RoleAssistant, nil, "")
	add(session.RoleAssistant, &session.TokenCounts{Input: 20, Output: 7, CacheWrite: 50, Total: 77}, "stop")

	tokens, turns, finish := collectUsage(store, sess.ID)
	if turns != 3 {
		t.Errorf("turns = %d, want 3", turns)
	}
	if finish != "stop" {
		t.Errorf("finish = %q, want the last turn's reason %q", finish, "stop")
	}
	// Total is every token consumed — cache reads and writes included — the same
	// figure providers report as total_tokens (see TokenCounts.Consumed).
	// Effective is Total without the 100 cache reads (see TokenCounts.Effective).
	want := runTokens{Input: 30, Output: 12, CacheRead: 100, CacheWrite: 50, Effective: 92, Total: 192}
	if tokens != want {
		t.Errorf("tokens = %+v, want %+v", tokens, want)
	}
}

// Utility calls (title, risk check, compaction) record their tokens on the
// session row, not on a message, so collectUsage must fold them in or a run
// that compacted repeatedly under-reports what it spent.
func TestCollectUsageIncludesUtilityTokens(t *testing.T) {
	database, err := db.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store := session.NewStore(database)

	sess := &session.Session{
		ID:        session.NewSessionID(),
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	msg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: sess.ID,
		Role:      session.RoleAssistant,
		Tokens:    &session.TokenCounts{Input: 10, Output: 5, Total: 15},
		CreatedAt: session.Now(),
	}
	if err := store.CreateMessage(msg); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if err := store.UpdateMessage(msg); err != nil {
		t.Fatalf("update message: %v", err)
	}

	if err := store.AddUtilityUsage(sess.ID, session.TokenCounts{Input: 100, Output: 20, CacheRead: 5}); err != nil {
		t.Fatalf("add utility: %v", err)
	}

	tokens, _, _ := collectUsage(store, sess.ID)
	// Components are the sum of the message and the utility call; Utility reports
	// just the utility subtotal, counted the same way as every other total (cache
	// read included). Effective takes in the utility call too, minus its cache read.
	want := runTokens{Input: 110, Output: 25, CacheRead: 5, Utility: 125, Effective: 135, Total: 140}
	if tokens != want {
		t.Errorf("tokens = %+v, want %+v", tokens, want)
	}
}
