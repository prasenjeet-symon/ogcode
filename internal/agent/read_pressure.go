package agent

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// readPressureThresholdEnv names the read volume (in estimated tokens) at which
// the reminder arms, overriding readPressureThresholdDefault.
const readPressureThresholdEnv = "OGCODE_READ_PRESSURE_THRESHOLD_TOKENS"

// resendCostMultipleEnv names the accumulated re-send cost, as a multiple of the
// model's context window, at which the cost trigger arms the same reminder.
const resendCostMultipleEnv = "OGCODE_RESEND_COST_WINDOW_MULTIPLE"

// Read pressure is the volume of file, search, and page content the agent has
// pulled into the current turn. All of it stays in the context: it crowds the
// model's reasoning and is re-sent on every remaining step. A long reading phase
// is thus both a drag on accuracy and, where the endpoint does not cache a
// repeated prefix, the most expensive thing the agent does — and the one it is
// least likely to notice, because each individual read looks cheap.
//
// compact_context exists to reclaim exactly that space, but the agent has to
// remember to reach for it. This tracker watches the reading as it happens and,
// once it has crossed a threshold over several steps, attaches a reminder to the
// NEXT content-returning tool result — the moment the agent is demonstrably
// still in a reading phase and the directive can still change what it does. The
// reminder is not advice: it keeps arriving until the agent compacts, because the
// turn that ignores it once is exactly the turn it exists for.
//
// Volume is only half the picture. Every step sends the whole context back to the
// model, so a turn that reads little but runs long still pays for all of its
// finished work on every remaining step — and on an endpoint that does not cache
// a repeated prefix, that re-send is the bill. So a second trigger watches the
// accumulated cost of re-sending (the sum of the context size across the steps so
// far) and arms the same reminder once it crosses a multiple of the model's
// window. The two triggers are independent: whichever crosses its own threshold
// first arms the reminder, and each re-arms on its own pacing, so one turn can be
// reminded for crowding, for cost, or for both, at different steps.
const (
	// readPressureMinSteps is how many separate steps must have returned content
	// before a reminder is warranted. One enormous read is not a reading phase;
	// the point of compacting is that several steps' worth of material has piled
	// up and the earlier ones have already served their purpose.
	readPressureMinSteps = 3
	// readPressureThresholdDefault is the read volume, in estimated tokens, at
	// which the reminder arms: 40k, independent of the model's context window.
	// Override with OGCODE_READ_PRESSURE_THRESHOLD_TOKENS.
	readPressureThresholdDefault = 40000
	// readPressureThresholdMin/Max clamp a configured threshold so a mistyped
	// magnitude cannot arm the reminder at ~zero or park it past any reading.
	readPressureThresholdMin = 2000
	readPressureThresholdMax = 1000000
	// resendCostMultipleDefault is the accumulated re-send cost, as a multiple of
	// the model's context window, at which the cost trigger first arms: 1. A turn
	// that has sent a full window's worth of input back has already paid to
	// re-send its whole finished context once, and every remaining step pays it
	// again — the point at which the re-send has become the bill rather than a
	// side effect. Override with OGCODE_RESEND_COST_WINDOW_MULTIPLE.
	resendCostMultipleDefault = 1
	// resendCostMultipleMin/Max clamp the override so a mistyped magnitude cannot
	// arm the cost trigger on the first step or park it past any turn.
	resendCostMultipleMin = 1
	resendCostMultipleMax = 1000
)

// readPressure accumulates two independent measurements of one turn — read
// volume and re-send cost — and decides when the agent must be told to compact
// the context. It keeps reminding (there is no cap) until a real compaction
// resets it, because the agent that ignores the reminder is the one it exists
// for.
//
// It counts tokens rather than bytes because tokens are what the endpoint bills
// and what the compaction threshold is denominated in; bytes are reported to the
// agent alongside them only because they are the more legible number.
type readPressure struct {
	// sessionID is the session this tracker belongs to. It is carried here only
	// so a reminder that fires is attributable in the logs to the turn it fired
	// in, where the compaction paths are already logged.
	sessionID session.SessionID
	// base is the threshold, in estimated tokens, at which the first reminder
	// arms. A standalone read volume, not derived from the compaction threshold.
	base int
	// nextAt is the accumulated-token level the next reminder arms at. It moves
	// up after each one so a reminder is never repeated until substantially more
	// content has been read.
	nextAt int
	// baseCost is the accumulated re-send cost, in input tokens, at which the
	// cost trigger first arms: a multiple of the model's context window
	// (resendCostBaseTokens). Window-relative, unlike base, because what it
	// measures is the context itself being sent again on every step.
	baseCost int
	// nextCostAt is the accumulated-cost level the next cost-triggered reminder
	// arms at, paced the same way as nextAt.
	nextCostAt int

	tokens    int // accumulated read tokens since the last reset
	bytes     int // the same content measured in bytes, for the reminder text
	readSteps int // steps since the last reset that returned content
	// cost is the input tokens the model has processed since the last reset —
	// the sum of every step's context size, which is what a turn actually pays
	// when a repeated prefix is not cached. steps counts the reported steps that
	// sum is spread over, for the reminder text.
	cost  int
	steps int

	// offered mirrors whether compact_context is on this turn's tool list. The
	// reminder names a tool, so it must never be attached when the agent was not
	// given that tool. Set once per turn (the loop resolves its toolset once, so
	// the answer cannot change between steps) and deliberately NOT cleared by
	// reset(), which only zeroes the accumulated volume.
	offered bool

	armed bool // a reminder is due on the next content-returning result
	// armedReason records which trigger armed the pending reminder, so the text
	// can say why it fired: a crowded context, or the cost of re-sending it.
	armedReason reminderReason

	stepTokens int // content read during the step currently in flight
	stepBytes  int
	stepRead   bool // that step returned content from at least one tool
	stepNudged bool // a reminder was already attached during that step
}

// newReadPressure builds a tracker, bound to sessionID for its log lines, that
// arms the compact_context reminder from two independent measurements of the
// turn. The volume trigger is a standalone read volume
// (readPressureThresholdTokens), not a share of where the loop's own compaction
// fires: it means the same thing on a 32k local model and on a 200k hosted one.
// The cost trigger is its opposite — resendCostBaseTokens(modelContextWindow) —
// because the cost of re-sending scales with the context, which scales with the
// window, so a flat threshold there would mean nothing.
func newReadPressure(sessionID session.SessionID, modelContextWindow int) *readPressure {
	base := readPressureThresholdTokens()
	baseCost := resendCostBaseTokens(modelContextWindow)
	return &readPressure{
		sessionID:  sessionID,
		base:       base,
		nextAt:     base,
		baseCost:   baseCost,
		nextCostAt: baseCost,
	}
}

// readPressureThresholdTokens returns the read volume, in estimated tokens, that
// warrants a reminder: OGCODE_READ_PRESSURE_THRESHOLD_TOKENS when it names a
// usable value, else the built-in 40k default. Read per turn, so a long-lived
// server picks up an override the same way any other env-configured setting
// applies, and so tests can drive it with t.Setenv.
func readPressureThresholdTokens() int {
	return envInt(readPressureThresholdEnv, readPressureThresholdDefault,
		readPressureThresholdMin, readPressureThresholdMax)
}

// resendCostBaseTokens returns the accumulated re-send cost, in input tokens, at
// which the cost trigger arms: OGCODE_RESEND_COST_WINDOW_MULTIPLE times the
// model's context window, that multiple defaulting to 1 and clamped into
// [resendCostMultipleMin, resendCostMultipleMax]. An unknown window falls back to
// fallbackMaxRequestTokens, the same stand-in the compaction threshold uses. Read
// per turn, like every other env-configured setting, so tests can drive it with
// t.Setenv.
func resendCostBaseTokens(modelContextWindow int) int {
	mult := envInt(resendCostMultipleEnv, resendCostMultipleDefault,
		resendCostMultipleMin, resendCostMultipleMax)
	if modelContextWindow <= 0 {
		modelContextWindow = fallbackMaxRequestTokens
	}
	return mult * modelContextWindow
}

// setOffered records whether compact_context is on the tool list for the turn
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
//
// A reminder armed by EITHER trigger lands here, so the cost trigger also only
// ever surfaces on a content-returning result — not on a write or an edit. A
// turn that only mutates waits until it reads again, which is when the summary
// it is being asked for would be worth writing.
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
	if rp.armedReason == reasonResendCost {
		res.Output += resendCostReminder(rp.cost, rp.steps)
	} else {
		res.Output += readPressureReminder(rp.tokens+rp.stepTokens, rp.bytes+rp.stepBytes, rp.readSteps+1)
	}
	if res.Metadata == nil {
		res.Metadata = map[string]any{}
	}
	// Marks the appended text as harness-injected rather than something the tool
	// produced, so the UI can tell them apart.
	res.Metadata["contextPressureReminder"] = true
	// The only trace of a reminder used to be inside the session data — the
	// appended text and the metadata flag beside it — so a turn that kept
	// ignoring it looked identical to one that had simply not read enough. Log
	// it, with the measurement it quotes, where the compaction paths already log.
	slog.Info("read-pressure reminder attached to tool result", "session", rp.sessionID,
		"tool", toolName, "reason", string(rp.armedReason),
		"tokens", rp.tokens+rp.stepTokens, "bytes", rp.bytes+rp.stepBytes,
		"steps", rp.readSteps+1, "thresholdTokens", rp.base,
		"resendCost", rp.cost, "resendThresholdTokens", rp.baseCost)
	rp.armed = false
	rp.stepNudged = true
}

// observeContext records the size of the request the model just processed — the
// exact input-side token count (Input + CacheRead + CacheWrite). That figure is
// one step's re-send cost: the whole context goes back to the model every step,
// so summing it across the turn is what the cost trigger watches, and the count
// of such steps is what the reminder quotes. Non-positive counts are ignored,
// which is also the first step of a turn where no previous response has reported
// a size yet.
func (rp *readPressure) observeContext(inputTokens int) {
	if rp == nil || inputTokens <= 0 {
		return
	}
	rp.cost += inputTokens
	rp.steps++
}

// endStep closes the step, adds its reading and its re-send cost to the running
// totals, and decides whether the next content-returning result should carry a
// reminder.
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

	if rp.armed {
		return
	}
	// Two independent conditions. The volume trigger still needs several reading
	// steps behind it, because one huge read is not a reading phase. The cost
	// trigger has no such floor: every step costs a full context re-send whether
	// or not it read anything, so a turn that runs long pays it either way.
	volumeDue := rp.readSteps >= readPressureMinSteps && rp.tokens >= rp.nextAt
	costDue := rp.cost >= rp.nextCostAt
	if !volumeDue && !costDue {
		return
	}
	rp.armed = true
	if costDue {
		rp.armedReason = reasonResendCost
	} else {
		rp.armedReason = reasonReadVolume
	}
	// Re-arm each trigger that just fired well above the level that armed it, so
	// the next reminder answers genuinely new pressure rather than the same pile
	// measured again. This pacing is the only thing spacing the reminders: they
	// keep arriving — half a threshold apart — until the agent compacts and
	// reset() starts the counts over. There is deliberately no cap after which the
	// turn is left un-reminded, because an ignored reminder is exactly the case it
	// exists for.
	if volumeDue {
		rp.nextAt = rp.tokens + rp.base/2
	}
	if costDue {
		rp.nextCostAt = rp.cost + rp.baseCost/2
	}
}

// reset clears the accumulated pressure, and is called only at a point where the
// turn's history has actually and durably shrunk. The content it counted is no
// longer being sent, so continuing to count it would nag the agent about context
// it has already reclaimed.
//
// There are two such points. The agent's own compact_context watermark is one.
// The loop's proactive compaction is the other, but only once it has advanced the
// watermark: that is what makes the fold durable across the rest of the turn,
// rather than a trim of a single request. The reactive, context-length path does
// NOT reset — it rewrites only the messages of the request about to go out, and
// the next step rebuilds the turn from the store with every one of those reads
// back. Resetting there would clear the count for content that is still being
// re-sent, silencing the reminder at the moment the turn has proved it is too
// large.
func (rp *readPressure) reset() {
	if rp == nil {
		return
	}
	rp.tokens, rp.bytes, rp.readSteps = 0, 0, 0
	rp.cost, rp.steps = 0, 0
	rp.stepTokens, rp.stepBytes, rp.stepRead, rp.stepNudged = 0, 0, false, false
	rp.armed = false
	rp.armedReason = reasonReadVolume
	rp.nextAt = rp.base
	rp.nextCostAt = rp.baseCost
}

// reminderReason records which measurement armed a reminder, so the text can say
// why it fired — a context crowded with finished reading, or the cost of sending
// that context back on every step.
type reminderReason string

const (
	reasonReadVolume reminderReason = "read-volume"
	reasonResendCost reminderReason = "resend-cost"
)

// compactDirective is the call to action every reminder ends with, whichever
// trigger armed it. It makes the call an obligation rather than a suggestion —
// once enough finished work is behind the turn, compacting is no longer the
// agent's option to weigh. The one exception is the case no measurement can see:
// every item still in context is in active use for the next action, so a summary
// would lose something still needed. Because the reminder re-arms rather than
// giving up, it keeps arriving until the agent compacts; the text says so.
const compactDirective = "You must call compact_context now, before reading anything else. Write a summary that " +
	"carries every file path, line range, conclusion, decision, and exact value you would " +
	"otherwise have to look up again, then continue the task from the summary. Do not hold " +
	"off to save tokens or to keep a cached prefix intact; a focused context is worth more.\n\n" +
	"There is exactly one reason to carry on without compacting: nothing finished is left in " +
	"your context — every item still in front of you is in active use for the immediate next " +
	"action, so a summary would lose something you still need. If that is not true, compact " +
	"now. This reminder will keep arriving until you do.\n"

// readPressureReminder is the text for the volume trigger. It reports what was
// measured — the agent should be able to check the claim, not just be told — so
// the crowding is named as the reason it fired.
func readPressureReminder(tokens, bytes, steps int) string {
	return fmt.Sprintf("\n\n<system-reminder>\n"+
		"Context pressure: %s of file, search, and page content has entered this turn "+
		"across %d reading steps (~%s tokens). All of it stays in your context — crowding "+
		"the reasoning you do from here, and re-sent on every remaining step of this turn.\n\n"+
		compactDirective+
		"</system-reminder>", formatBytes(bytes), steps, formatCount(tokens))
}

// resendCostReminder is the text for the cost trigger. It names the re-send cost
// as the reason — not crowding — so the agent can tell the two apart and knows
// the length of the turn, not the bulk of what it read, is what is being paid
// for.
func resendCostReminder(cost, steps int) string {
	return fmt.Sprintf("\n\n<system-reminder>\n"+
		"Re-send cost: the whole context has gone back to the model %d times this turn — "+
		"roughly %s tokens of input re-sent so far. Every further step sends all of it again, "+
		"so the finished part of this turn is being paid for over and over, and each remaining "+
		"step costs more than the last as the context grows.\n\n"+
		compactDirective+
		"</system-reminder>", steps, formatCount(cost))
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
