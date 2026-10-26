package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// The tool finder behind update_intent. When the agent states a plan, each step
// may carry a description of what it involves; this lines those steps up with
// the skills and MCP tools the agent holds, and folds a short list into the tool
// result the agent reads on its next step. The agent decides whether to load a
// skill or call a tool; for an MCP tool the finder also adds it to the wire for
// the rest of the turn, so a candidate it names is one the agent can call.
//
// The match is one LLM call over the whole plan, not one call per step: the
// model sees every step at once, which is what lets it tell a step that needs
// the PDF skill from one that does not, and it costs a single utility call
// rather than one per step. The result is per-step regardless — a step's names
// belong to that step alone, because the agent may rewrite the plan at any time.

// intentMatchLLMTimeout bounds the routing call, like the risk check.
const intentMatchLLMTimeout = 12 * time.Second

// intentRouteSystem is the router's system prompt. It is deliberately rigid: the
// answer is parsed, so a model that chats instead of answering yields no hint
// rather than a wrong one.
const intentRouteSystem = `You route a coding agent's plan steps to the tools that could carry them out. You are given the plan's steps and a catalogue of the skills and MCP tools the agent has. For each step, name the catalogue entries that would genuinely help with THAT step.

Rules:
- Answer with JSON only: {"matches":[{"step":<index>,"names":["<exact name>", ...]}]}
- Use names exactly as they appear in the catalogue. Never invent one.
- Include a step only when at least one entry clearly fits it. A wrong suggestion costs the agent real work, so leave a step out when nothing fits.
- Do not explain, and add no prose outside the JSON.`

// intentCandidate is one entry the matcher may pick: a skill the agent can load,
// or an MCP server whose tools it can call.
//
// For an MCP server, Name is the server name and ToolIDs holds the concrete
// "mcp_<server>_*" tool IDs registered for that server (absent from the router
// JSON payload via json:"-"). Grouping MCP tools by server keeps a 70-tool
// server (e.g. Cal.com) to a single compact catalogue entry, and matching that
// server offers all of its tools on the wire for the rest of the turn.
type intentCandidate struct {
	Kind        string   `json:"kind"` // "skill" or "mcp"
	Name        string   `json:"name"`
	Description string   `json:"description"`
	ToolIDs     []string `json:"-"`
}

// intentCatalogue collects the skills and MCP servers the agent actually holds —
// the only entries worth suggesting. A skill the agent cannot load, or an MCP
// tool its glob does not reach, would be a suggestion it could not act on.
// MCP tools are grouped by their owning server so the router sees one entry per
// server rather than one per tool.
func (lr *LoopRunner) intentCatalogue(agent Agent, workDir string) []intentCandidate {
	var out []intentCandidate
	if lr.Skills != nil && agent.HasTool("skill") {
		for _, sk := range lr.Skills.Load(workDir).Visible() {
			out = append(out, intentCandidate{Kind: "skill", Name: sk.Name, Description: sk.Description})
		}
	}
	if lr.Tools != nil {
		type serverGroup struct {
			name      string
			desc      string
			toolNames []string
			toolDescs []string
			toolIDs   []string
		}
		var order []string
		byServer := make(map[string]*serverGroup)
		for _, t := range lr.Tools.List() {
			id := t.ID()
			if !strings.HasPrefix(id, "mcp_") || !agent.HasTool(id) {
				continue
			}
			server, serverDesc, rawName := mcpToolGrouping(t)
			g, ok := byServer[server]
			if !ok {
				g = &serverGroup{name: server, desc: serverDesc}
				byServer[server] = g
				order = append(order, server)
			} else if g.desc == "" && serverDesc != "" {
				g.desc = serverDesc
			}
			g.toolNames = append(g.toolNames, rawName)
			if td := strings.TrimSpace(t.Description()); td != "" {
				g.toolDescs = append(g.toolDescs, td)
			}
			g.toolIDs = append(g.toolIDs, id)
		}
		sort.Strings(order)
		for _, srv := range order {
			g := byServer[srv]
			sort.Strings(g.toolNames)
			sort.Strings(g.toolIDs)
			out = append(out, intentCandidate{
				Kind:        "mcp",
				Name:        g.name,
				Description: formatMCPServerCandidateDesc(g.desc, g.toolNames, g.toolDescs),
				ToolIDs:     g.toolIDs,
			})
		}
	}
	return out
}

// maxMCPServerToolNames caps how many raw tool names are listed in a single MCP
// server's router description so a server with hundreds of tools stays compact.
const maxMCPServerToolNames = 40

// mcpToolGrouping returns the server name, server handshake summary, and raw
// tool name for an MCP tool. Real MCP tools implement tool.MCPToolInfo; tests
// that register bare ToolDefs fall back to parsing the "mcp_<server>/<tool>" or
// "mcp_<server>_<tool>" ID.
func mcpToolGrouping(t tool.ToolDef) (server, serverDesc, rawName string) {
	if info, ok := t.(tool.MCPToolInfo); ok && info.MCPServer() != "" {
		raw := info.MCPToolName()
		if raw == "" {
			raw = t.ID()
		}
		return info.MCPServer(), info.MCPServerDescription(), raw
	}
	rest := strings.TrimPrefix(t.ID(), "mcp_")
	if i := strings.IndexByte(rest, '/'); i > 0 {
		return rest[:i], "", rest[i+1:]
	}
	if i := strings.IndexByte(rest, '_'); i > 0 {
		return rest[:i], "", rest[i+1:]
	}
	return rest, "", rest
}

// formatMCPServerCandidateDesc builds the compact description shown to the
// intent router for one MCP server: its handshake summary (when present) plus
// the list of tool names it exposes. When a server has only a handful of tools
// and no handshake summary, the first tool descriptions are included for context.
func formatMCPServerCandidateDesc(serverDesc string, toolNames, toolDescs []string) string {
	names := toolNames
	extra := 0
	if len(names) > maxMCPServerToolNames {
		extra = len(names) - maxMCPServerToolNames
		names = names[:maxMCPServerToolNames]
	}
	toolsLine := "Tools: " + strings.Join(names, ", ")
	if extra > 0 {
		toolsLine += fmt.Sprintf(" (+%d more)", extra)
	}
	if serverDesc != "" {
		return serverDesc + ". " + toolsLine
	}
	if len(toolDescs) == 1 && len(toolNames) == 1 {
		return toolDescs[0] + " (" + toolsLine + ")"
	}
	return toolsLine
}

// intentStepIn is one described step as the router sees it. The status stays
// behind: it decided that the step belongs in the list, and the router has no
// use for it.
type intentStepIn struct {
	Index       int    `json:"index"`
	Step        string `json:"step"`
	Description string `json:"description"`
}

// describedSteps keeps the steps still ahead of the agent that carry a
// description — the only ones a suggestion can help with. A step already done or
// dropped does not need a tool, and a step with no description gives the router
// nothing to match against.
func describedSteps(steps []session.IntentStep) []intentStepIn {
	var out []intentStepIn
	for i, s := range steps {
		if s.Status != session.IntentPending && s.Status != session.IntentActive {
			continue
		}
		if strings.TrimSpace(s.Description) == "" {
			continue
		}
		out = append(out, intentStepIn{Index: i, Step: s.Step, Description: s.Description})
	}
	return out
}

// intentFinder remembers what a turn has already routed. A long turn restates
// its plan many times, and each restatement used to be another completion — on a
// provider that caps completions in flight (OGX answers 429 "too many
// completions in flight") that is a 429 for work the agent already had an answer
// for.
//
// Two rules keep it from re-asking the same question — neither caps how many
// calls a turn may make, so every genuinely new step is routed:
//
//   - A call happens only when the plan holds a step this turn has not routed
//     yet. A status flip (pending → active), or a step leaving the described set,
//     brings no new step, so ordinary progress spends nothing.
//   - Steps are marked as seen when the call is DISPATCHED, not when it
//     succeeds, so a failed call cannot turn every later revision into a retry
//     of the same request.
//
// A nil finder always agrees, so a caller that keeps no memory keeps the
// unbudgeted behaviour.
type intentFinder struct {
	seen map[string]bool
	// matched keeps each routed step's counts by step identity, so a restated plan
	// — which spends no new call — still shows what its steps matched, even when
	// the model rewrote a description to record what the step produced.
	matched map[string]intentStepMatch
}

func newIntentFinder() *intentFinder {
	return &intentFinder{seen: make(map[string]bool), matched: make(map[string]intentStepMatch)}
}

// take reports whether this plan revision holds a step the turn has not routed
// yet, and records those steps as routed.
func (f *intentFinder) take(steps []intentStepIn) bool {
	if f == nil {
		return true
	}
	fresh := false
	for _, s := range steps {
		if !f.seen[intentStepIdentity(s.Step)] {
			fresh = true
			break
		}
	}
	if !fresh {
		return false
	}
	for _, s := range steps {
		f.seen[intentStepIdentity(s.Step)] = true
	}
	return true
}

// intentStepIdentity identifies a step across plan revisions. It is the label the
// user reads, not the description that explains it: a model rewrites a
// description to record what the step produced ("Read X, Y and measured Z"), so
// keying on the prose made the closing revision of a step look like a brand-new
// one — its matches vanished from the chip, and the turn spent a routing call
// re-reading text that had not changed the step. A genuinely reworded step has a
// new label and is routed afresh. The status is deliberately absent: only a new
// or reworded step is worth routing again. Folding case and spacing keeps a
// cosmetic edit from counting as a reword.
func intentStepIdentity(label string) string {
	return strings.ToLower(strings.Join(strings.Fields(label), " "))
}

// remember files a routing answer's per-step counts under the step keys they
// belong to, so a later restatement of the same plan can show them again without
// spending another call. A nil finder keeps nothing.
func (f *intentFinder) remember(steps []session.IntentStep, counts map[int]intentStepMatch) {
	if f == nil || len(counts) == 0 {
		return
	}
	if f.matched == nil {
		f.matched = make(map[string]intentStepMatch)
	}
	for i, c := range counts {
		if i < 0 || i >= len(steps) {
			continue
		}
		f.matched[intentStepIdentity(steps[i].Step)] = c
	}
}

// annotate stamps what this turn has matched onto a plan revision before it is
// persisted, so the session header draws the counts beside each step and can
// name them on hover. A step the turn never routed reads zero, and a reworded
// step starts from zero again rather than inheriting its predecessor's counts.
func (f *intentFinder) annotate(intent *session.SessionIntent) {
	if intent == nil {
		return
	}
	for i := range intent.Steps {
		s := &intent.Steps[i]
		var m intentStepMatch
		if f != nil {
			m = f.matched[intentStepIdentity(s.Step)]
		}
		s.MatchedSkills = m.Skills
		s.MatchedMCPs = m.MCPs
		s.MatchedSkillNames = m.SkillNames
		s.MatchedMCPNames = m.MCPNames
	}
}

// suggestIntentTools runs one routing call over the plan's described steps and
// returns a note to append to the update_intent tool result, plus the concrete
// tools the matcher named (an MCP server's tools, and the whole browser_*
// takeover family when it named the browser-takeover skill). The note is ""
// when there is nothing to say — no catalogue, no described step, nothing new
// to route, no match, or any failure; the ids are what the caller then offers
// on the wire. Every failure is a no-op: the finder is a nudge, and losing it
// must never cost the turn.
func (lr *LoopRunner) suggestIntentTools(ctx context.Context, sessionID session.SessionID, model, providerID string, agent Agent, workDir string, steps []session.IntentStep, finder *intentFinder) (string, []string) {
	catalogue := lr.intentCatalogue(agent, workDir)
	if len(catalogue) == 0 {
		return "", nil
	}

	described := describedSteps(steps)
	if len(described) == 0 {
		return "", nil
	}
	if !finder.take(described) {
		return "", nil
	}

	var p provider.Provider
	if lr.Registry != nil {
		p = lr.Registry.ResolveProviderFor(model, providerID)
		if p == nil {
			p = lr.Registry.DefaultUsable()
		}
	}
	if p == nil {
		p = lr.DefaultProvider
	}
	if p == nil {
		return "", nil
	}

	payload, _ := json.Marshal(map[string]any{
		"steps":     described,
		"catalogue": catalogue,
	})
	userContent, _ := json.Marshal(string(payload))

	reqCtx, cancel := context.WithTimeout(ctx, intentMatchLLMTimeout)
	defer cancel()
	// MaxTokens is deliberately unset: the number of matches per step is
	// uncapped, so the provider's own output limit applies rather than a local
	// cap truncating a long answer.
	ch, err := p.StreamChat(reqCtx, provider.StreamRequest{
		Model:    model,
		System:   []string{intentRouteSystem},
		Messages: []provider.ModelMessage{{Role: "user", Content: userContent}},
	})
	if err != nil {
		slog.Warn("intent tool finder failed", "err", err)
		return "", nil
	}
	var out strings.Builder
	var usage *provider.TokenUsage
	reasoningChars := 0
	for evt := range ch {
		switch evt.Type {
		case provider.EventTextDelta:
			out.WriteString(evt.Text)
		case provider.EventReasoning:
			reasoningChars += len(evt.Text)
		case provider.EventUsage:
			usage = evt.Usage
		}
	}
	lr.recordUtilityUsage(sessionID, p.ID(), model, usage)

	matches := parseIntentMatches(out.String())
	if len(matches) == 0 {
		slog.Debug("intent tool finder found nothing", "session", sessionID, "steps", len(described), "reasoningChars", reasoningChars)
		return "", nil
	}
	hint := formatIntentHint(steps, catalogue, matches, browserTakeoverOfferIDs(lr, agent))
	finder.remember(steps, hint.Counts)
	return hint.Text, hint.OfferIDs
}

// browserTakeoverSkillName is the embedded walkthrough skill for driving the
// user's Chrome. Naming it in a plan step — or loading it through the skill
// tool — is what puts the browser_* takeover ops on the wire for the turn.
const browserTakeoverSkillName = "browser-takeover"

// browserTakeoverOfferIDs reports the takeover tool ids the agent may put on
// the wire: nil unless this is the interactive build agent whose registry
// actually holds the family (the same gate the loop uses to attach the glob).
func browserTakeoverOfferIDs(lr *LoopRunner, agent Agent) []string {
	if !agent.canControlBrowser() || lr.Tools == nil || lr.Tools.Get("browser_status") == nil {
		return nil
	}
	return tool.BrowserToolIDs()
}

// intentMatchResult is the router's JSON answer.
type intentMatchResult struct {
	Matches []struct {
		Step  int      `json:"step"`
		Names []string `json:"names"`
	} `json:"matches"`
}

// parseIntentMatches pulls the router's JSON object out of its answer and turns
// it into step index → proposed names. A bare or fenced JSON blob parses the
// same way, because the object is found by its braces; unparseable output
// yields nil, which the caller treats as "no suggestion".
func parseIntentMatches(text string) map[int][]string {
	i := strings.IndexByte(text, '{')
	j := strings.LastIndexByte(text, '}')
	if i < 0 || j <= i {
		return nil
	}
	var res intentMatchResult
	if err := json.Unmarshal([]byte(text[i:j+1]), &res); err != nil {
		return nil
	}
	out := make(map[int][]string)
	for _, m := range res.Matches {
		if len(m.Names) == 0 {
			continue
		}
		out[m.Step] = m.Names
	}
	return out
}

// intentStepMatch is what the matcher paired with one step. The counts are what
// the plan chip shows beside the step; the names ride along so hovering a count
// can say which skill or MCP server it stands for.
type intentStepMatch struct {
	Skills     int
	MCPs       int
	SkillNames []string
	MCPNames   []string
}

// intentHint is one routing answer after validation: the note to append to the
// tool result, the tools to offer on the wire (an MCP server's tools, plus the
// whole browser_* takeover family when the browser-takeover skill was named),
// and the per-step counts the loop stamps onto the persisted plan.
type intentHint struct {
	Text     string
	OfferIDs []string
	// Counts is keyed by the step's index in the plan.
	Counts map[int]intentStepMatch
}

// formatIntentHint turns the router's per-step names into the note appended to
// the tool result, and reports the tools it named so the caller can offer them
// on the wire. Names are resolved against the catalogue — a name the model
// invented is dropped rather than suggested — and a repeated name is listed
// once. Every surviving match is listed: there is no per-step cap, so a step
// that genuinely fits several skills and servers shows all of them. The counts
// and names it returns are over the matches that survived those filters.
// browserTools is the takeover family the agent may offer; when the router
// names the browser-takeover skill for a step, all of it is offered too.
func formatIntentHint(steps []session.IntentStep, catalogue []intentCandidate, matches map[int][]string, browserTools []string) intentHint {
	byName := make(map[string]intentCandidate, len(catalogue))
	for _, c := range catalogue {
		byName[c.Name] = c
	}
	var b strings.Builder
	offered := map[string]bool{}
	counts := map[int]intentStepMatch{}
	browserFamily := false
	for i, s := range steps {
		names := matches[i]
		if len(names) == 0 {
			continue
		}
		var picked []string
		var count intentStepMatch
		seen := map[string]bool{}
		for _, n := range names {
			c, ok := byName[n]
			if !ok || seen[n] {
				continue
			}
			seen[n] = true
			label := c.Name
			if c.Kind == "skill" {
				label += " (skill)"
				count.Skills++
				count.SkillNames = append(count.SkillNames, c.Name)
				if c.Name == browserTakeoverSkillName && len(browserTools) > 0 {
					// The plan naming the takeover skill is the turn deciding to
					// drive the user's Chrome: put the whole family on the wire
					// now rather than making the model load tools one op at a time.
					browserFamily = true
					for _, id := range browserTools {
						offered[id] = true
					}
				}
			} else {
				label += " (MCP server)"
				count.MCPs++
				count.MCPNames = append(count.MCPNames, c.Name)
				if len(c.ToolIDs) > 0 {
					for _, id := range c.ToolIDs {
						offered[id] = true
					}
				} else {
					offered[c.Name] = true
				}
			}
			picked = append(picked, label)
		}
		if len(picked) == 0 {
			continue
		}
		counts[i] = count
		fmt.Fprintf(&b, "- %s: %s\n", s.Step, strings.Join(picked, ", "))
	}
	if b.Len() == 0 {
		return intentHint{}
	}
	ids := make([]string, 0, len(offered))
	for id := range offered {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	text := "\n\nPossible tools for these steps — a matcher compared each step's description against the skills and MCP servers you can reach. An MCP server named here has all of its tools offered to you from now on; a skill is loaded through the \"skill\" tool. Use a match when it fits the step; if you skip one, say in one line why it does not fit rather than ignoring it silently.\n" + b.String()
	if browserFamily {
		text += "\nThe browser takeover tools are part of this app and are already on the wire for the rest of the turn; start with browser_status to check pairing before using them.\n"
	}
	return intentHint{
		Text:     text,
		OfferIDs: ids,
		Counts:   counts,
	}
}

// turnToolset is the set of tools offered on the wire for one turn.
//
// The agent's allowlist (Agent.Tools) is the authority on what it may call —
// executeTool checks it — and for the build agent it carries both the "mcp_*"
// and the "browser_*" glob. For the build agent the wire is narrower: a
// concrete MCP tool is offered only once the intent matcher has named it for
// the turn's plan, and the browser takeover ops are offered once the matcher
// names the browser-takeover skill (or the agent loads it) — browser_status
// stays on the wire from step one, so even a turn the matcher never routes can
// check whether the bridge is paired. Tools arrive with the task, not before
// it. Every other agent's turn list is its allowlist unchanged.
//
// version moves whenever the offered set changes, and the loop re-resolves the
// provider tool array on it exactly as it does on the registry's generation.
// Only RunLoop touches a turnToolset, so it needs no lock.
type turnToolset struct {
	base      []string
	suggested []string
	version   uint64
}

// newTurnToolset narrows an allowlist to the wire list a turn starts with.
//
// matcherTurn drops the globs whose expansion would be upfront knowledge the
// intent matcher keeps out: the "mcp_*" glob (ForAgent would expand it to every
// MCP tool the registry happens to hold) and the "browser_*" glob (the
// takeover ops ride the wire only once the family is offered). browser_status
// is concrete, so it is kept either way. Only an agent that runs the matcher
// can safely set it — with it false every id is kept, and the globs expand
// from the first step as they always did.
func newTurnToolset(allow []string, matcherTurn bool) *turnToolset {
	base := make([]string, 0, len(allow))
	for _, id := range allow {
		if matcherTurn && strings.Contains(id, "*") &&
			(strings.HasPrefix(id, "mcp_") || strings.HasPrefix(id, "browser_")) {
			continue
		}
		base = append(base, id)
	}
	return &turnToolset{base: base}
}

// offer adds one concrete tool to the turn's wire list — an MCP tool named by
// the intent matcher, or a browser takeover op surfaced when the matcher named
// the browser-takeover skill for the plan or the agent loaded that skill. A
// repeated id, a glob rather than a concrete id, an empty one, or an id the
// base already covers (exactly, or through a glob it carries) is a no-op, so
// a matcher naming the same tool for two steps costs nothing.
func (t *turnToolset) offer(id string) {
	if id == "" || strings.Contains(id, "*") {
		return
	}
	if !strings.HasPrefix(id, "mcp_") && !strings.HasPrefix(id, "browser_") {
		return
	}
	for _, have := range t.base {
		if have == id {
			return
		}
		if strings.HasSuffix(have, "*") && strings.HasPrefix(id, strings.TrimSuffix(have, "*")) {
			return
		}
	}
	for _, have := range t.suggested {
		if have == id {
			return
		}
	}
	t.suggested = append(t.suggested, id)
	sort.Strings(t.suggested)
	t.version++
}

// wireIDs is the list handed to ForAgent. It is a fresh copy each call, because
// the caller keeps it across steps.
func (t *turnToolset) wireIDs() []string {
	out := make([]string, 0, len(t.base)+len(t.suggested))
	out = append(out, t.base...)
	out = append(out, t.suggested...)
	return out
}
