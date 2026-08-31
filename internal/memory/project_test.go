package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeEmbedder gives facts a two-dimensional vector chosen by a marker in the
// text, so cosine similarity in tests is exactly 1 or 0 and the assertions are
// about retrieval behaviour rather than embedding quality.
type fakeEmbedder struct{}

func (fakeEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, 0, len(inputs))
	for _, in := range inputs {
		if strings.Contains(in, "MATCH") {
			out = append(out, []float32{1, 0})
		} else {
			out = append(out, []float32{0, 1})
		}
	}
	return out, nil
}

func newTestGraph(t *testing.T) *Graph {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &Graph{Store: store, Embed: fakeEmbedder{}}
}

// addFact inserts a fact directly, bypassing LLM placement.
func addFact(t *testing.T, g *Graph, projectID, sessionID, sessionType, topic, text string, createdAt int64) Node {
	t.Helper()
	if err := g.Store.EnsureSessionMeta(SessionUpsert{
		ID: sessionID, ProjectID: projectID, SessionType: sessionType, Name: "session " + sessionID,
	}); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	n, err := g.Store.AddNode(Node{
		SessionID:   sessionID,
		ProjectID:   projectID,
		SessionType: sessionType,
		Type:        TypeFact,
		Key:         sessionID + "-" + text,
		Content:     text,
		Question:    "q: " + text,
		Response:    text,
		TopicName:   topic,
		Summary:     "summary of " + text,
	})
	if err != nil {
		t.Fatalf("add node: %v", err)
	}
	vecs, _ := g.Embed.Embed(context.Background(), []string{text})
	if err := g.Store.SetEmbedding(sessionID, n.Key, vecs[0]); err != nil {
		t.Fatalf("set embedding: %v", err)
	}
	if createdAt > 0 {
		if _, err := g.Store.DB().Exec(`UPDATE nodes SET created_at = ? WHERE id = ?`, createdAt, n.ID); err != nil {
			t.Fatalf("backdate node: %v", err)
		}
		n.CreatedAt = createdAt
	}
	return *n
}

func TestScanProjectFactsFiltersByProjectAndSessionType(t *testing.T) {
	g := newTestGraph(t)
	addFact(t, g, "/proj/a", "s1", "build", "Auth", "MATCH one", 0)
	addFact(t, g, "/proj/a", "s2", "subagent", "Auth", "MATCH from a subagent", 0)
	addFact(t, g, "/proj/b", "s3", "build", "Auth", "MATCH other project", 0)
	// A row written before the schema tracked session types.
	addFact(t, g, "/proj/a", "s4", "", "Auth", "MATCH legacy", 0)

	var got []string
	err := g.Store.ScanProjectFacts("/proj/a", ProjectFilter{SessionTypes: []string{"build", "plan"}}, func(n Node, _ []float32) {
		got = append(got, n.Content)
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	want := map[string]bool{"MATCH one": true, "MATCH legacy": true}
	if len(got) != len(want) {
		t.Fatalf("scanned %d facts (%v), want %d", len(got), got, len(want))
	}
	for _, c := range got {
		if !want[c] {
			t.Errorf("unexpected fact in project scan: %q", c)
		}
	}
}

func TestScanProjectFactsRespectsSinceBound(t *testing.T) {
	g := newTestGraph(t)
	now := time.Now().UnixMilli()
	old := now - int64(90*24*time.Hour/time.Millisecond)
	addFact(t, g, "/proj", "s1", "build", "Auth", "MATCH recent", now)
	addFact(t, g, "/proj", "s1", "build", "Auth", "MATCH ancient", old)

	var got []string
	cutoff := now - int64(30*24*time.Hour/time.Millisecond)
	if err := g.Store.ScanProjectFacts("/proj", ProjectFilter{Since: cutoff}, func(n Node, _ []float32) {
		got = append(got, n.Content)
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 1 || got[0] != "MATCH recent" {
		t.Fatalf("since-bounded scan = %v, want [MATCH recent]", got)
	}
}

func TestScanProjectCapsFactsPerSession(t *testing.T) {
	g := newTestGraph(t)
	// One session dominates the project with 20 matching facts; two others have
	// one each. Without a per-session cap the loud session takes every slot.
	for i := 0; i < 20; i++ {
		addFact(t, g, "/proj", "loud", "build", "Auth", fmt.Sprintf("MATCH loud %d", i), 0)
	}
	addFact(t, g, "/proj", "quiet1", "build", "Auth", "MATCH quiet one", 0)
	addFact(t, g, "/proj", "quiet2", "build", "Auth", "MATCH quiet two", 0)

	opts := ProjectRecallOptions{ProjectID: "/proj", Question: "MATCH?"}
	opts.applyDefaults()
	opts.PerSessionCap = 3
	opts.Limit = 50

	matches, stats, total, _, err := g.scanProject(opts, []float32{1, 0})
	if err != nil {
		t.Fatalf("scanProject: %v", err)
	}
	if total != 22 {
		t.Fatalf("scanned %d facts, want 22", total)
	}
	perSession := map[string]int{}
	for _, m := range matches {
		perSession[m.node.SessionID]++
	}
	if perSession["loud"] != 3 {
		t.Errorf("loud session contributed %d facts, want the cap of 3", perSession["loud"])
	}
	if perSession["quiet1"] != 1 || perSession["quiet2"] != 1 {
		t.Errorf("quiet sessions were crowded out: %v", perSession)
	}
	if st := stats["Auth"]; st == nil || st.Facts != 22 || len(st.Sessions) != 3 {
		t.Errorf("topic stats = %+v, want 22 facts across 3 sessions", st)
	}
}

func TestScanProjectSkipsUnrelatedFacts(t *testing.T) {
	g := newTestGraph(t)
	addFact(t, g, "/proj", "s1", "build", "Auth", "MATCH relevant", 0)
	addFact(t, g, "/proj", "s1", "build", "Other", "unrelated chatter", 0)

	opts := ProjectRecallOptions{ProjectID: "/proj", Question: "MATCH?"}
	opts.applyDefaults()
	matches, _, total, _, err := g.scanProject(opts, []float32{1, 0})
	if err != nil {
		t.Fatalf("scanProject: %v", err)
	}
	if total != 2 {
		t.Fatalf("scanned %d facts, want 2", total)
	}
	if len(matches) != 1 || matches[0].node.Content != "MATCH relevant" {
		t.Fatalf("matches = %v, want only the relevant fact", matches)
	}
}

func TestRecencyDecayHalvesEveryHalfLife(t *testing.T) {
	now := time.Now().UnixMilli()
	day := int64(24 * time.Hour / time.Millisecond)

	if got := recencyDecay(now, now, 30); got < 0.99 {
		t.Errorf("decay for a fact written now = %v, want ~1", got)
	}
	if got := recencyDecay(now-30*day, now, 30); got < 0.49 || got > 0.51 {
		t.Errorf("decay at one half-life = %v, want ~0.5", got)
	}
	if got := recencyDecay(now-60*day, now, 30); got < 0.24 || got > 0.26 {
		t.Errorf("decay at two half-lives = %v, want ~0.25", got)
	}
	if got := recencyDecay(0, now, 30); got != 0 {
		t.Errorf("decay for an undated fact = %v, want 0", got)
	}
}

func TestRecencyBreaksSimilarityTies(t *testing.T) {
	g := newTestGraph(t)
	now := time.Now().UnixMilli()
	day := int64(24 * time.Hour / time.Millisecond)
	addFact(t, g, "/proj", "old", "build", "Auth", "MATCH the old decision", now-200*day)
	addFact(t, g, "/proj", "new", "build", "Auth", "MATCH the new decision", now-1*day)

	opts := ProjectRecallOptions{ProjectID: "/proj", Question: "MATCH?"}
	opts.applyDefaults()
	matches, _, _, _, err := g.scanProject(opts, []float32{1, 0})
	if err != nil {
		t.Fatalf("scanProject: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2", len(matches))
	}
	// Identical cosine (both exactly 1) — recency must decide the order.
	if matches[0].node.SessionID != "new" {
		t.Errorf("ranked %q first; the more recent fact should win an exact tie", matches[0].node.SessionID)
	}
}

func TestExpandWithNeighboursStaysWithinSession(t *testing.T) {
	g := newTestGraph(t)
	// Two sessions, each with three ordered turns. The match is turn 2 of s1;
	// its neighbours must be s1's turns 1 and 3 — never s2's, whose "order"
	// numbers collide but mean something else entirely.
	for i := 1; i <= 3; i++ {
		addFact(t, g, "/proj", "s1", "build", "Auth", fmt.Sprintf("s1 turn %d", i), 0)
		addFact(t, g, "/proj", "s2", "build", "Auth", fmt.Sprintf("s2 turn %d", i), 0)
	}
	target, err := g.Store.GetNodeAt("s1", 2)
	if err != nil {
		t.Fatalf("get node at: %v", err)
	}

	facts, sessions, err := g.expandWithNeighbours([]scoredFact{{node: *target, score: 1}}, ProjectRecallOptions{ProjectID: "/proj"})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if sessions != 1 {
		t.Errorf("windowing touched %d sessions, want 1", sessions)
	}
	if len(facts) != 3 {
		t.Fatalf("got %d facts, want the match plus its two neighbours: %v", len(facts), facts)
	}
	for _, f := range facts {
		if f.SessionID != "s1" {
			t.Errorf("neighbour windowing leaked across sessions: %q", f.SessionID)
		}
	}
}

func TestProjectFactsTextAttributesAndOrders(t *testing.T) {
	day := int64(24 * time.Hour / time.Millisecond)
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	facts := []Node{
		{SessionID: "newer", Key: "b", Order: 1, CreatedAt: base + 60*day, Question: "why sqlite", Response: "because it is embedded", Summary: "sqlite choice"},
		{SessionID: "older", Key: "a", Order: 1, CreatedAt: base, Question: "why postgres", Response: "because it scales", Summary: "postgres choice"},
	}
	matched := map[string]bool{"a": true, "b": true}
	names := map[string]string{"older": "First conversation", "newer": "Later conversation"}

	out := projectFactsText(facts, matched, names, defaultProjectMaxChars)

	firstIdx := strings.Index(out, "First conversation")
	laterIdx := strings.Index(out, "Later conversation")
	if firstIdx < 0 || laterIdx < 0 {
		t.Fatalf("output is missing conversation attribution:\n%s", out)
	}
	if firstIdx > laterIdx {
		t.Errorf("conversations are not oldest-first:\n%s", out)
	}
	if !strings.Contains(out, "2026-05-01") || !strings.Contains(out, "2026-06-30") {
		t.Errorf("output is missing fact dates:\n%s", out)
	}
	if !strings.Contains(out, "★") {
		t.Errorf("semantic matches are not marked:\n%s", out)
	}
}

func TestProjectFactsTextDropsNeighboursBeforeMatches(t *testing.T) {
	// Enough neighbours that even their one-line summaries blow the budget, so
	// the only way to fit is to drop context turns and keep the matches.
	var facts []Node
	for i := 0; i < 40; i++ {
		facts = append(facts, Node{
			SessionID: "s1", Key: fmt.Sprintf("k%d", i), Order: i + 1,
			CreatedAt: time.Now().UnixMilli(),
			Response:  strings.Repeat("verbose tool trace ", 200),
			Summary:   fmt.Sprintf("summary %d ", i) + strings.Repeat("padding ", 30),
		})
	}
	facts[0].Response = "DECISION we picked the first option"
	facts[1].Response = "DECISION we later reversed it"
	matched := map[string]bool{"k0": true, "k1": true}

	const budget = 3000
	out := projectFactsText(facts, matched, map[string]string{}, budget)

	if len(out) > budget {
		t.Errorf("fact block is %d chars, over the %d budget", len(out), budget)
	}
	if !strings.Contains(out, "DECISION we picked the first option") ||
		!strings.Contains(out, "DECISION we later reversed it") {
		t.Errorf("a matched fact was dropped to fit the budget:\n%s", out)
	}
	if !strings.Contains(out, "omitted to fit the context budget") {
		t.Errorf("dropped context turns were not disclosed:\n%s", out)
	}
}

func TestProjectSkeletonTextRanksAndCapsTopics(t *testing.T) {
	now := time.Now().UnixMilli()
	stats := map[string]*topicStat{
		"Small": {Name: "Small", Facts: 2, First: now, Last: now, Sessions: map[string]bool{"s": true}, Labels: map[string]int{"x": 1}},
		"Big":   {Name: "Big", Facts: 40, First: now, Last: now, Sessions: map[string]bool{"s": true, "t": true}, Labels: map[string]int{"y": 3}},
	}
	out := projectSkeletonText(stats, 1, 42, 2)

	if !strings.Contains(out, "Topic: Big") {
		t.Errorf("largest topic missing from the map:\n%s", out)
	}
	if strings.Contains(out, "Topic: Small") {
		t.Errorf("topic cap not applied:\n%s", out)
	}
	if !strings.Contains(out, "+1 smaller topics not listed") {
		t.Errorf("capped topics were not disclosed:\n%s", out)
	}
}

func TestBackfillProjectIsIdempotentAndScoped(t *testing.T) {
	g := newTestGraph(t)
	// Simulate pre-upgrade rows: no project, no session type.
	addFact(t, g, "", "legacy", "", "Auth", "MATCH legacy fact", 0)
	addFact(t, g, "/already/set", "current", "build", "Auth", "MATCH current fact", 0)

	n, err := g.Store.BackfillProject("/proj", map[string]string{"legacy": "build", "current": "build"})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 {
		t.Fatalf("backfill stamped %d nodes, want 1 (the legacy row only)", n)
	}

	again, err := g.Store.BackfillProject("/proj", map[string]string{"legacy": "build", "current": "build"})
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if again != 0 {
		t.Errorf("re-running backfill stamped %d more nodes, want 0", again)
	}

	// The already-homed session keeps its project.
	var scanned []string
	if err := g.Store.ScanProjectFacts("/already/set", ProjectFilter{}, func(node Node, _ []float32) {
		scanned = append(scanned, node.Content)
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(scanned) != 1 || scanned[0] != "MATCH current fact" {
		t.Errorf("backfill re-homed an already-scoped session: %v", scanned)
	}
}

func TestListProjectTopicsOrdersBySize(t *testing.T) {
	g := newTestGraph(t)
	for i := 0; i < 3; i++ {
		addFact(t, g, "/proj", "s1", "build", "Big Topic", fmt.Sprintf("fact %d", i), 0)
	}
	addFact(t, g, "/proj", "s1", "build", "Small Topic", "lone fact", 0)

	topics, err := g.Store.ListProjectTopics("/proj", ProjectFilter{SessionTypes: DefaultProjectSessionTypes}, 10)
	if err != nil {
		t.Fatalf("list topics: %v", err)
	}
	if len(topics) != 2 {
		t.Fatalf("got %d topics, want 2: %v", len(topics), topics)
	}
	if topics[0].Name != "Big Topic" || topics[0].Facts != 3 {
		t.Errorf("topics[0] = %+v, want Big Topic with 3 facts", topics[0])
	}
}

func TestProjectRecallWithoutChatReturnsAssembledContext(t *testing.T) {
	g := newTestGraph(t)
	addFact(t, g, "/proj", "s1", "build", "Auth", "MATCH we chose JWT", 0)
	addFact(t, g, "/proj", "s2", "build", "Auth", "MATCH we later moved to sessions", 0)
	addFact(t, g, "/proj", "s2", "subagent", "Auth", "MATCH subagent trace", 0)

	res, err := g.ProjectRecall(context.Background(), ProjectRecallOptions{
		ProjectID:    "/proj",
		Question:     "MATCH what did we pick",
		SessionTypes: DefaultProjectSessionTypes,
	})
	if err != nil {
		t.Fatalf("project recall: %v", err)
	}
	if res.TotalFacts != 2 {
		t.Fatalf("TotalFacts = %d, want 2 (subagent turns excluded)", res.TotalFacts)
	}
	if res.SessionsUsed != 2 {
		t.Errorf("SessionsUsed = %d, want 2", res.SessionsUsed)
	}
	if !strings.Contains(res.Answer, "PROJECT MAP") || !strings.Contains(res.Answer, "RELEVANT FACTS") {
		t.Errorf("assembled context is missing its sections:\n%s", res.Answer)
	}
	if strings.Contains(res.Answer, "subagent trace") {
		t.Errorf("subagent turn leaked into project recall:\n%s", res.Answer)
	}
}

func TestProjectRecallEmptyProject(t *testing.T) {
	g := newTestGraph(t)
	res, err := g.ProjectRecall(context.Background(), ProjectRecallOptions{ProjectID: "/empty", Question: "MATCH anything"})
	if err != nil {
		t.Fatalf("project recall: %v", err)
	}
	if res.TotalFacts != 0 || res.Answer != "" {
		t.Errorf("recall on an empty project = %+v, want a zero result", res)
	}
}

func TestProjectRecallRequiresProjectID(t *testing.T) {
	g := newTestGraph(t)
	if _, err := g.ProjectRecall(context.Background(), ProjectRecallOptions{Question: "MATCH"}); err == nil {
		t.Error("expected an error when no project id is given")
	}
}

func TestOnlySessionRestrictsToOneConversation(t *testing.T) {
	g := newTestGraph(t)
	addFact(t, g, "/proj", "s1", "build", "Auth", "MATCH from this session", 0)
	addFact(t, g, "/proj", "s2", "build", "Auth", "MATCH from another session", 0)

	var got []string
	if err := g.Store.ScanProjectFacts("/proj", ProjectFilter{OnlySession: "s1"}, func(n Node, _ []float32) {
		got = append(got, n.Content)
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 1 || got[0] != "MATCH from this session" {
		t.Fatalf("session-scoped scan = %v, want only s1's fact", got)
	}
}

func TestOnlySessionOverridesSessionTypeFilter(t *testing.T) {
	g := newTestGraph(t)
	// A subagent session asking about its own history must still find it, even
	// though subagent turns are excluded from project-wide recall.
	addFact(t, g, "/proj", "sub", "subagent", "Auth", "MATCH my own trace", 0)

	var got []string
	err := g.Store.ScanProjectFacts("/proj", ProjectFilter{
		OnlySession:  "sub",
		SessionTypes: DefaultProjectSessionTypes,
	}, func(n Node, _ []float32) {
		got = append(got, n.Content)
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("explicitly named session returned %v, want its own fact despite the type filter", got)
	}
}

func TestProjectRecallScopedToSession(t *testing.T) {
	g := newTestGraph(t)
	addFact(t, g, "/proj", "current", "build", "Auth", "MATCH we chose JWT here", 0)
	addFact(t, g, "/proj", "other", "build", "Auth", "MATCH a different conversation", 0)

	res, err := g.ProjectRecall(context.Background(), ProjectRecallOptions{
		ProjectID:    "/proj",
		Question:     "MATCH what did we pick",
		OnlySession:  "current",
		SessionTypes: DefaultProjectSessionTypes,
	})
	if err != nil {
		t.Fatalf("project recall: %v", err)
	}
	if res.TotalFacts != 1 || res.SessionsUsed != 1 {
		t.Fatalf("scoped recall saw %d facts across %d sessions, want 1 and 1", res.TotalFacts, res.SessionsUsed)
	}
	if !strings.Contains(res.Answer, "we chose JWT here") {
		t.Errorf("scoped recall dropped the session's own fact:\n%s", res.Answer)
	}
	if strings.Contains(res.Answer, "a different conversation") {
		t.Errorf("scoped recall leaked another conversation:\n%s", res.Answer)
	}
}
