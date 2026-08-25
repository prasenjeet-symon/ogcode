package memory

import (
	"context"
	"encoding/json"
	"testing"
)

// TestCosineReturnsZeroOnDimensionMismatch pins the fix for the bug where a
// provider switch left old-dimensionality embeddings producing wrong cosine
// scores. Mismatched vectors must score zero (skip), not a partial overlap.
func TestCosineReturnsZeroOnDimensionMismatch(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0}
	if got := cosine(a, b); got != 0 {
		t.Fatalf("cosine with mismatched dims = %v, want 0 (skip)", got)
	}
	if got := cosine(b, a); got != 0 {
		t.Fatalf("cosine with mismatched dims (reversed) = %v, want 0", got)
	}
	// Same dimensionality must still produce a real score.
	if got := cosine([]float32{1, 0}, []float32{1, 0}); got <= 0.99 {
		t.Fatalf("cosine of identical vectors = %v, want ~1", got)
	}
}

// TestTruncateIsRuneSafe pins the fix for byte-slicing truncation that split
// multi-byte UTF-8 sequences and produced invalid strings.
func TestTruncateIsRuneSafe(t *testing.T) {
	// "é" is 2 bytes. max=1 would split it under the old byte-slice logic.
	got := truncate("ééé", 1)
	if !validUTF8(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	// A multi-byte emoji string truncated mid-character must not split it.
	got = truncate("😀😀😀", 3)
	if !validUTF8(got) {
		t.Fatalf("truncate split an emoji: %q", got)
	}
	// Truncation under a rune boundary still cuts at the last full rune.
	got = truncate("ab😀", 3) // "ab" = 2 bytes, "😀" = 4 bytes → cut before emoji
	if got != "ab..." {
		t.Fatalf("truncate = %q, want %q", got, "ab...")
	}
}

// TestProjectFactsTextTruncationIsRuneSafe pins the fix for the byte-based hard
// truncation in projectFactsText that could split a multi-byte character.
func TestProjectFactsTextTruncationIsRuneSafe(t *testing.T) {
	// Build facts with multi-byte content that, when rendered, exceeds a small
	// budget so the hard-truncation path in projectFactsText is exercised.
	facts := make([]Node, 200)
	for i := range facts {
		facts[i] = Node{Content: "café résumé ☃snowman " + string(rune('A'+i%26))}
	}
	matched := map[string]bool{}
	for i := range facts {
		matched[facts[i].Key] = true
	}
	// A tiny budget forces the over-budget branch (renderProjectFacts cannot
	// get under maxChars with 200 facts at the tightest limits).
	got := projectFactsText(facts, matched, nil, 50)
	if !validUTF8(got) {
		t.Fatalf("projectFactsText produced invalid UTF-8: %q", got[:20])
	}
}

// TestRefreshAllReEmbedsGraphNodes pins the fix where RefreshAll only touched
// memory_document and left graph fact embeddings at the old dimensionality
// after a provider switch, compounding the cosine mismatch bug.
func TestRefreshAllReEmbedsGraphNodes(t *testing.T) {
	g := newTestGraph(t)
	addFact(t, g, "/proj", "s1", "build", "Topic", "MATCH first", 0)

	// Simulate a provider switch: corrupt the stored embedding to an old
	// dimensionality (1536-dim placeholder) that will no longer match the
	// fakeEmbedder's 2-dim vectors.
	var old []float32
	for i := 0; i < 1536; i++ {
		old = append(old, 0.01)
	}
	rows, err := g.Store.DB().Query(`SELECT session_id, key FROM nodes WHERE type = 'fact'`)
	if err != nil {
		t.Fatalf("query nodes: %v", err)
	}
	type nk struct{ session, key string }
	var keys []nk
	for rows.Next() {
		var s, k string
		if err := rows.Scan(&s, &k); err != nil {
			t.Fatalf("scan: %v", err)
		}
		keys = append(keys, nk{s, k})
	}
	rows.Close()
	for _, n := range keys {
		raw, _ := json.Marshal(old)
		if _, err := g.Store.DB().Exec(`UPDATE nodes SET embedding = ? WHERE session_id = ? AND key = ?`, raw, n.session, n.key); err != nil {
			t.Fatalf("corrupt embedding: %v", err)
		}
	}

	// Build a Memory around the graph and run RefreshAll.
	m := &Memory{Store: g.Store, Graph: g}
	if err := m.RefreshAll(context.Background()); err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}

	// Every fact node must now carry a 2-dim embedding from fakeEmbedder.
	embs, err := g.Store.Embeddings("s1")
	if err != nil {
		t.Fatalf("load embeddings: %v", err)
	}
	if len(embs) == 0 {
		t.Fatalf("no graph embeddings after RefreshAll")
	}
	for key, vec := range embs {
		if len(vec) != 2 {
			t.Fatalf("node %q embedding dim = %d after RefreshAll, want 2", key, len(vec))
		}
	}
}

func validUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}
