package skill

import (
	"reflect"
	"testing"
)

// These rules arrive as a JSON object and Go randomizes map iteration, so
// "first match wins" would give one config different meanings on different
// runs. Specificity is the ordering the data can support: an exact name beats
// any glob, a glob with more literal characters beats one with fewer, and "*"
// is always last.
func TestRules_MostSpecificPatternWins(t *testing.T) {
	rules := Rules{
		"*":            "allow",
		"internal-*":   "deny",
		"internal-doc": "ask",
	}
	cases := map[string]Action{
		"internal-doc":    Ask,   // exact beats the glob that also matches
		"internal-deploy": Deny,  // the glob beats the catch-all
		"git-release":     Allow, // only the catch-all matches
	}
	for name, want := range cases {
		// Repeated so a specificity bug that hides behind one lucky map
		// iteration order shows up.
		for i := 0; i < 50; i++ {
			if got := rules.Evaluate(name); got != want {
				t.Fatalf("Evaluate(%q) = %q, want %q", name, got, want)
			}
		}
	}
}

// Skills are files the user put in their own project. The permission layer
// carves exceptions out of that; it does not gate everything behind a rule.
func TestRules_UnmatchedNameIsAllowed(t *testing.T) {
	if got := (Rules{"other-*": "deny"}).Evaluate("git-release"); got != Allow {
		t.Errorf("Evaluate = %q, want allow", got)
	}
	if got := Rules(nil).Evaluate("git-release"); got != Allow {
		t.Errorf("nil rules: Evaluate = %q, want allow", got)
	}
}

// A typo in a rule written to restrict something must not quietly widen access
// back to allow.
func TestRules_UnknownActionFallsBackToAsk(t *testing.T) {
	rules := Rules{"deploy-prod": "DENYY"}
	if got := rules.Evaluate("deploy-prod"); got != Ask {
		t.Errorf("Evaluate = %q, want ask", got)
	}
	if got := rules.Invalid(); !reflect.DeepEqual(got, []string{"deploy-prod"}) {
		t.Errorf("Invalid() = %v, want [deploy-prod] so the user can be told", got)
	}
}

func TestRules_ActionsAreCaseAndSpaceInsensitive(t *testing.T) {
	rules := Rules{"a": " Allow ", "b": "DENY", "c": "Ask"}
	if got := rules.Evaluate("a"); got != Allow {
		t.Errorf("a = %q", got)
	}
	if got := rules.Evaluate("b"); got != Deny {
		t.Errorf("b = %q", got)
	}
	if got := rules.Evaluate("c"); got != Ask {
		t.Errorf("c = %q", got)
	}
	if got := rules.Invalid(); len(got) != 0 {
		t.Errorf("Invalid() = %v, want none", got)
	}
}

// A pattern that is not a valid glob matches nothing at all, so a deny written
// that way protects nothing. It has to be reported, not silently ignored.
func TestRules_MalformedPatternIsReported(t *testing.T) {
	rules := Rules{"internal-[": "deny", "git-release": "allow"}
	if got := rules.Invalid(); !reflect.DeepEqual(got, []string{"internal-["}) {
		t.Errorf("Invalid() = %v, want [internal-[]", got)
	}
	// The rules around it still work.
	if got := rules.Evaluate("git-release"); got != Allow {
		t.Errorf("git-release = %q, want allow", got)
	}
}
