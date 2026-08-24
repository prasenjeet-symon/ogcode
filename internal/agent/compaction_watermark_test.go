package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func msgs(ids ...string) []*session.MessageWithParts {
	out := make([]*session.MessageWithParts, 0, len(ids))
	for _, id := range ids {
		out = append(out, &session.MessageWithParts{
			Info: session.MessageInfo{ID: session.MessageID(id)},
		})
	}
	return out
}

const goodSummary = "Explored the auth package: middleware/auth.go:40-120 holds the token check. " +
	"Ruled out changing the session store. Next: add the refresh path."

func TestWatermarkAcceptsAMessageNotYetInTheWorkingSet(t *testing.T) {
	// The loop folds an iteration's new messages into the working set at the top
	// of the FOLLOWING iteration, so the assistant message that carried the
	// compact_context call is not in the slice when the watermark is recorded.
	// Requiring presence here would reject every real compaction.
	var w compactionWatermark
	working := msgs("m1", "m2")

	if !w.set("m3-not-yet-folded", goodSummary, working) {
		t.Fatal("watermark rejected a message the working set has not folded in yet")
	}
	if !w.active() {
		t.Error("watermark should be active after being set")
	}
}

func TestWatermarkRejectsEmptyInput(t *testing.T) {
	var w compactionWatermark
	working := msgs("m1")

	if w.set("", goodSummary, working) {
		t.Error("accepted an empty message id")
	}
	if w.set("m1", "", working) {
		t.Error("accepted an empty summary")
	}
	if w.active() {
		t.Error("watermark must stay inactive after rejected sets")
	}
}

func TestWatermarkRefusesToMoveBackwards(t *testing.T) {
	working := msgs("m1", "m2", "m3", "m4")
	var w compactionWatermark

	if !w.set("m3", goodSummary, working) {
		t.Fatal("first set rejected")
	}
	// An earlier message would readmit history the agent already discarded.
	if w.set("m2", "an earlier summary that should not be allowed to take effect here", working) {
		t.Error("accepted a watermark earlier than the current one")
	}
	if w.messageID != "m3" {
		t.Errorf("watermark moved to %q, want it to stay at m3", w.messageID)
	}
	// Forward is fine — later summaries describe more of the turn.
	if !w.set("m4", goodSummary, working) {
		t.Error("rejected a watermark later than the current one")
	}
}

func TestSliceStartNarrowsWithinTheTurn(t *testing.T) {
	working := msgs("m1", "m2", "m3", "m4", "m5")
	var w compactionWatermark

	// No watermark: the turn boundary is untouched.
	if got := w.sliceStart(working, 1); got != 1 {
		t.Errorf("inactive watermark = %d, want the turn boundary 1", got)
	}

	w.set("m4", goodSummary, working)
	if got := w.sliceStart(working, 1); got != 3 {
		t.Errorf("active watermark = %d, want m4's index 3", got)
	}
}

func TestSliceStartIgnoresAStaleWatermark(t *testing.T) {
	working := msgs("m1", "m2", "m3", "m4", "m5")
	var w compactionWatermark
	w.set("m2", goodSummary, working)

	// A new user turn starts at m4, past where the old watermark points. The
	// watermark must not reach backwards and readmit the previous turn.
	if got := w.sliceStart(working, 3); got != 3 {
		t.Errorf("stale watermark = %d, want the newer turn boundary 3", got)
	}
}

func TestSliceStartIgnoresAnUnresolvableWatermark(t *testing.T) {
	// Between recording and the next fold, the ID is not in the slice. Falling
	// back to the turn boundary sends more than necessary for one step, which is
	// strictly better than sending a structurally invalid request.
	working := msgs("m1", "m2")
	var w compactionWatermark
	w.set("m9", goodSummary, working)

	if got := w.sliceStart(working, 1); got != 1 {
		t.Errorf("unresolvable watermark = %d, want the turn boundary 1", got)
	}
}

func TestPrependCompactionSummaryOpensWithAUserTurn(t *testing.T) {
	// The narrowed slice begins at an assistant message; every provider requires
	// the conversation to open with a user turn, and ensureStartsWithUser would
	// otherwise drop that assistant message and orphan its tool_result.
	in := []provider.ModelMessage{{Role: "assistant"}, {Role: "user"}}
	out := prependCompactionSummary(in, goodSummary)

	if len(out) != len(in)+1 {
		t.Fatalf("got %d messages, want %d", len(out), len(in)+1)
	}
	if out[0].Role != "user" {
		t.Errorf("leading role = %q, want user", out[0].Role)
	}
	var text string
	if err := json.Unmarshal(out[0].Content, &text); err != nil {
		t.Fatalf("leading content is not a JSON string: %v", err)
	}
	if !strings.Contains(text, goodSummary) {
		t.Error("summary text missing from the prepended message")
	}
	if !strings.Contains(text, "compacted") {
		t.Error("summary is unlabeled — the model would read it as a fresh user instruction")
	}
	if survived := ensureStartsWithUser(out); len(survived) != len(out) {
		t.Errorf("ensureStartsWithUser dropped %d message(s); the prepend failed its purpose", len(out)-len(survived))
	}
}

func TestPrependCompactionSummaryIsANoOpWithoutASummary(t *testing.T) {
	in := []provider.ModelMessage{{Role: "user"}}
	if out := prependCompactionSummary(in, ""); len(out) != 1 {
		t.Errorf("got %d messages, want the input untouched", len(out))
	}
}

func TestCanCompactContextTracksTheToolThatFillsContext(t *testing.T) {
	if !canCompactContext(BuildAgent) {
		t.Error("build agent should be offered compaction")
	}
	if !canCompactContext(PlanAgent) {
		t.Error("plan agent reads files too and should be offered compaction")
	}
	if canCompactContext(IndexAgent) {
		t.Error("index agent makes one structured call and has nothing to compact")
	}
}
