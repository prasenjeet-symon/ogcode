package agent

import (
	"encoding/json"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// compactionWatermark is the in-turn counterpart to the user-turn boundary that
// already narrows the model-facing history. It records how far into the current
// turn the agent has summarized its own work, so later steps stop re-sending
// what the summary now stands for.
//
// The zero value means nothing has been compacted this turn.
type compactionWatermark struct {
	// messageID is the assistant message that carried the compact_context call.
	// The watermark points AT that message rather than past it: its tool_call and
	// the tool_result that answers it must stay together, or the next request is
	// structurally invalid.
	messageID session.MessageID
	// summary is what the agent wrote in place of everything before messageID.
	summary string
}

// set records a new watermark.
//
// It deliberately does NOT require messageID to be present in the working set.
// The loop folds each iteration's new messages in at the top of the following
// iteration, so the assistant message that carried the compact_context call is
// written to the store but not yet in the slice when this is called. Requiring
// presence here would reject every real compaction. The ID is resolved lazily
// instead, by sliceStart, once the fold has happened.
//
// It refuses to move backwards when both watermarks are resolvable: a summary
// can only ever describe more of the turn than the one before it, so an earlier
// messageID would silently readmit history the agent already discarded.
func (w *compactionWatermark) set(messageID session.MessageID, summary string, messages []*session.MessageWithParts) bool {
	if messageID == "" || summary == "" {
		return false
	}
	if w.messageID != "" {
		prev, next := indexOfMessage(messages, w.messageID), indexOfMessage(messages, messageID)
		if prev >= 0 && next >= 0 && next <= prev {
			return false
		}
	}
	w.messageID = messageID
	w.summary = summary
	return true
}

// active reports whether anything has been compacted this turn.
func (w compactionWatermark) active() bool { return w.messageID != "" && w.summary != "" }

// indexOfMessage returns the position of a message in the working set, or -1
// when it is not present. Watermarks are stored as IDs rather than indices
// because the working set is refolded on every iteration, so an index captured
// on one step does not necessarily mean the same thing on the next.
func indexOfMessage(messages []*session.MessageWithParts, id session.MessageID) int {
	for i, m := range messages {
		if m.Info.ID == id {
			return i
		}
	}
	return -1
}

// sliceStart returns the index the model-facing history should begin at, given
// the turn boundary already computed by the caller. The later of the two wins:
// a watermark inside the current turn narrows further, and a watermark from a
// turn that has since ended is ignored rather than reaching backwards.
func (w compactionWatermark) sliceStart(messages []*session.MessageWithParts, turnStart int) int {
	if !w.active() {
		return turnStart
	}
	idx := indexOfMessage(messages, w.messageID)
	if idx > turnStart {
		return idx
	}
	return turnStart
}

// prependSummary puts the agent's summary in front of the narrowed history as a
// user message.
//
// It has to be a message rather than a system-prompt entry: the narrowed slice
// begins at the assistant message that called compact_context, and every
// provider requires the conversation to open with a user turn — ensureStartsWithUser
// would otherwise drop that assistant message and orphan the tool_result that
// answers it.
func prependCompactionSummary(messages []provider.ModelMessage, summary string) []provider.ModelMessage {
	if summary == "" {
		return messages
	}
	content, err := json.Marshal(compactionSummaryPreamble + summary)
	if err != nil {
		return messages
	}
	out := make([]provider.ModelMessage, 0, len(messages)+1)
	out = append(out, provider.ModelMessage{Role: "user", Content: content})
	return append(out, messages...)
}

// compactionSummaryPreamble labels the summary so the model reads it as its own
// compacted record of the turn rather than as a fresh instruction from the user.
const compactionSummaryPreamble = "[Earlier steps of this turn have been compacted to reclaim context. " +
	"The following is the summary you wrote of that work; the messages it replaces are no longer available to you.]\n\n"

// canCompactContext reports whether an agent is one whose context grows by
// accumulating tool output across steps, and which therefore has something to
// reclaim. The "read" tool is the discriminator: an agent that pulls file
// contents into its context is the one that fills it. Utility agents that make
// a single structured call have nothing to compact and should not be offered a
// tool that only adds a decision to make.
func canCompactContext(a Agent) bool { return a.HasTool("read") }

// proactiveCompactionBoundary returns the working-set index at which the
// model-facing history should resume after a proactive (size-triggered)
// compaction, or -1 when the current turn is too short to narrow safely.
//
// It keeps the last keep working-set messages verbatim and treats everything
// before the boundary as covered by the compaction summary. The boundary is
// pulled back to the nearest assistant message so the narrowed slice starts on
// the one front edge every provider accepts — an assistant message never
// depends on anything before it for tool-call pairing, whereas a tool result
// orphaned from its call is rejected. This is the same anchor the agent-driven
// compact_context watermark uses (it points at the assistant message that
// carried the call).
//
// Anchoring in working-set space rather than the summarizer's converted space
// is what makes it safe: convertMessages expands one working-set message into
// one OR MORE provider messages, so keeping the last keep working-set messages
// keeps at least keep provider messages — a superset of the last keep provider
// messages the summarizer kept verbatim. Everything the watermark then drops is
// therefore a subset of what the summary already covers; nothing goes missing.
func proactiveCompactionBoundary(messages []*session.MessageWithParts, turnStart, keep int) int {
	if turnStart < 0 || keep < 1 {
		return -1
	}
	b := len(messages) - keep
	if b <= turnStart {
		return -1
	}
	// Move the boundary earlier (keeping MORE recent context, never less) until
	// it lands on an assistant message. Bounded by turnStart, and assistant
	// messages are frequent — every step creates one — so this rarely moves far.
	for b > turnStart && messages[b].Info.Role != session.RoleAssistant {
		b--
	}
	if b <= turnStart {
		return -1
	}
	return b
}
