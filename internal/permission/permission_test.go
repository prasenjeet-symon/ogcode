package permission

import "testing"

func TestDefaultRulesetGatesMutatorsAllowsRest(t *testing.T) {
	rs := DefaultRuleset()
	cases := map[string]Action{
		"read":  Allow,
		"glob":  Allow,
		"grep":  Allow,
		"bash":  Ask,
		"write": Ask,
		"edit":  Ask,
		// Anything not explicitly listed falls through the catch-all → Allow,
		// so unrelated tools never prompt.
		"deep_search":   Allow,
		"memory_recall": Allow,
		"view_image":    Allow,
	}
	for tool, want := range cases {
		if got := rs.Evaluate(tool, "whatever"); got != want {
			t.Errorf("Evaluate(%q) = %q, want %q", tool, got, want)
		}
	}
}

func TestAddRuleAlwaysGrantTakesPrecedence(t *testing.T) {
	m := NewManager()
	const sess = "s1"

	if got := m.Ruleset(sess).Evaluate("bash", "ls"); got != Ask {
		t.Fatalf("bash before grant = %q, want Ask", got)
	}
	m.AddRule(sess, Rule{Permission: "bash", Pattern: "*", Action: Allow})
	if got := m.Ruleset(sess).Evaluate("bash", "ls"); got != Allow {
		t.Fatalf("bash after always-grant = %q, want Allow", got)
	}
	// A different session is unaffected.
	if got := m.Ruleset("s2").Evaluate("bash", "ls"); got != Ask {
		t.Fatalf("bash on other session = %q, want Ask", got)
	}
}

func TestReplyDeliversAndRemoves(t *testing.T) {
	m := NewManager()
	req := Request{ID: NewPermissionID(), SessionID: "s1", Tool: "write"}
	pr := m.Create(req)

	if !m.Reply(req.ID, "once") {
		t.Fatal("Reply returned false for a live request")
	}
	select {
	case got := <-pr.ReplyCh:
		if got != "once" {
			t.Fatalf("ReplyCh = %q, want once", got)
		}
	default:
		t.Fatal("expected a value on ReplyCh")
	}
	// The request is gone now — a second reply must fail, not panic.
	if m.Reply(req.ID, "once") {
		t.Fatal("Reply returned true for an already-answered request")
	}
}

func TestRemoveDiscardsPending(t *testing.T) {
	m := NewManager()
	req := Request{ID: NewPermissionID(), SessionID: "s1", Tool: "bash"}
	m.Create(req)
	m.Remove(req.ID)
	if m.Reply(req.ID, "once") {
		t.Fatal("Reply succeeded after Remove")
	}
}

// The loop calls EnsureRules at the start of every turn, so it has to be
// idempotent — and it must never overwrite a grant the user gave earlier in the
// session.
func TestEnsureRules_SeedsOnceAndPreservesUserGrants(t *testing.T) {
	m := NewManager()
	seed := Ruleset{{Permission: "skill", Pattern: "internal-docs", Action: Deny}}

	m.EnsureRules("s1", seed)
	if got := m.Ruleset("s1").Evaluate("skill", "internal-docs"); got != Deny {
		t.Fatalf("configured rule not applied: %q", got)
	}
	// The trailing catch-all still answers for everything the seed misses.
	if got := m.Ruleset("s1").Evaluate("skill", "git-release"); got != Allow {
		t.Errorf("unmatched skill = %q, want allow", got)
	}

	// A user grant lands ahead of the seeded rules and survives the next turn's
	// EnsureRules call.
	m.AddRule("s1", Rule{Permission: "skill", Pattern: "internal-docs", Action: Allow})
	m.EnsureRules("s1", seed)
	if got := m.Ruleset("s1").Evaluate("skill", "internal-docs"); got != Allow {
		t.Errorf("an always-allow grant was overwritten by a later EnsureRules: %q", got)
	}
}

// A configured "ask" has to be reached before DefaultRuleset's trailing
// catch-all Allow, or it would never prompt.
//
// Patterns here are concrete, not globs: matchGlob resolves only an exact match
// or a bare "*", so a caller with glob-shaped configuration has to expand it to
// the names it covers before seeding — which is what skillPermissionRules in
// the agent package does.
func TestEnsureRules_AskBeatsTheDefaultCatchAll(t *testing.T) {
	m := NewManager()
	m.EnsureRules("s1", Ruleset{{Permission: "skill", Pattern: "deploy-prod", Action: Ask}})
	if got := m.Ruleset("s1").Evaluate("skill", "deploy-prod"); got != Ask {
		t.Errorf("Evaluate = %q, want ask", got)
	}
}

func TestEnsureRules_EmptyRulesLeaveTheSessionOnDefaults(t *testing.T) {
	m := NewManager()
	m.EnsureRules("s1", nil)
	if got := m.Ruleset("s1").Evaluate("bash", "rm -rf /"); got != Ask {
		t.Errorf("bash = %q, want the default ask", got)
	}
}
