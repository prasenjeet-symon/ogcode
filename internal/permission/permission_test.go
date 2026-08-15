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
