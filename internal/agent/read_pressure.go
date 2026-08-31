package agent

import (
	"fmt"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// Read pressure is the volume of file, search, and page content the agent has
// pulled into the current turn. On an endpoint that does not cache a repeated
// prefix, every byte of it is re-billed on every remaining step, so a long
// reading phase is the single most expensive thing the agent does — and the one
// it is least likely to notice, because each individual read looks cheap.
//
// compact_context exists to reclaim exactly that space, but the agent has to
// remember to reach for it. This tracker watches the reading as it happens and,
// once it has crossed a threshold over several steps, attaches a reminder to the
// NEXT content-returning tool result — the moment the agent is demonstrably
// still in a reading phase and the advice can still change what it does.
const (
	// readPressureMinSteps is how many separate steps must have returned content
	// before a reminder is warranted. One enormous read is not a reading phase;
	// the point of compacting is that several steps' worth of material has piled
	// up and the earlier ones have already served their purpose.
	readPressureMinSteps = 3
	// readPressureMaxNudges bounds how often one turn is reminded. Past this the
	// agent has heard it and is either compacting or has decided not to; more
	// reminders would just be more context spent saying so.
	readPressureMaxNudges = 3
	// readPressureShareNumer/Denom set the reminder threshold as a share of the
	// turn's compaction threshold — nudge at roughly a third of the way to the
	// point where the loop would compact on the agent's behalf, so the agent
	// still has room to write a good summary before the expensive path fires.
	readPressureShareNumer = 30
	readPressureShareDenom = 100
	// readPressureCeilTokens caps the threshold for very large context windows.
	// The cost of re-sending accumulated reads is per-step and per-token; it does
	// not stop mattering just because the window is big.
	readPressureCeilTokens = 60000
	// readPressureFloorTokens keeps the threshold off zero for tiny windows,
	// where a handful of reads already overflows and the LLM-driven compaction
	// path is what actually saves the turn.
	readPressureFloorTokens = 2000
)

// readPressure accumulates read volume across the steps of one turn and decides
// when the agent should be reminded that compact_context exists.
//
// It counts tokens rather than bytes because tokens are what the endpoint bills
// and what the compaction threshold is denominated in; bytes are reported to the
// agent alongside them only because they are the more legible number.
type readPressure struct {
	// base is the threshold, in estimated tokens, at which the first reminder
	// arms. Derived from the turn's compaction threshold.
	base int
	// nextAt is the accumulated-token level the next reminder arms at. It moves
	// up after each one so a reminder is never repeated until substantially more
	// content has been read.
	nextAt int

	tokens    int // accumulated read tokens since the last reset
	bytes     int // the same content measured in bytes, for the reminder text
	readSteps int // steps since the last reset that returned content

	// offered mirrors whether compact_context is on this step's tool list. The
	// reminder names a tool, so it must never be attached on a step where the
	// agent was not given that tool.
	offered bool

	armed  bool // a reminder is due on the next content-returning result
	nudges int  // reminders attached since the last reset

	stepTokens int // content read during the step currently in flight
	stepBytes  int
	stepRead   bool // that step returned content from at least one tool
	stepNudged bool // a reminder was already attached during that step
}

// newReadPressure builds a tracker sized against the turn's compaction
// threshold, so the reminder lands at a point that means the same thing on a
// 32k local model and on a 200k hosted one.
func newReadPressure(compactionThreshold int) *readPressure {
	base := readPressureThresholdTokens(compactionThreshold)
	return &readPressure{base: base, nextAt: base}
}

// readPressureThresholdTokens converts a compaction threshold into the read
// volume that warrants a reminder: a share of it, bounded above so large windows
// still get reminded and below so the number stays meaningful, and never more
// than half the threshold — past that the loop's own compaction is imminent and
// a reminder is too late to be the cheaper option.
func readPressureThresholdTokens(compactionThreshold int) int {
	t := compactionThreshold * readPressureShareNumer / readPressureShareDenom
	if t > readPressureCeilTokens {
		t = readPressureCeilTokens
	}
	if half := compactionThreshold / 2; half > 0 && t > half {
		t = half
	}
	if t < readPressureFloorTokens {
		t = readPressureFloorTokens
	}
	return t
}

// setOffered records whether compact_context is on the tool list for the step
// about to run.
func (rp *readPressure) setOffered(offered bool) {
	if rp == nil {
		return
	}
	rp.offered = offered
}

// observe folds one completed tool result into the step currently in flight and,
// when a reminder is armed and this is the first content-returning result of the
// step, appends it to the result's output.
//
// It mutates res in place, deliberately, and before the result is written to the
// store: the model-facing history is rebuilt from the stored output on every
// step, so a reminder that is not part of that output is a reminder the model
// never sees.
func (rp *readPressure) observe(toolName string, res *tool.Result) {
	if rp == nil || res == nil || res.Denied {
		return
	}
	if !isContentReturningTool(toolName) {
		return
	}
	cost := estimateTokens(res.Output)
	if res.Image != nil {
		cost += imageTokenEstimate
	}
	if cost == 0 {
		return
	}
	rp.stepTokens += cost
	rp.stepBytes += len(res.Output)
	rp.stepRead = true

	if !rp.armed || rp.stepNudged || !rp.offered {
		return
	}
	res.Output += readPressureReminder(rp.tokens+rp.stepTokens, rp.bytes+rp.stepBytes, rp.readSteps+1)
	if res.Metadata == nil {
		res.Metadata = map[string]any{}
	}
	// Marks the appended text as harness-injected rather than something the tool
	// produced, so the UI can tell them apart.
	res.Metadata["contextPressureReminder"] = true
	rp.armed = false
	rp.stepNudged = true
	rp.nudges++
}

// endStep closes the step, adds its reading to the running total, and decides
// whether the next content-returning result should carry a reminder.
func (rp *readPressure) endStep() {
	if rp == nil {
		return
	}
	rp.tokens += rp.stepTokens
	rp.bytes += rp.stepBytes
	if rp.stepRead {
		rp.readSteps++
	}
	rp.stepTokens, rp.stepBytes, rp.stepRead, rp.stepNudged = 0, 0, false, false

	if rp.armed || rp.nudges >= readPressureMaxNudges {
		return
	}
	if rp.readSteps < readPressureMinSteps || rp.tokens < rp.nextAt {
		return
	}
	rp.armed = true
	// Re-arm well above the level that just fired, so the next reminder answers
	// genuinely new reading rather than the same pile measured again.
	rp.nextAt = rp.tokens + rp.base/2
}

// reset clears the accumulated pressure, and is called only when the turn's
// history has actually and durably shrunk: a compact_context watermark. The
// content it counted is no longer being sent, so continuing to count it would
// nag the agent about context it has already reclaimed.
//
// Deliberately NOT called from the loop's own two compaction paths. Those
// rewrite the messages of the single request about to go out; the next step
// rebuilds the turn from the store and every one of those reads comes straight
// back. Resetting there would clear the count for content that is still being
// re-sent — and silence the reminder at precisely the moment the turn has proved
// it is too large.
func (rp *readPressure) reset() {
	if rp == nil {
		return
	}
	rp.tokens, rp.bytes, rp.readSteps = 0, 0, 0
	rp.stepTokens, rp.stepBytes, rp.stepRead, rp.stepNudged = 0, 0, false, false
	rp.armed, rp.nudges = false, 0
	rp.nextAt = rp.base
}

// readPressureReminder is the text appended below the tool result. It reports
// what was measured — the agent should be able to check the claim, not just be
// told — and leaves the decision with the agent, because only the agent knows
// whether it is done with the material it has read.
func readPressureReminder(tokens, bytes, steps int) string {
	return fmt.Sprintf("\n\n<system-reminder>\n"+
		"Context pressure: %s of file, search, and page content has entered this turn "+
		"across %d reading steps (~%s tokens). This endpoint does not cache a repeated "+
		"prefix, so all of it is re-sent, and re-billed, on every remaining step of this turn.\n\n"+
		"Decide now, before reading anything else: if you have already taken what you need "+
		"from that material, call compact_context with a summary that carries every file "+
		"path, line range, conclusion, decision, and exact value you would otherwise have to "+
		"look up again — then continue the task from the summary. If you genuinely still need "+
		"the raw content in front of you, disregard this and carry on.\n"+
		"</system-reminder>", formatBytes(bytes), steps, formatCount(tokens))
}

// formatBytes renders a byte count the way a person reads one.
func formatBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// formatCount renders a token count as a short magnitude ("42k") rather than a
// precise-looking figure, since it is an estimate.
func formatCount(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// contentReturningTools are the built-ins whose results put external material
// into the context — file contents, search hits, indexes, fetched pages, a
// sub-agent's report. These are what a reading phase is made of.
//
// The list is an allowlist rather than an exclusion of the mutating tools,
// because getting it wrong in the exclusion direction is the worse failure: a
// tool wrongly counted inflates the measurement the reminder quotes at the
// agent, and a reminder that misstates its own evidence is worse than none.
var contentReturningTools = map[string]bool{
	"bash":                  true, // cat/sed/grep run through the shell read just as much
	"read":                  true,
	"file_map":              true,
	"codebase_map":          true,
	"glob":                  true,
	"grep":                  true,
	"deep_search":           true,
	"web_search":            true,
	"fetch_page":            true,
	"memory_recall":         true,
	"project_memory_recall": true,
	"pdf_index":             true,
	"read_pdf_page":         true,
	"docx_index":            true,
	"read_docx_page":        true,
	"view_image":            true,
	"skill":                 true,
	"task":                  true,
}

// isContentReturningTool reports whether a tool's result counts toward read
// pressure. MCP tools are included wholesale: they are unknown at build time and
// exist precisely to return outside content into the context.
func isContentReturningTool(name string) bool {
	if contentReturningTools[name] {
		return true
	}
	return strings.HasPrefix(name, "mcp_")
}
