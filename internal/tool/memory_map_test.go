package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/memfile"
)

// projectScopeCtx carries the recall scope the real runner sets for project
// recall — the summaries are indexed under ProjectID "/proj", which the tool
// takes from the scope rather than from the (temp) session dir.
func projectScopeCtx() context.Context {
	return WithRecallScope(context.Background(), RecallScope{Scope: "project", ProjectID: "/proj"})
}

func newMemoryMapTestStore(t *testing.T) *memfile.Store {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return memfile.NewStore(database)
}

// indexSummary writes and indexes one summary with the given labels, mirroring
// what the real writer + IndexFile produce.
func indexSummary(t *testing.T, s *memfile.Store, dir string, sessionID, title string, labels []string, created time.Time) {
	t.Helper()
	body := "# " + title + "\n"
	if len(labels) > 0 {
		body += "\nTopics: " + strings.Join(labels, ", ") + "\n"
	}
	body += "\n## Request\nbody\n"
	meta := memfile.Meta{SessionID: sessionID, ProjectID: "/proj", SessionType: "build", Title: title, CreatedAt: created}
	path, err := memfile.Write(dir, meta, body)
	if err != nil {
		t.Fatalf("write %q: %v", title, err)
	}
	if err := s.IndexFile(path, meta); err != nil {
		t.Fatalf("index %q: %v", title, err)
	}
}

// memEntry fabricates one indexed summary without touching disk — enough for
// the render-level tests, which are about how a level degrades under the byte
// budget, not about indexing. The path is real-looking so memoryDirOf and the
// file-line shape behave as they do in production.
func memEntry(sessionID, title string, created time.Time, labels ...string) *memfile.Entry {
	return &memfile.Entry{
		Path:      filepath.Join("/proj/.ogcode/memory", memfile.Filename(created, sessionID, title)),
		SessionID: sessionID,
		ProjectID: "/proj",
		Labels:    labels,
		CreatedAt: created.UnixMilli(),
	}
}

// A conversation line is capped at sessionLabelCap labels however many distinct
// topics its turns carry: one line stands for a whole branch of turns, and the
// per-turn detail arrives on drill-down — the codebase_map folder-line rule.
// Every turn is heard before any turn is heard twice.
func TestMemoryMap_ConversationLineCappedAtTheSessionCeiling(t *testing.T) {
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	// Six turns of eight labels each: 48 distinct topics in one conversation,
	// more than the cap allows.
	entries := make([]*memfile.Entry, 0, 6)
	for turn := 0; turn < 6; turn++ {
		labels := make([]string, 8)
		for j := range labels {
			labels[j] = fmt.Sprintf("topic%02d", turn*8+j)
		}
		entries = append(entries, memEntry("sessAABBCC", "turn", base.Add(time.Duration(turn)*time.Hour), labels...))
	}

	out := renderMemoryMap(entries, "this project", false, "")
	line := folderLine(t, out, "sessAABB/")
	if got := len(lineLabels(line)); got != sessionLabelCap {
		t.Errorf("conversation line carries %d labels, want the cap of %d:\n%s", got, sessionLabelCap, line)
	}
	// Each turn's first — most important — label is on the line.
	for turn := 0; turn < 6; turn++ {
		if !strings.Contains(line, fmt.Sprintf("topic%02d", turn*8)) {
			t.Errorf("turn %d went unheard:\n%s", turn, line)
		}
	}
}

// A level too wide for every conversation's full label set must degrade by
// showing fewer labels, not by dropping them wholesale — the ladder
// codebase_map uses. The raised cap makes this reachable: many conversations
// each carrying a full label set cross the budget long before any single one
// does.
func TestMemoryMap_WideLevelShowsFewerLabelsRatherThanNone(t *testing.T) {
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	labels := make([]string, 8)
	for j := range labels {
		labels[j] = fmt.Sprintf("conversation behaviour topic %d", j)
	}
	// Four digits keep each session id inside the 8-char tag, so every entry is
	// its own conversation. 1200 conversations render ~331 KB at full depth and
	// ~139 KB at three labels each, both past the 100 KB budget, settling on one
	// label per line (~62 KB).
	const conversations = 1200
	entries := make([]*memfile.Entry, 0, conversations)
	for i := 0; i < conversations; i++ {
		entries = append(entries, memEntry(fmt.Sprintf("sess%04d", i), "turn", base, labels...))
	}

	// Sanity: at full depth this level must overflow, or the test proves nothing.
	var probe strings.Builder
	renderSessions(summarizeSessions(entries), &probe, sessionLabelCap, func(n int) string { return fmt.Sprintf("%d turns", n) })
	if probe.Len() <= memoryMapBudget {
		t.Skipf("fixture is only %d bytes at full depth — widen it to exercise degradation", probe.Len())
	}

	out := renderMemoryMap(entries, "this project", false, "")
	if len(out) > memoryMapBudget {
		t.Errorf("map is %d bytes, over the %d budget", len(out), memoryMapBudget)
	}
	// Labels survive — the whole point. If degradation dropped them, the line
	// would carry the tag and turn count alone.
	if !strings.Contains(out, "conversation behaviour topic") {
		t.Errorf("a wide level lost every label instead of showing fewer:\n%s", out[:400])
	}
	// And they survive at reduced depth: three labels per line must not fit a
	// level this wide, so the render had to go below that rung.
	atThree := "conversation behaviour topic 0; conversation behaviour topic 1; conversation behaviour topic 2"
	if strings.Contains(out, atThree) {
		t.Error("expected a cap below 3 labels per conversation for a level this wide")
	}
}

// Past every rung the labels go, not the structure — and the agent is told how
// to get them back rather than being left with a silently shorter map. This is
// the last resort: a level where even one label per line will not fit, so the
// fabricated memory is a single conversation's worth of thousands of turns.
func TestMemoryMap_LargeProjectDropsLabelsWithGuidance(t *testing.T) {
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	label := "conversation behaviour topic 0"
	// 3000 conversations: one label each still renders ~156 KB, past the 100 KB
	// budget, so even the 1-label rung overflows and the map drops to the
	// label-less outline.
	const conversations = 3000
	entries := make([]*memfile.Entry, 0, conversations)
	for i := 0; i < conversations; i++ {
		entries = append(entries, memEntry(fmt.Sprintf("sess%04d", i), "turn", base, label))
	}

	small := renderMemoryMap(entries[:20], "this project", false, "")
	large := renderMemoryMap(entries, "this project", false, "")

	if !strings.Contains(small, label) {
		t.Error("a small memory should still show labels")
	}
	if strings.Contains(large, label) {
		t.Error("an oversized memory should drop labels, not keep them")
	}
	if !strings.Contains(large, "subdir") {
		t.Errorf("oversized map does not tell the agent how to get labels back:\n%s", large)
	}
}

// The map's byte budget sits above MaxToolOutputBytes, the generic cap on any
// tool result. That is only safe because Execute opts the result out of the
// loop's backstop (Result.Truncated): without the flag the backstop would
// head-truncate a large map mid-conversation — the very thing the budget exists
// to prevent. Pinned at the Execute boundary, since a direct renderMemoryMap
// call never touches the flag.
func TestMemoryMap_ResultOptsOutOfTheGenericOutputCap(t *testing.T) {
	s := newMemoryMapTestStore(t)
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	labels := make([]string, 8)
	for j := range labels {
		labels[j] = fmt.Sprintf("conversation behaviour topic %d", j)
	}
	// 800 conversations at the label ceiling render ~93 KB once the map sheds
	// to three labels per line — past the generic 50 KB cap but inside the
	// 100 KB budget, so the flag — not the size — is what keeps it intact.
	for i := 0; i < 800; i++ {
		if err := s.Upsert(memEntry(fmt.Sprintf("sess%04d", i), "turn", base, labels...)); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	res, err := NewMemoryMapTool(s).Execute(projectScopeCtx(), nil, Context{SessionDir: t.TempDir()})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(res.Output) <= MaxToolOutputBytes {
		t.Fatalf("fixture renders only %d bytes, under the %d generic cap — widen it to exercise the opt-out",
			len(res.Output), MaxToolOutputBytes)
	}
	if !res.Truncated {
		t.Errorf("map is %d bytes but the result is not marked Truncated — the loop's backstop would cut the map mid-conversation",
			len(res.Output))
	}
}

// Project scope with no subdir must render the codebase_map shape: every
// conversation collapsed to ONE line — tag/, its turn count, a sample of its
// topics — and nothing else.
func TestMemoryMap_ProjectScopeCollapsesConversations(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)

	// Three turns in one conversation, one in another.
	indexSummary(t, s, dir, "sessAABBCC", "First", []string{"migrations", "schema"}, base)
	indexSummary(t, s, dir, "sessAABBCC", "Second", []string{"migrations", "tooling"}, base.Add(time.Hour))
	indexSummary(t, s, dir, "sessAABBCC", "Third", []string{"migrations"}, base.Add(2*time.Hour))
	indexSummary(t, s, dir, "sessXXYYZZ", "Solo", []string{"ui"}, base.Add(3*time.Hour))

	res, err := NewMemoryMapTool(s).Execute(projectScopeCtx(), nil, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := res.Output
	if !strings.Contains(out, "4 turns in this project") {
		t.Fatalf("missing total in header:\n%s", out)
	}
	// The conversation line carries the count and the topics, each once.
	if !strings.Contains(out, "sessAABB/ (3 turns) — migrations; schema; tooling") {
		t.Fatalf("missing collapsed line for sessAABBCC:\n%s", out)
	}
	if !strings.Contains(out, "sessXXYY/ (1 turn) — ui") {
		t.Fatalf("missing collapsed line for sessXXYYZZ:\n%s", out)
	}
	// Collapsed means collapsed: no outline, no path at this level.
	if strings.Contains(out, "## Request") || strings.Contains(out, ".md") {
		t.Fatalf("project level leaked per-turn detail:\n%s", out)
	}
	// The drilldown hint names a real tag: the conversation with the most turns.
	if !strings.Contains(out, `call again with subdir set to its tag (e.g. subdir="sessAABB")`) {
		t.Fatalf("missing drilldown hint:\n%s", out)
	}
}

// The subdir drilldown lists one conversation's summaries as codebase_map file
// lines: file name (timestamp--tag--slug) and its topic labels. No title, no
// "Topics:" block, no path, and no outline — the outline is file_map's job.
func TestMemoryMap_SubdirDrillsIntoConversation(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	indexSummary(t, s, dir, "sessAABBCC", "First", []string{"migrations"}, base)
	indexSummary(t, s, dir, "sessXXYYZZ", "Other", []string{"ui"}, base.Add(time.Hour))

	args, _ := json.Marshal(map[string]any{"subdir": "sessAABB"})
	res, err := NewMemoryMapTool(s).Execute(projectScopeCtx(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := res.Output
	if !strings.Contains(out, "1 turn in conversation sessAABB") {
		t.Fatalf("missing drilldown header:\n%s", out)
	}
	// The codebase_map file-line shape: name and labels on one line.
	if !strings.Contains(out, "2026-09-09T100000Z--sessAABBCC--first.md — migrations") {
		t.Fatalf("drilldown missing the file line for the turn:\n%s", out)
	}
	// Collapsed means collapsed: no old-style detail lines, no other conversation.
	if strings.Contains(out, "Topics:") || strings.Contains(out, "## Request") ||
		strings.Contains(out, "other") || strings.Contains(out, "sessXXYY") {
		t.Fatalf("drilldown leaked outline or another conversation:\n%s", out)
	}
}

// An unknown subdir is an error the model can act on, not an empty success.
func TestMemoryMap_SubdirUnknownTagFailsCleanly(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	indexSummary(t, s, dir, "sessAABBCC", "First", []string{"migrations"}, time.Now())

	args, _ := json.Marshal(map[string]any{"subdir": "sessNOPE"})
	res, err := NewMemoryMapTool(s).Execute(projectScopeCtx(), args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "No conversation with tag") {
		t.Fatalf("expected a clean not-found, got:\n%s", res.Output)
	}
}

// Session-scoped recall stays flat: that conversation's summaries, one
// codebase_map file line each.
func TestMemoryMap_SessionScopeStaysFlat(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	indexSummary(t, s, dir, "sessAABBCC", "First", []string{"migrations"}, base)
	indexSummary(t, s, dir, "sessAABBCC", "Second", []string{"tooling"}, base.Add(time.Hour))
	indexSummary(t, s, dir, "sessXXYYZZ", "Other", []string{"ui"}, base.Add(2*time.Hour))

	ctx := WithRecallScope(context.Background(), RecallScope{Scope: "session", SessionID: "sessAABBCC", ProjectID: "/proj"})
	res, err := NewMemoryMapTool(s).Execute(ctx, nil, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := res.Output
	if !strings.Contains(out, "2 turns in this conversation") {
		t.Fatalf("missing flat header:\n%s", out)
	}
	if !strings.Contains(out, "--sessAABBCC--first.md — migrations") || !strings.Contains(out, "--sessAABBCC--second.md — tooling") {
		t.Fatalf("flat render missing file lines:\n%s", out)
	}
	// No collapsed lines, no other conversation's summaries, no outline.
	if strings.Contains(out, "sessAABB/") || strings.Contains(out, "other") ||
		strings.Contains(out, "## Request") {
		t.Fatalf("session scope leaked other data, collapsed lines, or outlines:\n%s", out)
	}
}

// Session scope with a foreign subdir is refused, not silently widened.
func TestMemoryMap_SessionScopeRejectsForeignSubdir(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	indexSummary(t, s, dir, "sessAABBCC", "First", []string{"migrations"}, time.Now())

	ctx := WithRecallScope(context.Background(), RecallScope{Scope: "session", SessionID: "sessAABBCC", ProjectID: "/proj"})
	args, _ := json.Marshal(map[string]any{"subdir": "sessXXYYZZ"})
	res, err := NewMemoryMapTool(s).Execute(ctx, args, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Output, "scoped to conversation") {
		t.Fatalf("expected scope refusal, got:\n%s", res.Output)
	}
}

// A conversation line samples the whole conversation, not its alphabetical
// head: a turn's labels are nearly all its own, so a ranking by how many turns
// carry a label ties everywhere and its tie-break decided the line.
func TestMemoryMap_ConversationLineSamplesEveryPartOfIt(t *testing.T) {
	base := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	entries := make([]*memfile.Entry, 0, 60)
	for turn := 0; turn < 60; turn++ {
		// Labels that sort in the opposite order to time, so a head-of-alphabet
		// pick and a head-of-time pick would both miss most of the conversation.
		entries = append(entries, memEntry("sessAABBCC", fmt.Sprintf("turn %02d", turn),
			base.Add(time.Duration(turn)*time.Minute), fmt.Sprintf("topic %02d", 59-turn)))
	}
	labels := lineLabels(folderLine(t, renderMemoryMap(entries, "this project", false, ""), "sessAABB/"))
	if len(labels) != sessionLabelCap {
		t.Fatalf("conversation line carries %d labels, want %d: %q", len(labels), sessionLabelCap, labels)
	}
	var early, middle, late bool
	for _, l := range labels {
		var n int
		fmt.Sscanf(l, "topic %d", &n)
		turn := 59 - n
		switch {
		case turn < 20:
			early = true
		case turn < 40:
			middle = true
		default:
			late = true
		}
	}
	if !early || !middle || !late {
		t.Errorf("labels come from only part of the conversation (early %v, middle %v, late %v): %q", early, middle, late, labels)
	}
}

// A summary line joins its labels with semicolons, after a dash, so a label
// with commas of its own reads as one — and one carrying a semicolon is
// cleaned rather than split.
func TestMemoryMap_SummaryLineSeparatesLabelsUnambiguously(t *testing.T) {
	e := memEntry("sessAABBCC", "First", time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC),
		"Yamux Tunnel, Reverse Proxy", "Auth; Sessions")
	got := renderSummaryLine(e, textLabelCap)
	want := "2026-09-09T100000Z--sessAABBCC--first.md — Yamux Tunnel, Reverse Proxy; Auth, Sessions\n"
	if got != want {
		t.Errorf("summary line = %q, want %q", got, want)
	}
}

// Conversations begun within a couple of hours of each other share the leading
// characters of their session ids — the ids lead with the creation time — and
// used to share one tag, one map line and one drilldown. Each now keeps its own
// line under the shortest tag that tells it apart, and a short tag that fits
// several is refused with the tags that do, never answered with a blend.
func TestMemoryMap_ConversationsBegunTogetherKeepTheirOwnLines(t *testing.T) {
	s := newMemoryMapTestStore(t)
	dir := t.TempDir()
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	const (
		first  = "ses_01M269J8TM53WGCRH83WZY8QD7"
		second = "ses_01M268Y4TGXSBJB7PYHTB8399B"
		later  = "ses_01M32MFDSH78P6VXWYQ72MRP9X"
	)
	indexSummary(t, s, dir, first, "Deploy plan", []string{"deploy"}, base)
	indexSummary(t, s, dir, first, "Deploy again", []string{"rollback"}, base.Add(time.Hour))
	indexSummary(t, s, dir, second, "Pricing", []string{"pricing"}, base.Add(30*time.Minute))
	indexSummary(t, s, dir, later, "Onboarding", []string{"onboarding"}, base.Add(72*time.Hour))
	tool := NewMemoryMapTool(s)

	res, err := tool.Execute(projectScopeCtx(), nil, Context{SessionDir: dir})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{
		"ses01M268/ (1 turn) — pricing",
		"ses01M269/ (2 turns) — rollback; deploy",
		"ses01M32/ (1 turn) — onboarding",
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("map missing %q:\n%s", want, res.Output)
		}
	}

	drill := func(subdir string) string {
		t.Helper()
		args, _ := json.Marshal(map[string]any{"subdir": subdir})
		res, err := tool.Execute(projectScopeCtx(), args, Context{SessionDir: dir})
		if err != nil {
			t.Fatalf("Execute(%q): %v", subdir, err)
		}
		return res.Output
	}

	// The old shared tag fits both, so it names neither.
	amb := drill("ses01M26")
	if !strings.Contains(amb, "fits 2 conversations") ||
		!strings.Contains(amb, "ses01M268/ (1 turn)") || !strings.Contains(amb, "ses01M269/ (2 turns)") {
		t.Errorf("an ambiguous tag was not refused with the tags that tell the conversations apart:\n%s", amb)
	}
	if strings.Contains(amb, "pricing") || strings.Contains(amb, "deploy") {
		t.Errorf("an ambiguous tag listed turns:\n%s", amb)
	}

	// The shown tag, a longer prefix and the session id itself all name one.
	for _, subdir := range []string{"ses01M269", "ses01M269J8TM", first} {
		out := drill(subdir)
		if !strings.Contains(out, "2 turns in conversation ses01M269") ||
			!strings.Contains(out, "rollback") || strings.Contains(out, "pricing") {
			t.Errorf("subdir %q did not open just its conversation:\n%s", subdir, out)
		}
	}
}

// A tag is the shortest prefix of its conversation's key that no other key
// starts with, never under minTagLen — and a key that is itself a prefix of
// another still resolves to exactly itself.
func TestMemoryMap_ConversationTagsAreUniquePrefixes(t *testing.T) {
	entries := []*memfile.Entry{
		{SessionID: "ses_01M269J8TM53WGCRH83WZY8QD7"},
		{SessionID: "ses_01M268Y4TGXSBJB7PYHTB8399B"},
		{SessionID: "ses_01M32MFDSH78P6VXWYQ72MRP9X"},
		{SessionID: "sessAB"},
		{SessionID: "sessABCD1234"},
	}
	tags := conversationTags(entries)
	for key, want := range map[string]string{
		"ses01M269J8TM53WGCRH83WZY8QD7": "ses01M269",
		"ses01M268Y4TGXSBJB7PYHTB8399B": "ses01M268",
		"ses01M32MFDSH78P6VXWYQ72MRP9X": "ses01M32",
		"sessAB":                        "sessAB",
		"sessABCD1234":                  "sessABCD",
	} {
		if got := tags[key]; got != want {
			t.Errorf("tag for %s = %q, want %q", key, got, want)
		}
	}
	if got := resolveConversation("sessAB", entries); len(got) != 1 || got[0] != "sessAB" {
		t.Errorf("an exact key did not resolve to itself alone: %v", got)
	}
}
