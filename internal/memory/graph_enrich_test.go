package memory

import "testing"

// BuildLightweightTree returns a nil map for "nothing matched" — both when the
// session has no facts and, less obviously, when it has facts but none clear the
// cosine threshold. Recall stores that nil in semanticTree and then merges a
// follow-up round into it, so the nil map reaches a write and takes the process
// down with it.
func TestEnrichTreeWithFollowUp_NilMainTree(t *testing.T) {
	extra := map[string]TopicTree{
		"auth": {Name: "auth", Concepts: []ConceptTree{{Name: "auth-facts"}}},
	}

	got := enrichTreeWithFollowUp(nil, extra)

	if got == nil {
		t.Fatal("merging into a nil tree returned nil; the follow-up facts were dropped")
	}
	if _, ok := got["auth"]; !ok {
		t.Error("follow-up topic missing from the merged tree")
	}
}

// The nil can also arrive as the follow-up side.
func TestEnrichTreeWithFollowUp_NilExtraTree(t *testing.T) {
	main := map[string]TopicTree{"auth": {Name: "auth"}}
	got := enrichTreeWithFollowUp(main, nil)
	if len(got) != 1 || got["auth"].Name != "auth" {
		t.Errorf("merging a nil follow-up altered the main tree: %#v", got)
	}
}

// Both nil is the empty-session case: no facts anywhere, no panic, nothing to show.
func TestEnrichTreeWithFollowUp_BothNil(t *testing.T) {
	if got := enrichTreeWithFollowUp(nil, nil); len(got) != 0 {
		t.Errorf("expected an empty result, got %#v", got)
	}
}

// Merging must not lose concepts already on a topic.
func TestEnrichTreeWithFollowUp_MergesConcepts(t *testing.T) {
	main := map[string]TopicTree{
		"auth": {Name: "auth", Concepts: []ConceptTree{{Name: "one"}}},
	}
	extra := map[string]TopicTree{
		"auth": {Name: "auth", Concepts: []ConceptTree{{Name: "two"}}},
	}
	got := enrichTreeWithFollowUp(main, extra)
	if n := len(got["auth"].Concepts); n != 2 {
		t.Errorf("expected 2 concepts after merge, got %d", n)
	}
}
