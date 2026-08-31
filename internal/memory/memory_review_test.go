package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// scriptedChat replays canned responses in order, optionally failing on a
// given round, so tests can drive the placement/recall parsers directly.
type scriptedChat struct {
	replies  []string
	failFrom int // 1-based round that starts failing; 0 = never
	calls    int
}

func (c *scriptedChat) Chat(_ context.Context, _, _ string) (string, error) {
	c.calls++
	if c.failFrom > 0 && c.calls >= c.failFrom {
		return "", fmt.Errorf("synthetic provider outage")
	}
	if c.calls-1 < len(c.replies) {
		return c.replies[c.calls-1], nil
	}
	return c.replies[len(c.replies)-1], nil
}

// A fact's content is the whole turn trace — every tool call and 500 chars of
// each output, averaging ~20 KB — which both overflows the embedder's window
// and averages away to noise. The summary is what should represent it.
func TestFactEmbedTextPrefersSummary(t *testing.T) {
	trace := strings.Repeat("Tool: bash (completed)\n  Output: ...\n", 500)

	got := factEmbedText("why does auth fail?", "The JWT clock skew was 30s.", trace)
	if !strings.Contains(got, "why does auth fail?") {
		t.Error("question must always be included: it is what a recall query resembles")
	}
	if !strings.Contains(got, "clock skew") {
		t.Error("summary must be used as the body")
	}
	if strings.Contains(got, "Tool: bash") {
		t.Error("raw turn trace must not be embedded when a summary exists")
	}

	// No summary (no synthesis LLM available) falls back to content.
	if got := factEmbedText("q", "", "the raw content"); !strings.Contains(got, "the raw content") {
		t.Errorf("fallback to content failed: %q", got)
	}
	// A whitespace-only summary is not a summary.
	if got := factEmbedText("q", "   \n ", "the raw content"); !strings.Contains(got, "the raw content") {
		t.Errorf("blank summary should fall back to content: %q", got)
	}
}

// The prompts ask for bare "TOPIC:" lines, but models dress them up. A strict
// HasPrefix check dropped every decorated variant and left the caller holding
// zero values — which is how blank concept keys reached the store.
func TestParseFieldToleratesDecoratedMarkers(t *testing.T) {
	for _, line := range []string{
		"TOPIC: Auth System",
		"1. TOPIC: Auth System",
		"2) TOPIC: Auth System",
		"- TOPIC: Auth System",
		"**TOPIC:** Auth System",
		"  ### TOPIC: Auth System",
		"* **TOPIC**: Auth System",
	} {
		key, value, ok := parseField(line)
		if !ok || key != "TOPIC" || value != "Auth System" {
			t.Errorf("parseField(%q) = (%q, %q, %v), want (TOPIC, Auth System, true)", line, key, value, ok)
		}
	}
	if _, _, ok := parseField("just some prose about topics"); ok {
		t.Error("a line with no field must not parse as one")
	}
}

// A blank concept key is still written to the store as a node, producing a
// nameless concept in every tree render. 39 of these existed in a live graph.
func TestInferPlacementNeverReturnsBlankConcept(t *testing.T) {
	g := newTestGraph(t)
	content := "the agent rewrote the search backend to use uTLS"

	cases := []struct {
		name string
		chat ChatClient
	}{
		{"chat unavailable", nil},
		{"chat errors", &scriptedChat{failFrom: 1}},
		{"response is unparseable", &scriptedChat{replies: []string{"I'm not sure how to categorise this."}}},
		{"response omits CONCEPT", &scriptedChat{replies: []string{"TOPIC: Search\nRELATED: none"}}},
		{"CONCEPT is empty", &scriptedChat{replies: []string{"TOPIC: Search\nCONCEPT:\nRELATED: none"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _, err := g.inferPlacement(context.Background(), GraphOptions{SessionID: "s1"}, nil, nil, nil, content, tc.chat)
			if err != nil {
				t.Fatalf("inferPlacement: %v", err)
			}
			if strings.TrimSpace(p.Topic) == "" {
				t.Error("topic must never be blank")
			}
			if strings.TrimSpace(p.Concept) == "" {
				t.Fatalf("concept is blank; it would be stored as a nameless node")
			}
		})
	}
}

// Decorated markers and multi-line summaries both used to be dropped silently,
// leaving the fact with no summary — and therefore nothing good to embed.
func TestInferLabelsAndSummaryParsing(t *testing.T) {
	g := newTestGraph(t)
	chat := &scriptedChat{replies: []string{
		"1. **LABELS:** auth, JWT, Security\n" +
			"2. **SUMMARY:** The token expiry was wrong.\n" +
			"It was set to 30 seconds instead of 30 minutes.\n" +
			"The fix landed in config.go.",
	}}
	labels, summary := g.inferLabelsAndSummary(context.Background(), "q", "a", "Auth", chat)

	want := []string{"auth", "jwt", "security"}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	for i, l := range labels {
		if l != want[i] {
			t.Errorf("labels[%d] = %q, want %q (labels are lowercased)", i, l, want[i])
		}
	}
	for _, frag := range []string{"token expiry", "30 minutes", "config.go"} {
		if !strings.Contains(summary, frag) {
			t.Errorf("summary lost %q; multi-line summaries must be captured whole\ngot: %q", frag, summary)
		}
	}
}

// AddFact must not persist a nameless concept even when placement degrades.
func TestAddFactSkipsNamelessConcept(t *testing.T) {
	g := newTestGraph(t)
	_, err := g.AddFact(context.Background(), GraphOptions{
		SessionID: "s1",
		ProjectID: "/proj",
		Question:  "why is search failing?",
		Response:  "MATCH because the TLS fingerprint was rejected",
		Chat:      &scriptedChat{failFrom: 1}, // placement inference unavailable
	})
	if err != nil {
		t.Fatalf("AddFact: %v", err)
	}
	concepts, err := g.Store.ListNodes("s1", TypeConcept)
	if err != nil {
		t.Fatalf("list concepts: %v", err)
	}
	for _, c := range concepts {
		if strings.TrimSpace(c.Key) == "" {
			t.Fatal("a nameless concept node was stored")
		}
	}
}

// AddFact must embed the fact even when the synthesis LLM is unavailable:
// an unembedded fact is invisible to every semantic recall.
func TestAddFactAlwaysEmbeds(t *testing.T) {
	g := newTestGraph(t)
	n, err := g.AddFact(context.Background(), GraphOptions{
		SessionID: "s1",
		Question:  "q",
		Response:  "MATCH the answer",
	})
	if err != nil {
		t.Fatalf("AddFact: %v", err)
	}
	embs, err := g.Store.Embeddings("s1")
	if err != nil {
		t.Fatalf("load embeddings: %v", err)
	}
	if len(embs[n.Key]) == 0 {
		t.Fatal("fact stored without an embedding; it can never be recalled semantically")
	}
}

// conceptMap used to hold *ConceptTree pointers into a slice that later appends
// reallocate. Once a topic gained a second concept, writes through the stale
// pointer went to the abandoned array and the facts vanished.
func TestBuildTreeFromNodesSurvivesSliceGrowth(t *testing.T) {
	// Interleaved so the fact's "-facts" concept is registered before the
	// concept nodes that grow the same topic's slice past its capacity.
	nodes := []Node{
		{Type: TypeTopic, Key: "Auth", TopicName: "Auth"},
		{Type: TypeFact, Key: "f1", Content: "first fact", TopicName: "Auth"},
		{Type: TypeConcept, Key: "JWT tokens", TopicName: "Auth"},
		{Type: TypeConcept, Key: "Session cookies", TopicName: "Auth"},
		{Type: TypeConcept, Key: "Refresh flow", TopicName: "Auth"},
		{Type: TypeFact, Key: "f2", Content: "second fact", TopicName: "Auth"},
		{Type: TypeFact, Key: "f3", Content: "third fact", TopicName: "Auth"},
	}
	edges := []Edge{{FromKey: "JWT tokens", ToKey: "Refresh flow", RelType: "related"}}

	tree := buildTreeFromNodes(nodes, edges)
	auth, ok := tree["Auth"]
	if !ok {
		t.Fatal("topic Auth missing from tree")
	}

	var facts, related int
	for _, ct := range auth.Concepts {
		facts += len(ct.Facts)
		related += len(ct.RelatedConcepts)
	}
	if facts != 3 {
		t.Errorf("tree holds %d facts, want 3 — appends to the topic's concept slice dropped writes", facts)
	}
	if related != 1 {
		t.Errorf("tree holds %d related links, want 1 — written through a stale pointer", related)
	}
}

// Ranging a Go map reshuffles topics on every call, so two identical recalls
// built different prompts.
func TestTreeTextOrderingIsDeterministic(t *testing.T) {
	tree := map[string]TopicTree{}
	for _, name := range []string{"Zeta", "Alpha", "Mu", "Beta", "Omega", "Delta", "Kappa"} {
		tree[name] = TopicTree{Name: name, Concepts: []ConceptTree{{
			Name:  name + "-facts",
			Facts: []Node{{Key: name, Content: name + " content", Order: 1}},
		}}}
	}
	first := skeletonTreeText(tree)
	for i := 0; i < 20; i++ {
		if got := skeletonTreeText(tree); got != first {
			t.Fatal("skeletonTreeText output changed between identical calls")
		}
		if got := LightweightTreeAsText(tree, nil); got != LightweightTreeAsText(tree, nil) {
			t.Fatal("lightweightTreeAsText output changed between identical calls")
		}
	}
	if strings.Index(first, "Alpha") > strings.Index(first, "Zeta") {
		t.Error("topics should render in a stable sorted order")
	}
}

// A later refinement round only polishes an answer that already exists.
// Failing the whole recall discarded it, and the tool then reported "no
// relevant past context found" — indistinguishable from a genuine miss.
func TestRecallKeepsAnswerWhenRefinementFails(t *testing.T) {
	g := newTestGraph(t)
	addFact(t, g, "/proj", "s1", "build", "Search", "MATCH the TLS fingerprint was rejected", 0)

	round1 := `{"context_found":true,"final_context":"• The TLS fingerprint was rejected",` +
		`"refinement_needed":true,"confidence":0.4,"facts_used":1}`

	res, err := g.Recall(context.Background(), RecallOptions{
		SessionID: "s1",
		Question:  "MATCH why did search fail?",
		Chat:      &scriptedChat{replies: []string{round1}, failFrom: 2},
	})
	if err != nil {
		t.Fatalf("Recall returned an error instead of the answer it already had: %v", err)
	}
	if !strings.Contains(res.Answer, "TLS fingerprint") {
		t.Fatalf("round 1's answer was discarded, got %q", res.Answer)
	}
}

// Recall with nothing usable must still report the failure, not a silent miss.
func TestRecallStillErrorsWhenFirstRoundFails(t *testing.T) {
	g := newTestGraph(t)
	addFact(t, g, "/proj", "s1", "build", "Search", "MATCH something", 0)

	if _, err := g.Recall(context.Background(), RecallOptions{
		SessionID: "s1",
		Question:  "MATCH anything?",
		Chat:      &scriptedChat{failFrom: 1},
	}); err == nil {
		t.Fatal("a first-round failure has no answer to fall back on and must surface as an error")
	}
}

// emptyModelProvider stands in for a provider whose model list came back empty
// (a fetch failure, or a curated list that filtered everything out).
type emptyModelProvider struct{ provider.Provider }

func (emptyModelProvider) ID() string                   { return "empty" }
func (emptyModelProvider) Models() []provider.ModelInfo { return nil }

// chatClient indexed Models()[0] unguarded. AddFact runs in a bare goroutine,
// so that panic would have taken the whole server down rather than one write.
func TestChatClientWithNoModelsErrorsRatherThanPanics(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked instead of returning an error: %v", r)
		}
	}()
	c := NewChatClient(emptyModelProvider{}, "")
	if _, err := c.Chat(context.Background(), "", "hello"); err == nil {
		t.Fatal("expected an error when the provider has no models")
	}
}

// WriteMemory's goroutine is detached from any request; an unrecovered panic
// there ends the process.
func TestWriteMemoryContainsPanics(t *testing.T) {
	g := newTestGraph(t)
	m := &Memory{Store: g.Store, Graph: g, enabled: true}

	done := make(chan struct{})
	m.WriteMemory(context.Background(), Scope{SessionID: "s1"}, "q", "a", panicChat{done})
	<-done // the panic happened; if it were unrecovered the process would be gone
}

type panicChat struct{ done chan struct{} }

func (p panicChat) Chat(context.Context, string, string) (string, error) {
	close(p.done)
	panic("synthetic panic inside memory synthesis")
}

// Nameless concept rows from the old placement fallback are cleaned up on open.
func TestMigrationRemovesNamelessConcepts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.AddNode(Node{
		SessionID: "s1", Type: TypeConcept, Key: "", TopicName: "General",
	}); err != nil {
		t.Fatalf("seed nameless concept: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	concepts, err := reopened.ListNodes("s1", TypeConcept)
	if err != nil {
		t.Fatalf("list concepts: %v", err)
	}
	for _, c := range concepts {
		if strings.TrimSpace(c.Key) == "" {
			t.Fatal("nameless concept survived migration")
		}
	}
}

// Facts stored without an embedding are skipped by every semantic scan, so a
// graph full of them searches as if it were empty. Backfill must fill exactly
// those and leave existing vectors alone.
func TestBackfillEmbeddingsFillsOnlyMissing(t *testing.T) {
	g := newTestGraph(t)
	embedded := addFact(t, g, "/proj", "s1", "build", "Auth", "MATCH already embedded", 0)

	// A fact written the way the broken embedder left them: stored, unembedded.
	bare, err := g.Store.AddNode(Node{
		SessionID: "s1", ProjectID: "/proj", SessionType: "build", Type: TypeFact,
		Key: "bare", Content: "MATCH never embedded", Question: "q", TopicName: "Auth",
	})
	if err != nil {
		t.Fatalf("seed bare fact: %v", err)
	}

	before, err := g.Store.Embeddings("s1")
	if err != nil {
		t.Fatalf("load embeddings: %v", err)
	}
	if len(before[bare.Key]) != 0 {
		t.Fatal("fixture is wrong: the bare fact should start unembedded")
	}

	m := &Memory{Store: g.Store, Graph: g, enabled: true}
	got, failed, err := m.BackfillEmbeddings(context.Background(), nil)
	if err != nil {
		t.Fatalf("BackfillEmbeddings: %v", err)
	}
	if got != 1 || failed != 0 {
		t.Fatalf("backfilled %d facts (%d failed), want exactly 1 and 0", got, failed)
	}

	after, err := g.Store.Embeddings("s1")
	if err != nil {
		t.Fatalf("load embeddings: %v", err)
	}
	if len(after[bare.Key]) == 0 {
		t.Error("the unembedded fact still has no vector")
	}
	if len(after[embedded.Key]) == 0 {
		t.Error("backfill dropped an existing embedding")
	}

	// Idempotent: a second run has nothing left to do.
	if got, _, err := m.BackfillEmbeddings(context.Background(), nil); err != nil || got != 0 {
		t.Fatalf("second run embedded %d facts (err=%v), want 0 — backfill must be a no-op once clear", got, err)
	}
}

// A fact's raw content is the agent's turn trace, and its scaffolding is
// near-identical across every fact. Embedding that put every vector in the same
// region: on a real graph every fact scored 0.80-0.82 against any question.
// Only the prose distinguishes one turn from another.
func TestTraceProseStripsToolScaffolding(t *testing.T) {
	trace := `--- Assistant iteration ---
Text: The TLS fingerprint was rejected by Brave.
It only accepts a real browser handshake.
Tool: bash (completed)
  Input: {"command":"curl -s https://search.brave.com"}
  Output: <!doctype html><html>challenge page...
Tool: read (error)
  Error: no such file
--- Assistant iteration ---
Text: Switching the transport to uTLS fixed it.`

	got := traceProse(trace)
	for _, want := range []string{
		"TLS fingerprint was rejected",
		"real browser handshake", // a continuation line of the same Text block
		"Switching the transport to uTLS",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prose lost %q\ngot: %q", want, got)
		}
	}
	for _, unwanted := range []string{"Tool:", "Input:", "Output:", "Error:", "Assistant iteration", "curl", "doctype"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("scaffolding %q survived into the embedded text\ngot: %q", unwanted, got)
		}
	}
}

// The fallback chain: summary, then trace prose, then raw content.
func TestFactEmbedTextFallbackChain(t *testing.T) {
	trace := "--- Assistant iteration ---\nText: the prose\nTool: bash (completed)\n  Output: noise noise noise"

	if got := factEmbedText("q", "the summary", trace); !strings.Contains(got, "the summary") || strings.Contains(got, "the prose") {
		t.Errorf("summary must win when present: %q", got)
	}
	got := factEmbedText("q", "", trace)
	if !strings.Contains(got, "the prose") {
		t.Errorf("trace prose must be used when there is no summary: %q", got)
	}
	if strings.Contains(got, "noise") {
		t.Errorf("tool output must not reach the embedder: %q", got)
	}
	// Content with no recoverable prose falls all the way back to raw content.
	if got := factEmbedText("q", "", "just some plain text"); !strings.Contains(got, "just some plain text") {
		t.Errorf("raw content fallback failed: %q", got)
	}
}
