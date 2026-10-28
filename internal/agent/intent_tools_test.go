package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/skill"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// fakeCatalogTool stands in for an MCP tool. The matcher reads a tool's id,
// description, and optional MCPToolInfo metadata.
type fakeCatalogTool struct {
	id, desc               string
	server, serverDesc, fn string
}

func (f fakeCatalogTool) ID() string          { return f.id }
func (f fakeCatalogTool) Description() string { return f.desc }
func (f fakeCatalogTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (f fakeCatalogTool) Execute(context.Context, json.RawMessage, tool.Context) (tool.Result, error) {
	return tool.Result{}, nil
}
func (f fakeCatalogTool) MCPServer() string            { return f.server }
func (f fakeCatalogTool) MCPServerDescription() string { return f.serverDesc }
func (f fakeCatalogTool) MCPToolName() string          { return f.fn }

func TestParseIntentMatches(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[int][]string
	}{
		{"bare json", `{"matches":[{"step":1,"names":["a"]}]}`, map[int][]string{1: {"a"}}},
		{"fenced json", "```json\n{\"matches\":[{\"step\":0,\"names\":[\"a\",\"b\"]}]}\n```", map[int][]string{0: {"a", "b"}}},
		{"prose around the object", `Sure: {"matches":[{"step":2,"names":["x"]}]} — done.`, map[int][]string{2: {"x"}}},
		{"no braces", `I cannot help with that.`, nil},
		{"unparseable", `{"matches":`, nil},
		{"an entry with no names is skipped", `{"matches":[{"step":1,"names":[]},{"step":2,"names":["y"]}]}`, map[int][]string{2: {"y"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseIntentMatches(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseIntentMatches(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatIntentHintDropsUnknown(t *testing.T) {
	catalogue := []intentCandidate{
		{Kind: "skill", Name: "excel", Description: "read spreadsheets"},
		{Kind: "mcp", Name: "srv_a", Description: "does a", ToolIDs: []string{"mcp_srv_a_x", "mcp_srv_a_y"}},
		{Kind: "mcp", Name: "srv_b", Description: "does b", ToolIDs: []string{"mcp_srv_b_x"}},
		{Kind: "mcp", Name: "srv_c", Description: "does c", ToolIDs: []string{"mcp_srv_c_x"}},
		{Kind: "mcp", Name: "srv_d", Description: "does d", ToolIDs: []string{"mcp_srv_d_x"}},
	}
	steps := []session.IntentStep{{Step: "read the sheet", Status: session.IntentActive}}

	matches := map[int][]string{0: {"excel", "srv_a", "srv_b", "srv_c", "srv_d", "invented"}}
	hint := formatIntentHint(steps, catalogue, matches, nil)
	got, offered := hint.Text, hint.OfferIDs
	if !strings.Contains(got, "Possible tools for these steps") {
		t.Errorf("hint does not carry its lead-in: %q", got)
	}
	// A match must be acted on or explained — the hint is not a list to skip at
	// will, and it must not invite ignoring it.
	if !strings.Contains(got, "say in one line why it does not fit") {
		t.Errorf("hint does not ask for a reason when a match is skipped: %q", got)
	}
	if strings.Contains(got, "ignoring the list is fine") {
		t.Errorf("hint still invites ignoring the matches: %q", got)
	}
	if !strings.Contains(got, "excel (skill)") {
		t.Errorf("a skill match lost its label: %q", got)
	}
	if !strings.Contains(got, "srv_a (MCP server)") {
		t.Errorf("an MCP match lost its label: %q", got)
	}
	if !reflect.DeepEqual(offered, []string{"mcp_srv_a_x", "mcp_srv_a_y", "mcp_srv_b_x", "mcp_srv_c_x", "mcp_srv_d_x"}) {
		t.Errorf("offered = %v, want every tool of every matched server", offered)
	}
	if strings.Contains(got, "invented") {
		t.Errorf("a name the model invented was suggested: %q", got)
	}
	// There is no per-step cap: every catalogue entry the router named is
	// listed, not just the first few.
	if !strings.Contains(got, "srv_c (MCP server)") || !strings.Contains(got, "srv_d (MCP server)") {
		t.Errorf("matches beyond the old per-step cap were dropped: %q", got)
	}
	// The counts the header draws are over the names that survived the catalogue
	// lookup — an invented name is not a match — and the names kept are the ones
	// the header shows on hover.
	want := intentStepMatch{
		Skills: 1, MCPs: 4,
		SkillNames: []string{"excel"},
		MCPNames:   []string{"srv_a", "srv_b", "srv_c", "srv_d"},
	}
	if !reflect.DeepEqual(hint.Counts[0], want) {
		t.Errorf("counts[0] = %+v, want %+v", hint.Counts[0], want)
	}

	// A repeated name is listed once.
	dup := formatIntentHint(steps, catalogue, map[int][]string{0: {"srv_a", "srv_a", "excel"}}, nil).Text
	if n := strings.Count(dup, "srv_a (MCP server)"); n != 1 {
		t.Errorf("a repeated name appeared %d times, want 1: %q", n, dup)
	}

	// Nothing kept means no note at all.
	if got := formatIntentHint(steps, catalogue, nil, nil).Text; got != "" {
		t.Errorf("no matches produced a hint: %q", got)
	}
	if got := formatIntentHint(steps, catalogue, map[int][]string{0: {"invented"}}, nil).Text; got != "" {
		t.Errorf("only invented names produced a hint: %q", got)
	}
}

// TestFormatIntentHintOffersBrowserFamilyForTakeoverSkill pins the takeover
// trigger: a step the matcher names for browser-takeover puts the whole
// browser_* family on the wire and says so, while any other skill — or the
// same skill with no family to offer — offers nothing at all.
func TestFormatIntentHintOffersBrowserFamilyForTakeoverSkill(t *testing.T) {
	family := tool.BrowserToolIDs()
	catalogue := []intentCandidate{
		{Kind: "skill", Name: browserTakeoverSkillName, Description: "drive the user's Chrome"},
		{Kind: "skill", Name: "excel", Description: "read spreadsheets"},
	}
	steps := []session.IntentStep{{Step: "click through the app", Status: session.IntentActive}}

	hint := formatIntentHint(steps, catalogue, map[int][]string{0: {browserTakeoverSkillName}}, family)
	want := append([]string{}, family...)
	sort.Strings(want)
	if !reflect.DeepEqual(hint.OfferIDs, want) {
		t.Errorf("OfferIDs = %v, want the whole takeover family", hint.OfferIDs)
	}
	if !strings.Contains(hint.Text, browserTakeoverSkillName+" (skill)") {
		t.Errorf("hint does not name the matched takeover skill: %q", hint.Text)
	}
	if !strings.Contains(hint.Text, "already on the wire") {
		t.Errorf("hint does not say the takeover ops arrived: %q", hint.Text)
	}

	// A different skill offers nothing.
	if got := formatIntentHint(steps, catalogue, map[int][]string{0: {"excel"}}, family); len(got.OfferIDs) != 0 {
		t.Errorf("an unrelated skill offered %v", got.OfferIDs)
	}
	// No family to offer is knowledge only: the hint still names the skill.
	none := formatIntentHint(steps, catalogue, map[int][]string{0: {browserTakeoverSkillName}}, nil)
	if len(none.OfferIDs) != 0 {
		t.Errorf("an offerless agent was offered %v", none.OfferIDs)
	}
	if !strings.Contains(none.Text, browserTakeoverSkillName+" (skill)") {
		t.Errorf("takeover match lost its label with no family to offer: %q", none.Text)
	}
	if strings.Contains(none.Text, "already on the wire") {
		t.Errorf("an offerless agent was told the ops are on the wire: %q", none.Text)
	}
}

// TestSuggestIntentToolsOffersBrowserFamily pins the end-to-end path: a real
// routing completion naming the takeover skill, through the catalogue the
// agent actually holds (the embedded skills, plus a registry carrying only the
// sentinel), returns the takeover ids for the loop to offer.
func TestSuggestIntentToolsOffersBrowserFamily(t *testing.T) {
	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{
		textVerdict(fmt.Sprintf(`{"matches":[{"step":0,"names":[%q]}]}`, browserTakeoverSkillName)),
	}}
	lr, sessionID, _ := utilityLoopRunner(t, p)
	lr.Tools = tool.NewRegistry()
	lr.Tools.Register(fakeCatalogTool{id: "browser_status", desc: "reports pairing"})
	lr.Skills = skill.NewLoader(skill.Config{})

	agent := Agent{ID: "build", Tools: []string{"skill", "browser_status"}}
	steps := []session.IntentStep{{Step: "click through the app", Status: session.IntentActive, Description: "drive Chrome through the takeover flow"}}

	got, ids := lr.suggestIntentTools(context.Background(), sessionID, "mock-model", "", agent, lr.Dir, steps, newIntentFinder())
	if !strings.Contains(got, browserTakeoverSkillName+" (skill)") {
		t.Fatalf("hint does not name the matched takeover skill: %q", got)
	}
	if !strings.Contains(got, "already on the wire") {
		t.Errorf("hint does not say the takeover ops arrived: %q", got)
	}
	want := append([]string{}, tool.BrowserToolIDs()...)
	sort.Strings(want)
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("offered ids = %v, want the whole takeover family", ids)
	}

	// Outside the gate the takeover family is knowledge only: the hint still
	// names the matched skill, but nothing is offered to ride the wire.
	headless := Agent{ID: "task", Tools: []string{"skill"}}
	got, ids = lr.suggestIntentTools(context.Background(), sessionID, "mock-model", "", headless, lr.Dir, steps, newIntentFinder())
	if !strings.Contains(got, browserTakeoverSkillName+" (skill)") {
		t.Fatalf("headless hint does not name the matched takeover skill: %q", got)
	}
	if len(ids) != 0 {
		t.Errorf("a headless agent was offered %v", ids)
	}
	if strings.Contains(got, "already on the wire") {
		t.Errorf("a headless agent was told the ops are on the wire: %q", got)
	}
}

func TestIntentCatalogueFiltersAndGroupsByServer(t *testing.T) {
	lr := &LoopRunner{Tools: tool.NewRegistry()}
	lr.Tools.Register(fakeCatalogTool{
		id: "mcp_cal_com_list_bookings", desc: "list bookings",
		server: "cal_com", serverDesc: "Cal.com — Scheduling & calendar", fn: "list_bookings",
	})
	lr.Tools.Register(fakeCatalogTool{
		id: "mcp_cal_com_create_booking", desc: "create a booking",
		server: "cal_com", serverDesc: "Cal.com — Scheduling & calendar", fn: "create_booking",
	})
	lr.Tools.Register(fakeCatalogTool{id: "mcp_b/y", desc: "does y"})
	lr.Tools.Register(fakeCatalogTool{id: "bash", desc: "runs a command"})

	// The glob reaches the "cal_com" server only, collapses both of its tools into
	// a single server candidate, and excludes "bash" and "b".
	got := lr.intentCatalogue(Agent{ID: "build", Tools: []string{"mcp_cal_com_*"}}, t.TempDir())
	if len(got) != 1 || got[0].Name != "cal_com" || got[0].Kind != "mcp" {
		t.Fatalf("catalogue = %+v, want one grouped entry for cal_com", got)
	}
	if !reflect.DeepEqual(got[0].ToolIDs, []string{"mcp_cal_com_create_booking", "mcp_cal_com_list_bookings"}) {
		t.Errorf("ToolIDs = %v, want both cal_com tool IDs", got[0].ToolIDs)
	}
	if !strings.Contains(got[0].Description, "Cal.com — Scheduling & calendar") || !strings.Contains(got[0].Description, "list_bookings") {
		t.Errorf("Description = %q, want server handshake summary and tool names", got[0].Description)
	}

	// No skill loader wired, so a skill entry cannot appear.
	if lr.intentCatalogue(Agent{ID: "build", Tools: []string{"skill"}}, t.TempDir()) != nil {
		t.Error("a skill appeared with no skill loader configured")
	}
}

func TestSuggestIntentToolsAppendsAHint(t *testing.T) {
	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{
		textVerdict(`{"matches":[{"step":0,"names":["a"]}]}`),
	}}
	lr, sessionID, _ := utilityLoopRunner(t, p)
	lr.Tools = tool.NewRegistry()
	lr.Tools.Register(fakeCatalogTool{id: "mcp_a/x", desc: "does x"})
	lr.Tools.Register(fakeCatalogTool{id: "mcp_a/y", desc: "does y"})

	agent := Agent{ID: "build", Tools: []string{"mcp_*"}}
	steps := []session.IntentStep{
		{Step: "read the sheet", Status: session.IntentActive, Description: "open the xlsx and list its sheet names"},
		{Step: "tag the release", Status: session.IntentDone, Description: "cut a git tag on the release commit"},
	}

	finder := newIntentFinder()
	got, ids := lr.suggestIntentTools(context.Background(), sessionID, "mock-model", "", agent, lr.Dir, steps, finder)
	if !strings.Contains(got, "a (MCP server)") {
		t.Fatalf("hint does not name the matched server: %q", got)
	}
	// Matching an MCP server offers all of that server's tools on the wire; a
	// skill is reached through the skill tool, which the agent already holds.
	if !reflect.DeepEqual(ids, []string{"mcp_a/x", "mcp_a/y"}) {
		t.Errorf("offered ids = %v, want all tools of the matched MCP server", ids)
	}
	if !strings.Contains(got, "read the sheet") {
		t.Errorf("hint does not name the step it answers: %q", got)
	}
	if strings.Contains(got, "tag the release") {
		t.Errorf("a finished step was matched: %q", got)
	}
	// The answer's counts are kept per step, so the plan persisted straight after
	// this call can be stamped with them without routing again.
	key := intentStepIdentity(steps[0].Step)
	if got, want := finder.matched[key], (intentStepMatch{MCPs: 1, MCPNames: []string{"a"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("remembered counts = %+v, want %+v", got, want)
	}

	reqs := p.requests()
	if len(reqs) != 1 {
		t.Fatalf("routing calls = %d, want 1", len(reqs))
	}
	if reqs[0].Model != "mock-model" {
		t.Errorf("routed on model %q, want the session's model", reqs[0].Model)
	}
	if reqs[0].MaxTokens != 0 {
		t.Errorf("MaxTokens = %d, want it unset: matches are uncapped", reqs[0].MaxTokens)
	}
	// ModelMessage.Content must arrive as a text-parts array whose single text
	// part IS the payload object — never a raw JSON object (OpenAIProvider
	// unmarshals Content into `any` and serializes it verbatim as
	// `messages[].content`, so a raw object is rejected with 400 by providers)
	// and never a JSON string inside a JSON string.
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(reqs[0].Messages[0].Content), &parts); err != nil {
		t.Fatalf("message Content is not a text-parts array (%s): %v", reqs[0].Messages[0].Content, err)
	}
	if len(parts) != 1 || parts[0].Type != "text" {
		t.Fatalf("content parts = %+v, want exactly one text part", parts)
	}
	var payload struct {
		Steps []struct {
			Index       int    `json:"index"`
			Step        string `json:"step"`
			Description string `json:"description"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(parts[0].Text), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if len(payload.Steps) != 1 || payload.Steps[0].Index != 0 || payload.Steps[0].Description == "" {
		t.Errorf("payload steps = %+v, want only the active described step", payload.Steps)
	}
}

func TestSuggestIntentToolsNoops(t *testing.T) {
	agent := Agent{ID: "build", Tools: []string{"mcp_*"}}
	described := []session.IntentStep{{Step: "read the sheet", Status: session.IntentActive, Description: "open the xlsx"}}

	t.Run("empty catalogue spends nothing", func(t *testing.T) {
		p := &riskScriptProvider{scripts: [][]provider.StreamEvent{textVerdict(`{"matches":[{"step":0,"names":["x"]}]}`)}}
		lr, sessionID, _ := utilityLoopRunner(t, p)
		if got, _ := lr.suggestIntentTools(context.Background(), sessionID, "mock-model", "", agent, lr.Dir, described, nil); got != "" {
			t.Errorf("hint with no catalogue: %q", got)
		}
		if n := len(p.requests()); n != 0 {
			t.Errorf("a routing call was made with no catalogue: %d", n)
		}
	})

	t.Run("a plan with no descriptions spends nothing", func(t *testing.T) {
		p := &riskScriptProvider{scripts: [][]provider.StreamEvent{textVerdict(`{"matches":[{"step":0,"names":["mcp_a/x"]}]}`)}}
		lr, sessionID, _ := utilityLoopRunner(t, p)
		lr.Tools = tool.NewRegistry()
		lr.Tools.Register(fakeCatalogTool{id: "mcp_a/x", desc: "does x"})
		bare := []session.IntentStep{{Step: "read the sheet", Status: session.IntentActive}}
		if got, _ := lr.suggestIntentTools(context.Background(), sessionID, "mock-model", "", agent, lr.Dir, bare, nil); got != "" {
			t.Errorf("hint for an undescribed plan: %q", got)
		}
		if n := len(p.requests()); n != 0 {
			t.Errorf("a routing call was made with nothing to match: %d", n)
		}
	})

	t.Run("an unusable answer yields no hint", func(t *testing.T) {
		p := &riskScriptProvider{scripts: [][]provider.StreamEvent{textVerdict("I cannot help with that.")}}
		lr, sessionID, _ := utilityLoopRunner(t, p)
		lr.Tools = tool.NewRegistry()
		lr.Tools.Register(fakeCatalogTool{id: "mcp_a/x", desc: "does x"})
		if got, _ := lr.suggestIntentTools(context.Background(), sessionID, "mock-model", "", agent, lr.Dir, described, nil); got != "" {
			t.Errorf("hint from an unusable answer: %q", got)
		}
		if n := len(p.requests()); n != 1 {
			t.Errorf("routing calls = %d, want 1", n)
		}
	})
}

// TestTurnToolsetNarrowsUntilOffered pins the wire-narrowing at its source:
// the MCP and browser globs never expand onto a matcher turn's wire, an
// offered concrete tool is added once and in order, and a non-offerable id is
// refused. matcherTurn is the build agent's switch — an agent without the
// intent matcher keeps every glob on the wire from step one.
func TestTurnToolsetNarrowsUntilOffered(t *testing.T) {
	tt := newTurnToolset([]string{"grep", "mcp_*", "skill"}, true)
	if got := strings.Join(tt.wireIDs(), ","); got != "grep,skill" {
		t.Fatalf("the MCP glob must not reach the wire, got %q", got)
	}
	v0 := tt.version
	tt.offer("skill") // neither an MCP nor a browser id: refused
	if tt.version != v0 {
		t.Errorf("a non-offerable id must not bump the version")
	}
	tt.offer("mcp_b/two")
	tt.offer("mcp_a/one")
	tt.offer("mcp_a/one") // duplicate: no-op
	if got := strings.Join(tt.wireIDs(), ","); got != "grep,skill,mcp_a/one,mcp_b/two" {
		t.Errorf("offered ids must be deduped and sorted, got %q", got)
	}
	if tt.version != v0+2 {
		t.Errorf("two distinct offers must bump the version twice, got %d", tt.version)
	}

	// The takeover family narrows the same way: only the concrete browser_status
	// sentinel rides the wire, a concrete op is offered mid-turn, and a glob or
	// an id the base already carries is a no-op.
	matcher := newTurnToolset([]string{"grep", "browser_*", "browser_status"}, true)
	if got := strings.Join(matcher.wireIDs(), ","); got != "grep,browser_status" {
		t.Fatalf("on a matcher turn only the browser_status sentinel rides the wire, got %q", got)
	}
	v0 = matcher.version
	matcher.offer("browser_status") // already in base: no-op
	matcher.offer("browser_*")      // a glob, not a concrete id: refused
	matcher.offer("browser_click")
	matcher.offer("browser_click") // duplicate: no-op
	if got := strings.Join(matcher.wireIDs(), ","); got != "grep,browser_status,browser_click" {
		t.Errorf("offered takeover ops must land after the base, got %q", got)
	}
	if matcher.version != v0+1 {
		t.Errorf("one distinct takeover offer must bump the version once, got %d", matcher.version)
	}

	// Only the build agent narrows the wire. Every other agent has no plan to
	// read intent from, so its globs must stay exactly as the allowlist has them.
	full := newTurnToolset([]string{"grep", "mcp_*", "browser_*", "skill"}, false)
	if got := strings.Join(full.wireIDs(), ","); got != "grep,mcp_*,browser_*,skill" {
		t.Errorf("an agent without the matcher must keep the globs, got %q", got)
	}
	full.offer("browser_click") // the glob already carries it: no-op
	if full.version != 0 {
		t.Errorf("offering an id a base glob covers must not bump the version")
	}
	if got := strings.Join(full.wireIDs(), ","); got != "grep,mcp_*,browser_*,skill" {
		t.Errorf("an offer covered by a base glob must not land twice, got %q", got)
	}
}

// intentFinderRig wires a runner whose catalogue holds one MCP tool, so every
// suggestIntentTools call is a real routing completion unless the budget stops
// it. The scripted answer matches nothing: the tests count completions, not
// hints.
func intentFinderRig(t *testing.T) (*LoopRunner, session.SessionID, *riskScriptProvider, Agent) {
	t.Helper()
	p := &riskScriptProvider{scripts: [][]provider.StreamEvent{textVerdict(`{"matches":[]}`)}}
	lr, sessionID, _ := utilityLoopRunner(t, p)
	lr.Tools = tool.NewRegistry()
	lr.Tools.Register(fakeCatalogTool{id: "mcp_a/x", desc: "does x"})
	return lr, sessionID, p, Agent{ID: "build", Tools: []string{"mcp_*"}}
}

// TestIntentFinderSpendsOneCallPerNewStep pins the dedupe rule: a restated plan
// and an ordinary status flip cost nothing, and only a step the turn has not
// routed yet is worth another completion. There is no cap on how many such
// completions a turn may make.
func TestIntentFinderSpendsOneCallPerNewStep(t *testing.T) {
	lr, sessionID, p, agent := intentFinderRig(t)
	finder := newIntentFinder()
	route := func(steps []session.IntentStep) {
		t.Helper()
		lr.suggestIntentTools(context.Background(), sessionID, "mock-model", "", agent, lr.Dir, steps, finder)
	}

	sheet := []session.IntentStep{{Step: "read the sheet", Status: session.IntentActive, Description: "open the xlsx"}}
	route(sheet)
	if n := len(p.requests()); n != 1 {
		t.Fatalf("routing calls after the first plan = %d, want 1", n)
	}

	// The same plan restated, then the same step reaching active: the router's
	// answer is already in hand either way.
	route(sheet)
	route([]session.IntentStep{{Step: "read the sheet", Status: session.IntentPending, Description: "open the xlsx"}})
	if n := len(p.requests()); n != 1 {
		t.Errorf("a restated plan spent a routing call: %d", n)
	}

	// A step the turn has not routed yet is worth exactly one more call. A
	// description rewritten to record what the step produced is the same step
	// even while it is still open, so it must spend nothing — otherwise a long
	// turn burns its budget on prose and leaves genuinely new steps unmatchable.
	route([]session.IntentStep{{Step: "read the sheet", Status: session.IntentActive, Description: "read the sheet; opened the xlsx and listed 12 sheets"}})
	if n := len(p.requests()); n != 1 {
		t.Errorf("a rewritten description spent a routing call: %d", n)
	}

	// The closing revision is not routed at all (done steps are excluded), which
	// is why its counts have to travel with the step rather than the prose.
	route([]session.IntentStep{{Step: "read the sheet", Status: session.IntentDone, Description: "read the sheet; opened the xlsx and listed 12 sheets"}})
	if n := len(p.requests()); n != 1 {
		t.Errorf("a closing revision spent a routing call: %d", n)
	}
	route([]session.IntentStep{
		{Step: "read the sheet", Status: session.IntentDone, Description: "open the xlsx"},
		{Step: "tag the release", Status: session.IntentActive, Description: "cut a git tag on the release commit"},
	})
	if n := len(p.requests()); n != 2 {
		t.Errorf("a newly described step spent %d calls, want 2 total", n)
	}
}

// TestIntentFinderHasNoCallCap pins that routing calls are uncapped: however
// many distinct steps a turn introduces, each one is routed. Only a revision
// with nothing new is skipped.
func TestIntentFinderHasNoCallCap(t *testing.T) {
	lr, sessionID, p, agent := intentFinderRig(t)
	finder := newIntentFinder()
	const plans = 12
	for i := 0; i < plans; i++ {
		steps := []session.IntentStep{{
			Step:        fmt.Sprintf("step %d", i),
			Status:      session.IntentActive,
			Description: fmt.Sprintf("carry out step %d", i),
		}}
		lr.suggestIntentTools(context.Background(), sessionID, "mock-model", "", agent, lr.Dir, steps, finder)
	}
	if n := len(p.requests()); n != plans {
		t.Errorf("routing calls = %d, want one per new step (%d)", n, plans)
	}
}

// TestIntentFinderAnnotatesRestatedPlans pins what the plan chip reads: a
// restated plan keeps the counts its steps were routed with, and a step whose
// label was reworded starts from zero rather than inheriting its predecessor's
// counts.
func TestIntentFinderAnnotatesRestatedPlans(t *testing.T) {
	finder := newIntentFinder()
	routed := []session.IntentStep{{Step: "read the sheet", Status: session.IntentActive, Description: "open the xlsx"}}
	finder.remember(routed, map[int]intentStepMatch{0: {
		Skills:     1,
		MCPs:       2,
		SkillNames: []string{"update-ogcode"},
		MCPNames:   []string{"github"},
	}})

	// countsOf narrows a persisted step to the counts the chip shows, in a form
	// comparable now that a match also carries names.
	countsOf := func(s session.IntentStep) [2]int {
		return [2]int{s.MatchedSkills, s.MatchedMCPs}
	}

	// A status flip is the same step: its counts and names come back without
	// routing again.
	flipped := session.SessionIntent{Steps: []session.IntentStep{{Step: "read the sheet", Status: session.IntentDone, Description: "open the xlsx"}}}
	finder.annotate(&flipped)
	if got, want := countsOf(flipped.Steps[0]), [2]int{1, 2}; got != want {
		t.Errorf("a restated step read %v, want %v", got, want)
	}
	if got := flipped.Steps[0].MatchedSkillNames; len(got) != 1 || got[0] != "update-ogcode" {
		t.Errorf("a restated step's skill names read %v, want [update-ogcode]", got)
	}
	if got := flipped.Steps[0].MatchedMCPNames; len(got) != 1 || got[0] != "github" {
		t.Errorf("a restated step's MCP names read %v, want [github]", got)
	}

	// A model records an outcome by rewriting the description, and the closing
	// revision is not routed at all (done steps are excluded), so the counts
	// must follow the step label or they are lost exactly when the step closes.
	recorded := session.SessionIntent{Steps: []session.IntentStep{{Step: "read the sheet", Status: session.IntentDone, Description: "opened the xlsx and measured 1,204 rows"}}}
	finder.annotate(&recorded)
	if got, want := countsOf(recorded.Steps[0]), [2]int{1, 2}; got != want {
		t.Errorf("a step whose description was rewritten read %v, want %v", got, want)
	}

	// A reworded step has not been routed yet, so it must not look matched.
	reworded := session.SessionIntent{Steps: []session.IntentStep{{Step: "read the workbook", Status: session.IntentActive, Description: "open the xlsx"}}}
	finder.annotate(&reworded)
	if got, want := countsOf(reworded.Steps[0]), [2]int{}; got != want {
		t.Errorf("a reworded step read %v, want %v", got, want)
	}
	if got := (len(reworded.Steps[0].MatchedSkillNames) + len(reworded.Steps[0].MatchedMCPNames)); got != 0 {
		t.Errorf("a reworded step inherited %d matched names", got)
	}

	// An unrouted step and a nil finder both read zero rather than panicking.
	var none *intentFinder
	bare := session.SessionIntent{Steps: []session.IntentStep{{Step: "tag the release"}}}
	none.annotate(&bare)
	if got := countsOf(bare.Steps[0]); got != [2]int{} {
		t.Errorf("a nil finder stamped %v, want zero", got)
	}
}

// TestIntentFinderNilIsUnbudgeted keeps the escape hatch honest: a caller that
// passes no finder keeps the old unbudgeted behaviour.
func TestIntentFinderNilIsUnbudgeted(t *testing.T) {
	var finder *intentFinder
	steps := []intentStepIn{{Step: "read the sheet", Description: "open the xlsx"}}
	const plans = 8
	for i := 0; i < plans; i++ {
		if !finder.take(steps) {
			t.Fatalf("a nil finder refused call %d", i+1)
		}
	}
}
