package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

func TestLoopControl_PushDrainGuidance(t *testing.T) {
	lc := NewLoopControl()

	// Empty initially
	if lc.DrainGuidance() != "" {
		t.Error("expected empty guidance initially")
	}
	if lc.HasPendingGuidance() {
		t.Error("expected no pending guidance initially")
	}

	// Push and drain
	lc.PushGuidance("stop refactoring tests")
	lc.PushGuidance("just fix the build")

	if !lc.HasPendingGuidance() {
		t.Error("expected pending guidance after push")
	}

	got := lc.DrainGuidance()
	if got == "" {
		t.Fatal("expected non-empty guidance after drain")
	}
	if !strings.Contains(got, "stop refactoring tests") {
		t.Errorf("expected first guidance text in drained result, got %q", got)
	}
	if !strings.Contains(got, "just fix the build") {
		t.Errorf("expected second guidance text in drained result, got %q", got)
	}
	if !strings.Contains(got, "---") {
		t.Error("expected separator between multiple guidance texts")
	}

	// After drain, should be empty again
	if lc.HasPendingGuidance() {
		t.Error("expected no pending guidance after drain")
	}
	if lc.DrainGuidance() != "" {
		t.Error("expected empty guidance after drain")
	}
}

// TestLoopControl_DrainNoticesCapsAnInjection pins the burst bound: a flurry of
// reports must not crowd the task out of one request, and the notices past the
// cap stay queued for the next step instead of being dropped.
func TestLoopControl_DrainNoticesCapsAnInjection(t *testing.T) {
	lc := NewLoopControl()
	for i := 1; i <= 6; i++ {
		lc.PushNotice(Notice{Text: fmt.Sprintf("report %d", i), Cause: "teammate"})
	}

	batch := lc.DrainNotices()
	if len(batch) != maxNoticesPerInjection {
		t.Fatalf("drained %d notices, want the cap %d", len(batch), maxNoticesPerInjection)
	}
	for i, n := range batch {
		if want := fmt.Sprintf("report %d", i+1); n.Text != want {
			t.Fatalf("notice %d = %q, want %q (oldest first)", i, n.Text, want)
		}
	}
	if got := lc.PendingNoticeCount(); got != 2 {
		t.Fatalf("pending after one injection = %d, want the 2 past the cap", got)
	}
	if got := len(lc.DeliveredNotices()); got != maxNoticesPerInjection {
		t.Fatalf("delivered after one injection = %d, want %d", got, maxNoticesPerInjection)
	}

	rest := lc.DrainNotices()
	if len(rest) != 2 || rest[0].Text != "report 5" || rest[1].Text != "report 6" {
		t.Fatalf("second drain = %+v, want reports 5 and 6", rest)
	}
	if lc.HasPendingNotice() {
		t.Error("queue is not empty after draining every notice")
	}
}

// TestLoopControl_DrainNoticesByteCap keeps one huge report from evicting every
// other notice, but still lets it through on its own.
func TestLoopControl_DrainNoticesByteCap(t *testing.T) {
	lc := NewLoopControl()
	// Distinct texts on purpose: identical notices are deduped at push, so two
	// copies of the same string would never reach the byte cap.
	big := strings.Repeat("x", maxNoticeBytes-500)
	big2 := strings.Repeat("y", maxNoticeBytes-500)
	lc.PushNotice(Notice{Text: big})
	lc.PushNotice(Notice{Text: big2})
	lc.PushNotice(Notice{Text: "small"})

	batch := lc.DrainNotices()
	if len(batch) != 1 || batch[0].Text != big {
		t.Fatalf("byte-capped drain = %+v, want only the first report", batch)
	}

	lc = NewLoopControl()
	oversized := strings.Repeat("x", maxNoticeBytes*2)
	lc.PushNotice(Notice{Text: oversized})
	lc.PushNotice(Notice{Text: "small"})
	if batch := lc.DrainNotices(); len(batch) != 1 || batch[0].Text != oversized {
		t.Fatalf("drain with an oversized head = %+v, want it taken alone", batch)
	}
}

// TestLoopControl_AReportReadsFullOnceThenDigests pins the injection split:
// the step that first carries a notice reads it whole, and every later step of
// the turn carries only its headline — re-shipping the full report each step
// made the model re-acknowledge it at every step instead of working.
func TestLoopControl_AReportReadsFullOnceThenDigests(t *testing.T) {
	lc := NewLoopControl()
	lc.PushNotice(Notice{Text: "Teammate dev-1 is done — /session/a\n\nIts last message:\n\ndid the thing", Cause: "teammate"})

	// The loop drains at the top of a step, then builds the request.
	lc.DrainNotices()
	fresh, digest := lc.TakeNoticesForInjection()
	if len(digest) != 0 {
		t.Fatalf("first injection showed %d notices as digests, want none", len(digest))
	}
	if len(fresh) != 1 || fresh[0].Headline() != "Teammate dev-1 is done — /session/a" {
		t.Fatalf("first injection fresh = %+v, want the full report", fresh)
	}

	// A later step of the same turn: headline only, and nothing re-injects.
	fresh, digest = lc.TakeNoticesForInjection()
	if len(fresh) != 0 {
		t.Fatalf("repeat injection re-shipped %d fresh notices", len(fresh))
	}
	if len(digest) != 1 || digest[0].Headline() != "Teammate dev-1 is done — /session/a" {
		t.Fatalf("repeat injection digest = %+v, want one headline", digest)
	}
	if strings.Contains(digest[0].Headline(), "Its last message") {
		t.Errorf("digest carried more than the headline: %q", digest[0].Headline())
	}
}

// TestRestoreNoticesRequeuesFreshWithoutDigestLie pins the gap-restart
// contract the run loop depends on: a step takes the notices into its request
// tail BEFORE the request is built and sent, so a pre-stream restart is
// holding text it never sent. Restoring it must put it back on unshown, so
// the next take re-carries it in FULL — with a notice that arrives meanwhile
// behind it — and neither the digest nor the push dedup may swallow it.
func TestRestoreNoticesRequeuesFreshWithoutDigestLie(t *testing.T) {
	const fullA = "Teammate dev-1 is done — /session/a\n\nIts last message:\n\nrebuilt the auth flow end to end"
	lc := NewLoopControl()
	lc.PushNotice(Notice{Text: fullA, Cause: "teammate"})

	// Step 1: drain, then the request tail takes the notice out of unshown.
	lc.DrainNotices()
	take, digest := lc.TakeNoticesForInjection()
	if len(take) != 1 || len(digest) != 0 {
		t.Fatalf("first take = fresh %+v digest %+v, want the one notice as fresh", take, digest)
	}

	// The pre-stream gap restarts the step: what was taken was never sent.
	lc.RestoreNotices(take)

	// A new report arrives, and the restarted step's top-of-loop drain picks it
	// up behind the restored notice, the way a normal step queues its pushes.
	lc.PushNotice(Notice{Text: "Teammate dev-2 is done — /session/b", Cause: "teammate"})
	lc.DrainNotices()

	// The restarted step's take: the restored notice rides FIRST and IN FULL.
	fresh, digest := lc.TakeNoticesForInjection()
	if len(digest) != 0 {
		t.Fatalf("restarted take digested %d notices — the digest claims text that was never sent (%+v)", len(digest), digest)
	}
	if len(fresh) != 2 || fresh[0].Text != fullA || fresh[1].Text != "Teammate dev-2 is done — /session/b" {
		t.Fatalf("restarted take = %+v, want the restored notice ahead of the new one, both in full", fresh)
	}
	// And the wire for such a step has no digest block at all, while the
	// restored notice's detail survives it.
	wire := noticesUserContent(fresh, digest)
	if strings.Contains(wire, noticesDigestLabel) {
		t.Errorf("wire text carries the already-delivered digest block:\n%s", wire)
	}
	if !strings.Contains(wire, "rebuilt the auth flow end to end") {
		t.Errorf("wire text lost the restored notice's detail lines:\n%s", wire)
	}
	// DeliveredNotices was not touched by the restore: still the two, in
	// first-delivered order.
	if got := lc.DeliveredNotices(); len(got) != 2 || got[0].Text != fullA {
		t.Fatalf("deliveredNotices changed across a restore: %+v", got)
	}

	// A second gap restart in a row (take → restore again) must neither drop
	// nor duplicate: the same notices come back once, in order.
	lc.RestoreNotices(fresh)
	if got := lc.PendingNoticeCount(); got != 0 {
		t.Fatalf("restore leaked %d notices into the push queue", got)
	}
	fresh2, digest := lc.TakeNoticesForInjection()
	if len(fresh2) != 2 || fresh2[0].Text != fullA || len(digest) != 0 {
		t.Fatalf("re-restored take = fresh %+v digest %+v, want both notices fresh once", fresh2, digest)
	}

	// A push of text this turn has already carried is still deduped — that
	// guards against double-carrying a report that was really sent — and it
	// must not resurrect or drop anything already managed here.
	lc.PushNotice(Notice{Text: fullA})
	lc.PushNotice(Notice{Text: "Teammate dev-2 is done — /session/b"})
	if got := lc.PendingNoticeCount(); got != 0 {
		t.Fatalf("PushNotice re-delivered already-taken text %d times", got)
	}
	if fresh3, _ := lc.TakeNoticesForInjection(); len(fresh3) != 0 {
		t.Fatalf("push dedup resurrected the delivered notices: take = %+v", fresh3)
	}
}

// TestLoopControl_DeliveredNoticesAccumulate pins why delivered notices are
// kept: they are the dedup index PushNotice checks and the digest source the
// later steps reprint, in order.
func TestLoopControl_DeliveredNoticesAccumulate(t *testing.T) {
	lc := NewLoopControl()
	lc.PushNotice(Notice{Text: "first"})
	lc.DrainNotices()
	lc.PushNotice(Notice{Text: "second"})
	lc.DrainNotices()

	got := lc.DeliveredNotices()
	if len(got) != 2 || got[0].Text != "first" || got[1].Text != "second" {
		t.Fatalf("delivered = %+v, want both notices in order", got)
	}

	// The returned slice is a copy: mutating it must not rewrite the record.
	got[0].Text = "mutated"
	if lc.DeliveredNotices()[0].Text != "first" {
		t.Error("DeliveredNotices exposed the internal slice")
	}
}

// TestLoopControl_NoticeNilSafe covers the nil receiver a server can hold when
// no loop is running.
func TestLoopControl_NoticeNilSafe(t *testing.T) {
	var lc *LoopControl
	lc.PushNotice(Notice{Text: "hello"})
	if lc.DrainNotices() != nil {
		t.Error("nil DrainNotices returned notices")
	}
	if lc.DeliveredNotices() != nil {
		t.Error("nil DeliveredNotices returned notices")
	}
	if fresh, digest := lc.TakeNoticesForInjection(); fresh != nil || digest != nil {
		t.Error("nil TakeNoticesForInjection returned notices")
	}
	if lc.HasPendingNotice() {
		t.Error("nil HasPendingNotice returned true")
	}
	if lc.PendingNoticeCount() != 0 {
		t.Error("nil PendingNoticeCount returned a count")
	}
}

// TestNoticesUserContent keeps a teammate's report from reading as the user
// speaking, and keeps the headline digest out of the first injection.
func TestNoticesUserContent(t *testing.T) {
	if got := noticesUserContent(nil, nil); got != "" {
		t.Fatalf("empty notices rendered %q, want \"\"", got)
	}
	got := noticesUserContent([]Notice{{Text: "[Teammate dev-2]\ndone"}, {Text: "  [Teammate dev-3 stopped]  "}}, nil)
	if !strings.Contains(got, noticesLabel) {
		t.Error("notices block is missing its label")
	}
	if strings.Contains(got, guidanceLabel) {
		t.Error("notices block reuses the user-guidance label")
	}
	if strings.Contains(got, noticesDigestLabel) {
		t.Error("a first injection carried the digest label")
	}
	if !strings.Contains(got, "done") || !strings.Contains(got, "[Teammate dev-3 stopped]") {
		t.Fatalf("notices block dropped a notice: %q", got)
	}

	digest := noticesUserContent(nil, []Notice{{Text: "Teammate dev-1 is done — /session/a\n\nIts last message:\n\ndid the thing"}})
	if !strings.Contains(digest, noticesDigestLabel) {
		t.Error("digest block is missing its label")
	}
	if strings.Contains(digest, "did the thing") {
		t.Errorf("digest re-shipped the report body: %q", digest)
	}
	if !strings.Contains(digest, "Teammate dev-1 is done — /session/a") {
		t.Errorf("digest dropped the headline: %q", digest)
	}
}

func TestLoopControl_NilSafe(t *testing.T) {
	var lc *LoopControl
	// All methods should be nil-safe
	lc.PushGuidance("hello")
	if lc.DrainGuidance() != "" {
		t.Error("expected empty from nil LoopControl")
	}
	if lc.HasPendingGuidance() {
		t.Error("expected false from nil LoopControl")
	}
	if lc.CancelTool() {
		t.Error("expected false from nil CancelTool")
	}
	lc.SetToolCancel(func() {})
	lc.ClearToolCancel()
	if lc.CancelStream() {
		t.Error("expected false from nil CancelStream")
	}
	lc.SetStreamCancel(func() {})
	lc.ClearStreamCancel()
	if lc.CancelAll() {
		t.Error("expected false from nil CancelAll")
	}
}

func TestLoopControl_CancelTool(t *testing.T) {
	lc := NewLoopControl()

	// No tool running — CancelTool returns false
	if lc.CancelTool() {
		t.Error("expected CancelTool to return false when no tool is running")
	}

	// Register a cancel func
	cancelled := false
	lc.SetToolCancel(func() { cancelled = true })

	// CancelTool should call it and return true
	if !lc.CancelTool() {
		t.Error("expected CancelTool to return true when a tool is running")
	}
	if !cancelled {
		t.Error("expected cancel func to be called")
	}

	// After cancel, the stored func is cleared — second call returns false
	if lc.CancelTool() {
		t.Error("expected CancelTool to return false after cancel clears the func")
	}
}

func TestLoopControl_ClearToolCancel(t *testing.T) {
	lc := NewLoopControl()

	cancelled := false
	lc.SetToolCancel(func() { cancelled = true })
	lc.ClearToolCancel()

	// After ClearToolCancel, CancelTool returns false without calling
	if lc.CancelTool() {
		t.Error("expected CancelTool to return false after ClearToolCancel")
	}
	if cancelled {
		t.Error("expected cancel func NOT to be called after ClearToolCancel")
	}
}

func TestLoopControl_CancelStream(t *testing.T) {
	lc := NewLoopControl()

	// No stream running — CancelStream returns false
	if lc.CancelStream() {
		t.Error("expected CancelStream to return false when no stream is running")
	}

	// Register a cancel func
	cancelled := false
	lc.SetStreamCancel(func() { cancelled = true })

	// CancelStream should call it and return true
	if !lc.CancelStream() {
		t.Error("expected CancelStream to return true when a stream is running")
	}
	if !cancelled {
		t.Error("expected cancel func to be called")
	}

	// After cancel, the stored func is cleared — second call returns false
	if lc.CancelStream() {
		t.Error("expected CancelStream to return false after cancel clears the func")
	}
}

func TestLoopControl_ClearStreamCancel(t *testing.T) {
	lc := NewLoopControl()

	cancelled := false
	lc.SetStreamCancel(func() { cancelled = true })
	lc.ClearStreamCancel()

	// After ClearStreamCancel, CancelStream returns false without calling
	if lc.CancelStream() {
		t.Error("expected CancelStream to return false after ClearStreamCancel")
	}
	if cancelled {
		t.Error("expected cancel func NOT to be called after ClearStreamCancel")
	}
}

func TestLoopControl_CancelAll(t *testing.T) {
	lc := NewLoopControl()

	// Nothing running — CancelAll returns false
	if lc.CancelAll() {
		t.Error("expected CancelAll to return false when nothing is running")
	}

	// Register both
	streamCancelled := false
	toolCancelled := false
	lc.SetStreamCancel(func() { streamCancelled = true })
	lc.SetToolCancel(func() { toolCancelled = true })

	// CancelAll should call both
	if !lc.CancelAll() {
		t.Error("expected CancelAll to return true when work is running")
	}
	if !streamCancelled {
		t.Error("expected stream cancel func to be called")
	}
	if !toolCancelled {
		t.Error("expected tool cancel func to be called")
	}

	// After cancel, both are cleared
	if lc.CancelAll() {
		t.Error("expected CancelAll to return false after both are cancelled")
	}

	// Only stream running
	lc.SetStreamCancel(func() { streamCancelled = true })
	if !lc.CancelAll() {
		t.Error("expected CancelAll to return true when only stream is running")
	}

	// Only tool running
	lc.SetToolCancel(func() { toolCancelled = true })
	if !lc.CancelAll() {
		t.Error("expected CancelAll to return true when only tool is running")
	}
}

func TestWithLoopControl_ContextRoundtrip(t *testing.T) {
	lc := NewLoopControl()
	ctx := context.Background()

	// Without wrapping — returns nil
	if LoopControlFromContext(ctx) != nil {
		t.Error("expected nil LoopControl from un-wrapped context")
	}

	// With wrapping — returns the same pointer
	wrapped := WithLoopControl(ctx, lc)
	got := LoopControlFromContext(wrapped)
	if got != lc {
		t.Error("expected LoopControlFromContext to return the same LoopControl pointer")
	}
}

func TestGuidanceUserContent(t *testing.T) {
	got := guidanceUserContent("just fix the build")
	if !strings.Contains(got, "just fix the build") {
		t.Error("expected guidance text in content")
	}
	if !strings.Contains(got, guidanceLabel) {
		t.Error("expected guidance label in content")
	}
	if !strings.Contains(got, "Mid-loop guidance") {
		t.Error("expected guidance context phrase in content")
	}
}

// TestLoopControl_PushConcurrent verifies that PushGuidance and DrainGuidance
// are safe for concurrent use (the HTTP handler pushes while the loop drains).
func TestLoopControl_PushConcurrent(t *testing.T) {
	lc := NewLoopControl()
	done := make(chan struct{})

	// Concurrent pusher
	go func() {
		for i := 0; i < 100; i++ {
			lc.PushGuidance("guidance")
		}
		close(done)
	}()

	// Concurrent drainer
	drained := 0
	for i := 0; i < 100; i++ {
		if lc.DrainGuidance() != "" {
			drained++
		}
	}
	<-done

	// Drain whatever is left
	for {
		g := lc.DrainGuidance()
		if g == "" {
			break
		}
		drained++
	}

	// We should have drained roughly 100 times (some pushes may have been
	// coalesced into a single drain, but the total should be > 0).
	if drained == 0 {
		t.Error("expected at least one drain")
	}
}

// TestLoopControl_DeliveredGuidanceAccumulates verifies that DrainGuidance
// moves drained texts into the delivered accumulator, and that DeliveredGuidance
// returns the full accumulated set across multiple drains.
func TestLoopControl_DeliveredGuidanceAccumulates(t *testing.T) {
	lc := NewLoopControl()

	// Nothing delivered initially
	if lc.DeliveredGuidance() != "" {
		t.Error("expected empty delivered guidance initially")
	}

	// First drain
	lc.PushGuidance("first guidance")
	first := lc.DrainGuidance()
	if first != "first guidance" {
		t.Errorf("expected 'first guidance', got %q", first)
	}
	if got := lc.DeliveredGuidance(); got != "first guidance" {
		t.Errorf("expected delivered to contain 'first guidance', got %q", got)
	}

	// Second drain accumulates
	lc.PushGuidance("second guidance")
	lc.DrainGuidance()
	delivered := lc.DeliveredGuidance()
	if !strings.Contains(delivered, "first guidance") {
		t.Errorf("expected delivered to still contain 'first guidance', got %q", delivered)
	}
	if !strings.Contains(delivered, "second guidance") {
		t.Errorf("expected delivered to contain 'second guidance', got %q", delivered)
	}
	if !strings.Contains(delivered, "---") {
		t.Error("expected separator between accumulated guidance texts")
	}
}

// TestAppendGuidanceToUserMessage verifies that guidance text is appended to
// the first user text message's content with the proper label.
func TestAppendGuidanceToUserMessage(t *testing.T) {
	content, _ := json.Marshal("original user prompt")
	msgs := []provider.ModelMessage{
		{Role: "user", Content: content},
		{Role: "assistant", Content: []byte(`"assistant response"`)},
	}

	appendGuidanceToUserMessage(msgs, "stop and do X")

	var got string
	json.Unmarshal(msgs[0].Content, &got)
	if !strings.Contains(got, "original user prompt") {
		t.Error("expected original user text preserved")
	}
	if !strings.Contains(got, "stop and do X") {
		t.Error("expected guidance appended to user message content")
	}
	if !strings.Contains(got, guidanceLabel) {
		t.Error("expected guidance label in appended content")
	}

	// Assistant message must be untouched
	var assistantContent string
	json.Unmarshal(msgs[1].Content, &assistantContent)
	if assistantContent != "assistant response" {
		t.Errorf("expected assistant message untouched, got %q", assistantContent)
	}
}

// TestAppendGuidanceToUserMessage_NoUserMessage verifies the function is a
// no-op when there are no user messages (guidance is not silently injected
// into the wrong place).
func TestAppendGuidanceToUserMessage_NoUserMessage(t *testing.T) {
	msgs := []provider.ModelMessage{
		{Role: "assistant", Content: []byte(`"response"`)},
	}
	original := msgs[0].Content
	appendGuidanceToUserMessage(msgs, "some guidance")
	if !bytes.Equal(msgs[0].Content, original) {
		t.Error("expected no modification when no user message exists")
	}
}

// TestAppendGuidanceToUserMessage_SkipsCompactionSummaryPreamble verifies that
// when the model-facing slice has been narrowed by a compaction, guidance is
// appended to the user's real turn prompt — not to the synthetic
// compaction-summary message that prependCompactionSummary prepends at index 0.
// Otherwise the live mid-loop instruction would be buried inside the
// "[Earlier steps compacted...]" preamble and the model could misread it.
func TestAppendGuidanceToUserMessage_SkipsCompactionSummaryPreamble(t *testing.T) {
	preamble, _ := json.Marshal(compactionSummaryPreamble + goodSummary)
	turnPrompt, _ := json.Marshal("the original user prompt")
	msgs := []provider.ModelMessage{
		{Role: "user", Content: preamble}, // synthetic compaction summary
		{Role: "assistant", Content: []byte(`"working on it"`)},
		{Role: "user", Content: turnPrompt}, // the real turn prompt
	}

	appendGuidanceToUserMessage(msgs, "change direction")

	// The compaction-summary message must be untouched.
	var summaryContent string
	if err := json.Unmarshal(msgs[0].Content, &summaryContent); err != nil {
		t.Fatalf("preamble content is not a JSON string: %v", err)
	}
	if strings.Contains(summaryContent, guidanceLabel) {
		t.Error("guidance was attached to the compaction-summary preamble; " +
			"it should land on the real user turn prompt instead")
	}

	// The real turn prompt must carry the guidance.
	var turnContent string
	if err := json.Unmarshal(msgs[2].Content, &turnContent); err != nil {
		t.Fatalf("turn prompt content is not a JSON string: %v", err)
	}
	if !strings.Contains(turnContent, "the original user prompt") {
		t.Error("expected the original turn prompt to be preserved")
	}
	if !strings.Contains(turnContent, "change direction") {
		t.Error("expected guidance appended to the real user turn prompt")
	}
	if !strings.Contains(turnContent, guidanceLabel) {
		t.Error("expected the guidance label in the real turn prompt content")
	}
}
