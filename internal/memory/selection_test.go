package memory

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

// scorePool builds a candidate list and its matching distribution the way the
// recall paths do: every scored fact feeds stats, and .score carries raw cosine.
func scorePool(entries ...scoredFact) ([]scoredFact, scoreStats) {
	var stats scoreStats
	pool := make([]scoredFact, len(entries))
	for i, e := range entries {
		stats.add(e.score)
		pool[i] = e
	}
	return pool, stats
}

func fact(key string, score float32, order int) scoredFact {
	return scoredFact{node: Node{Key: key, Order: order}, score: score}
}

func keysOf(sel []scoredFact) []string {
	out := make([]string, len(sel))
	for i, s := range sel {
		out[i] = s.node.Key
	}
	return out
}

// The pool that motivated the whole change: gte-small puts unrelated text at a
// ~0.78 cosine floor, so an absolute gate of 0.1 admitted all 43 facts. The
// adaptive gate has to keep only the three that stand out.
func TestSelectByRelevanceGatesAgainstTheSimilarityFloor(t *testing.T) {
	var entries []scoredFact
	for i := 0; i < 40; i++ {
		entries = append(entries, fact(fmt.Sprintf("noise-%02d", i), 0.76+float32(i%5)*0.01, i+1))
	}
	for i := 0; i < 3; i++ {
		entries = append(entries, fact(fmt.Sprintf("match-%d", i), 0.88+float32(i)*0.01, 41+i))
	}
	pool, stats := scorePool(entries...)

	got := selectByRelevance(pool, stats, defaultRecallLimit, nil)

	if len(got) != 3 {
		t.Fatalf("selected %d facts (%v), want the 3 that clear the floor", len(got), keysOf(got))
	}
	for _, s := range got {
		if !strings.HasPrefix(s.node.Key, "match-") {
			t.Errorf("floor-level fact %q survived the gate", s.node.Key)
		}
	}
}

// The old scoring added 0.15×(order/maxOrder) to raw cosine, but the observed
// spread between a session's best and worst match is only ~0.11 — so the newest
// fact outranked the most relevant one no matter what the question was.
func TestSelectByRelevanceRecencyCannotOutrankSimilarity(t *testing.T) {
	// Two candidates keeps the pool under minPoolForZCut, so this exercises
	// ranking with the gate deliberately out of the way.
	pool, stats := scorePool(
		fact("relevant-but-old", 0.89, 1),
		fact("irrelevant-but-newest", 0.78, 100),
	)

	got := selectByRelevance(pool, stats, defaultRecallLimit, func(n Node) float32 {
		return float32(n.Order) / 100
	})

	if len(got) != 2 {
		t.Fatalf("selected %d facts, want both", len(got))
	}
	if got[0].node.Key != "relevant-but-old" {
		t.Errorf("ranked %q first; recency outranked similarity again", got[0].node.Key)
	}
}

// Recency still has to break ties, or a genuinely ambiguous question returns
// the oldest matches rather than the ones the user is working on now.
func TestSelectByRelevanceRecencyBreaksTies(t *testing.T) {
	pool, stats := scorePool(
		fact("older", 0.85, 1),
		fact("newer", 0.85, 10),
	)

	got := selectByRelevance(pool, stats, defaultRecallLimit, func(n Node) float32 {
		return float32(n.Order) / 10
	})

	if got[0].node.Key != "newer" {
		t.Errorf("ranked %q first, want the newer of two equally similar facts", got[0].node.Key)
	}
}

func TestSelectByRelevanceCapsAtLimit(t *testing.T) {
	var entries []scoredFact
	for i := 0; i < 30; i++ {
		// A rising ramp: the gate keeps the top slice, the cap trims it to size.
		entries = append(entries, fact(fmt.Sprintf("f-%02d", i), 0.5+float32(i)*0.01, i+1))
	}
	pool, stats := scorePool(entries...)

	got := selectByRelevance(pool, stats, 4, nil)

	if len(got) != 4 {
		t.Fatalf("selected %d facts, want the limit of 4", len(got))
	}
	if got[0].node.Key != "f-29" {
		t.Errorf("best match is %q, want f-29", got[0].node.Key)
	}
}

// Below minPoolForZCut a mean and standard deviation describe noise, so the cap
// alone bounds the result.
func TestSelectByRelevanceSmallPoolSkipsTheGate(t *testing.T) {
	pool, stats := scorePool(
		fact("a", 0.95, 1),
		fact("b", 0.78, 2),
		fact("c", 0.77, 3),
	)

	got := selectByRelevance(pool, stats, defaultRecallLimit, nil)

	if len(got) != 3 {
		t.Fatalf("selected %d facts (%v), want all 3 of an ungateable pool", len(got), keysOf(got))
	}
}

// A skewed pool can put mean+σ above every score. That says the spread is odd,
// not that memory is empty — returning nothing would be reported upstream as
// "no relevant past context found", which is a different and wrong claim.
func TestSelectByRelevanceEmptyGateFallsBackToRanking(t *testing.T) {
	pool, stats := scorePool(
		fact("low", 0.10, 1),
		fact("a", 0.90, 2),
		fact("b", 0.90, 3),
		fact("c", 0.90, 4),
		fact("d", 0.90, 5),
	)
	if cut, ok := stats.cut(relevanceZCut); !ok || cut <= 0.90 {
		t.Fatalf("test pool no longer produces an all-rejecting cut (cut=%.3f ok=%v)", cut, ok)
	}

	got := selectByRelevance(pool, stats, defaultRecallLimit, nil)

	if len(got) != 5 {
		t.Fatalf("selected %d facts, want the whole pool ranked rather than an empty recall", len(got))
	}
}

func TestSelectByRelevanceIsDeterministicOnTies(t *testing.T) {
	build := func() ([]scoredFact, scoreStats) {
		return scorePool(
			fact("zebra", 0.80, 5),
			fact("alpha", 0.80, 5),
			fact("mango", 0.80, 5),
		)
	}
	pool, stats := build()
	first := keysOf(selectByRelevance(pool, stats, defaultRecallLimit, nil))
	for i := 0; i < 20; i++ {
		pool, stats = build()
		got := keysOf(selectByRelevance(pool, stats, defaultRecallLimit, nil))
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("tie ordering drifted between calls: %v then %v", first, got)
			}
		}
	}
}

func TestSelectByRelevanceHandlesEmptyInput(t *testing.T) {
	if got := selectByRelevance(nil, scoreStats{}, defaultRecallLimit, nil); got != nil {
		t.Errorf("selectByRelevance(nil) = %v, want nil", got)
	}
	pool, stats := scorePool(fact("a", 0.9, 1))
	if got := selectByRelevance(pool, stats, 0, nil); got != nil {
		t.Errorf("selectByRelevance with limit 0 = %v, want nil", got)
	}
}

func TestScoreStats(t *testing.T) {
	var s scoreStats
	for _, v := range []float32{2, 4, 4, 4, 5, 5, 7, 9} {
		s.add(v)
	}
	if got := s.mean(); math.Abs(got-5) > 1e-9 {
		t.Errorf("mean = %v, want 5", got)
	}
	if got := s.stddev(); math.Abs(got-2) > 1e-9 {
		t.Errorf("stddev = %v, want 2", got)
	}

	// A pool of identical scores has nothing to gate on.
	var flat scoreStats
	for i := 0; i < 10; i++ {
		flat.add(0.78)
	}
	if _, ok := flat.cut(relevanceZCut); ok {
		t.Error("cut reported usable on a zero-variance pool")
	}
	var tiny scoreStats
	tiny.add(0.9)
	if _, ok := tiny.cut(relevanceZCut); ok {
		t.Error("cut reported usable on a single-fact pool")
	}
}

// floorEmbedder reproduces the property that makes an absolute cosine gate
// useless: a normalized sentence embedder puts *unrelated* text at a high
// similarity floor rather than near zero. Measured across ogcode's own store,
// gte-small scores facts from different sessions at a median cosine of 0.78.
//
// Each vector is a shared component of length √floor plus a per-topic residual,
// so two facts on different topics score exactly testFloorCos and two on the
// same topic score 1.0.
type floorEmbedder struct{}

const testFloorCos = 0.78

var testTopicMarkers = []string{"TOPIC_A", "TOPIC_B", "TOPIC_C", "TOPIC_D"}

func floorVec(text string) []float32 {
	shared := float32(math.Sqrt(testFloorCos))
	residual := float32(math.Sqrt(1 - testFloorCos))
	// One residual dimension per marker, plus a final one for unmarked text so
	// it does not silently collide with a topic.
	v := make([]float32, 1+len(testTopicMarkers)+1)
	v[0] = shared
	for i, marker := range testTopicMarkers {
		if strings.Contains(text, marker) {
			v[1+i] = residual
			return v
		}
	}
	v[len(v)-1] = residual
	return v
}

func (floorEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, floorVec(in))
	}
	return out, nil
}

func newFloorGraph(t *testing.T) *Graph {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &Graph{Store: store, Embed: floorEmbedder{}}
}

// End to end: against a realistic similarity floor, the tree handed to the
// synthesis LLM must be a small slice of the session, not all of it.
func TestBuildLightweightTreeSelectsASliceNotTheWholeSession(t *testing.T) {
	g := newFloorGraph(t)
	const total = 30
	var wantKeys []string
	for i := 0; i < total; i++ {
		// Three facts on the queried topic, spread through the session.
		marker := testTopicMarkers[1+i%3]
		if i == 9 || i == 19 || i == 29 {
			marker = testTopicMarkers[0]
		}
		n := addFact(t, g, "/proj", "s1", "build", "Topic "+marker,
			fmt.Sprintf("%s fact %02d", marker, i), 0)
		if marker == testTopicMarkers[0] {
			wantKeys = append(wantKeys, n.Key)
		}
	}

	queryVec := floorVec("TOPIC_A what did we decide")
	_, topFacts, err := g.BuildLightweightTree(context.Background(), "s1", NodeFilter{}, queryVec, defaultRecallLimit)
	if err != nil {
		t.Fatalf("BuildLightweightTree: %v", err)
	}

	// 3 matches, each widened by its N±1 neighbours, is the ceiling.
	if len(topFacts) == 0 || len(topFacts) > 9 {
		t.Fatalf("selected %d of %d facts, want the 3 matches plus their neighbours", len(topFacts), total)
	}
	got := make(map[string]bool, len(topFacts))
	for _, f := range topFacts {
		got[f.Key] = true
	}
	for _, k := range wantKeys {
		if !got[k] {
			t.Errorf("matching fact %q was not selected", k)
		}
	}
}

// The same session with the query vector removed must still return everything —
// ReadMemory renders the whole tree and depends on it.
func TestBuildLightweightTreeUnfilteredStillReturnsEverything(t *testing.T) {
	g := newFloorGraph(t)
	for i := 0; i < 12; i++ {
		addFact(t, g, "/proj", "s1", "build", "Topic", fmt.Sprintf("TOPIC_B fact %02d", i), 0)
	}

	_, allFacts, err := g.BuildLightweightTree(context.Background(), "s1", NodeFilter{}, nil, 0)
	if err != nil {
		t.Fatalf("BuildLightweightTree: %v", err)
	}
	if len(allFacts) != 12 {
		t.Fatalf("unfiltered tree returned %d facts, want all 12", len(allFacts))
	}
}
