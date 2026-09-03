package cli

import (
	"math"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func TestEstimateCost(t *testing.T) {
	// claude-opus-4-7 is $15/M in, $75/M out.
	tokens := runTokens{Input: 1_000_000, Output: 1_000_000}
	got := estimateCost(tokens, "claude-opus-4-7")
	if got == nil {
		t.Fatal("want a price for a catalogued model, got nil")
	}
	if want := 90.0; math.Abs(*got-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", *got, want)
	}
}

func TestEstimateCostAppliesCacheMultipliers(t *testing.T) {
	// Cache traffic only: 1M written at 1.25x and 1M read at 0.1x the $15
	// input price = 18.75 + 1.50.
	tokens := runTokens{CacheWrite: 1_000_000, CacheRead: 1_000_000}
	got := estimateCost(tokens, "claude-opus-4-7")
	if got == nil {
		t.Fatal("want a price, got nil")
	}
	if want := 20.25; math.Abs(*got-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", *got, want)
	}
}

func TestEstimateCostReasoningIsNotBilledTwice(t *testing.T) {
	// Providers count reasoning inside the output total, so carrying it in the
	// breakdown must not move the price.
	base := runTokens{Output: 1_000_000}
	withReasoning := runTokens{Output: 1_000_000, Reasoning: 400_000}
	a, b := estimateCost(base, "claude-opus-4-7"), estimateCost(withReasoning, "claude-opus-4-7")
	if a == nil || b == nil {
		t.Fatal("want prices, got nil")
	}
	if *a != *b {
		t.Errorf("reasoning tokens changed the price: %v vs %v", *a, *b)
	}
}

func TestEstimateCostUnknownIsNilNotZero(t *testing.T) {
	tokens := runTokens{Input: 1_000_000, Output: 1_000_000}
	for _, model := range []string{"", "some-openrouter/model-we-do-not-price"} {
		if got := estimateCost(tokens, model); got != nil {
			t.Errorf("model %q: want nil for an unpriced model, got %v", model, *got)
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
	want := runTokens{Input: 30, Output: 12, CacheRead: 100, CacheWrite: 50, Total: 192}
	if tokens != want {
		t.Errorf("tokens = %+v, want %+v", tokens, want)
	}
}
