package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// OpenAI and Ollama are the same OpenAIProvider, and it joins every system entry
// into ONE system message at messages[0]. Both cache by longest common prefix,
// so the first byte that differs between two steps ends the cached region — and
// everything behind it, the whole conversation included, is re-read at full
// price. buildSystemPromptEntries runs inside the step loop, so anything
// time-varying in it can never match twice.
//
// This pins the joined prompt byte-for-byte across a simulated step boundary.
func TestSystemPromptIsByteStableAcrossSteps(t *testing.T) {
	build := func() string {
		// The identical arguments a single turn passes on each of its steps.
		return strings.Join(buildSystemPromptEntries(
			BuildAgent, "/tmp/proj", false, "", "", 1920, 1080, "", 900, true), "\n\n")
	}

	first := build()
	// Longer than a second: the regression this guards against was a
	// second-resolution clock, which a sub-second sleep would not expose.
	time.Sleep(1100 * time.Millisecond)
	second := build()

	if first == second {
		return
	}
	i := 0
	for i < len(first) && i < len(second) && first[i] == second[i] {
		i++
	}
	lo, hi := i-80, i+80
	if lo < 0 {
		lo = 0
	}
	if hi > len(first) {
		hi = len(first)
	}
	t.Fatalf("system prompt differs between two steps of one turn — this costs the whole "+
		"conversation's cache on OpenAI/Ollama every step.\ndiverges at byte %d of %d\ncontext: %q",
		i, len(first), first[lo:hi])
}

// The date must still be present and readable — the fix was to coarsen it, not
// to drop it, and an agent that has no idea what day it is makes worse calls.
func TestSystemReminderStillCarriesTheDate(t *testing.T) {
	got := systemReminderPrompt()
	if !strings.Contains(got, "Current date:") {
		t.Fatalf("date line missing: %q", got)
	}
	if want := time.Now().Format("2006"); !strings.Contains(got, want) {
		t.Errorf("reminder %q does not carry the current year %s", got, want)
	}
	// A colon here means a wall clock crept back in, which is the whole bug.
	if strings.Contains(got, ":") != strings.Contains(got, "Current date:") {
		t.Errorf("unexpected colon in %q", got)
	}
	if strings.Count(got, ":") != 1 {
		t.Errorf("reminder looks time-resolved, not day-resolved: %q", got)
	}
}

// An output-only agent's FinalInstruction ("respond with only the note") is
// pinned to the end of the system entries because that is where a format
// constraint has the most influence — closest to the model's response, least
// diluted by everything above it. buildSystemPromptEntries puts it there, but
// RunLoop appends three more blocks after that function returns (the compaction
// summary, the compact_context guidance, the skill list), and a plain append
// silently undid it: every agent with a FinalInstruction also holds "read",
// which is what puts compact_context on the wire, so the ~2KB compaction
// section routinely landed between the constraint and the response.
//
// appendSystemEntry inserts before the instruction instead. This pins that the
// entry lands, and that the instruction stays last.
func TestFinalInstructionStaysLastAfterPerTurnBlocks(t *testing.T) {
	for _, a := range []Agent{NoteAgent, SubagentAgent, SearchAgent, MemoryRecallAgent} {
		if a.FinalInstruction == "" {
			t.Fatalf("%s: expected a FinalInstruction (the test is about it)", a.Name)
		}
		entries := buildSystemPromptEntries(a, "/tmp/proj", false, "", "", 1920, 1080, "", 900, true)
		if got := entries[len(entries)-1]; got != a.FinalInstruction {
			t.Fatalf("%s: buildSystemPromptEntries did not end with FinalInstruction", a.Name)
		}

		// The three blocks RunLoop appends, in the order it appends them.
		for _, extra := range []string{"COMPACTION-SUMMARY", compactContextPrompt(), "SKILL-LIST"} {
			entries = appendSystemEntry(entries, a, extra)
		}

		if got := entries[len(entries)-1]; got != a.FinalInstruction {
			t.Errorf("%s: FinalInstruction is no longer last — it is %d entries from the end, "+
				"displaced by the per-turn blocks RunLoop appends",
				a.Name, len(entries)-1-indexOfEntry(entries, a.FinalInstruction))
		}
		for _, extra := range []string{"COMPACTION-SUMMARY", "SKILL-LIST"} {
			if indexOfEntry(entries, extra) < 0 {
				t.Errorf("%s: %s was dropped instead of inserted", a.Name, extra)
			}
		}
	}
}

// An agent with no FinalInstruction must still get a plain append — nothing to
// protect, and nothing should be reordered around.
func TestAppendSystemEntry_PlainAppendWithoutFinalInstruction(t *testing.T) {
	if BuildAgent.FinalInstruction != "" {
		t.Fatal("BuildAgent unexpectedly has a FinalInstruction")
	}
	entries := []string{"base", "date"}
	entries = appendSystemEntry(entries, BuildAgent, "skills")
	if len(entries) != 3 || entries[2] != "skills" {
		t.Errorf("expected a plain append, got %v", entries)
	}
}

func indexOfEntry(entries []string, want string) int {
	for i, e := range entries {
		if e == want {
			return i
		}
	}
	return -1
}

// OpenAI, Ollama and OpenRouter join EVERY system entry into one message at
// messages[0] and cache by longest common prefix, so the first entry that
// differs from the previous request ends the cached region and takes every
// entry behind it with it. Entry order is therefore a caching decision: the
// entries have to run stable -> volatile.
//
// The index status moves only when the user rebuilds the index; the date once a
// day; the viewport is the one value the client recomputes on every request
// (session.tsx reads window.innerWidth at send time).
func TestSystemEntriesRunStableToVolatile(t *testing.T) {
	entries := buildSystemPromptEntries(
		BuildAgent, "/tmp/proj", false, "", "", 1920, 1080, "", 900, true)

	posOf := func(fragment string) int {
		for i, e := range entries {
			if strings.Contains(e, fragment) {
				return i
			}
		}
		t.Fatalf("no entry contains %q; entries: %d", fragment, len(entries))
		return -1
	}

	base, index := 0, posOf("Project index:")
	date, viewport := posOf("<system-reminder>"), posOf("Rendering viewport")

	if index <= base {
		t.Error("the static base must come first")
	}
	if date <= index {
		t.Errorf("the date (daily) must follow the index status (per rebuild): date=%d index=%d", date, index)
	}
	if viewport <= date {
		t.Errorf("the viewport (per request) must follow the date (daily): viewport=%d date=%d", viewport, date)
	}
}

// The compaction summary is the only system entry that can change DURING a
// turn — compaction fires mid-loop and rewrites it. Ahead of the skill list and
// the compaction guidance it discarded those too, on the one step that could
// least afford it. This pins it last among the per-turn blocks RunLoop appends.
func TestCompactionSummaryIsTheLastSystemEntry(t *testing.T) {
	a := BuildAgent // no FinalInstruction, so "last" is literally last
	entries := buildSystemPromptEntries(a, "/tmp/proj", false, "", "", 1920, 1080, "", 900, true)

	// The order RunLoop appends them in.
	entries = appendSystemEntry(entries, a, compactContextPrompt())
	entries = appendSystemEntry(entries, a, "SKILL-LIST")
	entries = appendSystemEntry(entries, a, "COMPACTION-SUMMARY")

	if got := entries[len(entries)-1]; got != "COMPACTION-SUMMARY" {
		t.Errorf("last entry is %.40q, want the compaction summary", got)
	}

	// And with an output-only agent it sits immediately before the pinned
	// FinalInstruction, which stays last for prompt reasons.
	n := NoteAgent
	e2 := buildSystemPromptEntries(n, "/tmp/proj", false, "", "", 1920, 1080, "", 900, true)
	e2 = appendSystemEntry(e2, n, compactContextPrompt())
	e2 = appendSystemEntry(e2, n, "SKILL-LIST")
	e2 = appendSystemEntry(e2, n, "COMPACTION-SUMMARY")

	if e2[len(e2)-1] != n.FinalInstruction {
		t.Error("FinalInstruction must stay pinned last")
	}
	if e2[len(e2)-2] != "COMPACTION-SUMMARY" {
		t.Errorf("compaction summary should sit just before FinalInstruction, got %.40q", e2[len(e2)-2])
	}
}

// Mid-loop guidance has two placements and the loop must pick exactly one.
//
// The fallback folds it into the turn's FIRST user message, which is the
// earliest byte that can change and therefore discards every tool result behind
// it — on the step right after the user steers, the one they are waiting on.
// Anthropic places it itself, at the end, where nothing is behind it. Doing both
// would send the guidance twice.
func TestGuidanceRoutingPicksOnePlacement(t *testing.T) {
	if !provider.PlacesGuidance(provider.NewAnthropicProvider()) {
		t.Error("Anthropic should place guidance itself, so the loop must not also fold it in")
	}
	// The OpenAI-shaped providers carry a tool result as its own role:"tool"
	// message, which cannot hold unrelated text, so they keep the fallback.
	for _, p := range []provider.Provider{
		provider.NewOpenAIProvider(),
		provider.NewOpenRouterProvider(),
		provider.NewOllamaProvider(),
	} {
		if provider.PlacesGuidance(p) {
			t.Errorf("%s claims to place guidance; the loop would then skip the fallback and drop it", p.ID())
		}
	}
	// A nil provider (tests, early startup) must not panic or claim placement.
	if provider.PlacesGuidance(nil) {
		t.Error("a nil provider must not claim to place guidance")
	}
}

// Whichever path runs, the model must read the same words — the placement is a
// caching decision, not a prompt change.
func TestGuidanceWordingIsIdenticalOnBothPaths(t *testing.T) {
	const raw = "skip the tests"
	formatted := guidanceUserContent(raw)

	if !strings.Contains(formatted, raw) {
		t.Fatalf("formatted guidance lost the text: %q", formatted)
	}
	if !strings.Contains(formatted, guidanceLabel) {
		t.Error("the label must survive, or the model reads the guidance as part of the prompt")
	}

	msgs := []provider.ModelMessage{{Role: "user", Content: mustMarshal("the task")}}
	appendGuidanceToUserMessage(msgs, raw)
	var got string
	if err := json.Unmarshal(msgs[0].Content, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasSuffix(got, formatted) {
		t.Errorf("the fallback must append exactly what the provider path receives:\n got %q\nwant suffix %q", got, formatted)
	}
}
