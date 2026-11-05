// Package agent: the lazy tools (the document tools and schedule) ride the wire
// only once the intent matcher names them for a plan step. These tests pin the
// three seams that make that work — the per-tool catalogue entry, the turn
// toolset that withholds and then offers a named tool, and the end-to-end loop
// where a real update_intent call puts exactly the named tools on the NEXT
// provider request.
package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	bus "github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// TestTurnToolsetWithholdsLazyTools pins the wire side: on a matcher turn the
// document and schedule ids are not offered at step one, each is offered mid-turn
// when the matcher names it, and an agent without the matcher keeps the whole
// allowlist exactly as it always did.
func TestTurnToolsetWithholdsLazyTools(t *testing.T) {
	allow := append([]string{"grep", "update_intent", "schedule"}, documentToolIDs()...)

	matcher := newTurnToolset(allow, true)
	if got := strings.Join(matcher.wireIDs(), ","); got != "grep,update_intent" {
		t.Fatalf("a matcher turn must withhold the lazy tools, got %q", got)
	}
	v0 := matcher.version
	matcher.offer("pdf_index")
	matcher.offer("schedule")
	matcher.offer("pdf_index") // duplicate: no-op
	matcher.offer("read")      // outside every offered family: refused
	if got := strings.Join(matcher.wireIDs(), ","); got != "grep,update_intent,pdf_index,schedule" {
		t.Errorf("offered lazy ids must land after the base, got %q", got)
	}
	if matcher.version != v0+2 {
		t.Errorf("two distinct offers must bump the version twice, got %d", matcher.version)
	}

	// Only the agent that runs the matcher narrows the wire.
	full := newTurnToolset(allow, false)
	for _, id := range allow {
		found := false
		for _, have := range full.wireIDs() {
			if have == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("an agent without the matcher lost %q from its wire", id)
		}
	}
}

// TestFormatIntentHintFollowsTheStepsGranularity pins the point of per-tool
// catalogue entries: what the wire gains follows what the step asked for. A
// spreadsheet step gets one tool, a PDF step gets the index/read pair, and a step
// that spans every document tool gets the whole set — the group is never implied.
func TestFormatIntentHintFollowsTheStepsGranularity(t *testing.T) {
	lr := &LoopRunner{Tools: tool.NewRegistry()}
	for _, id := range documentToolIDs() {
		lr.Tools.Register(fakeCatalogTool{id: id, desc: "document tool"})
	}
	catalogue := lr.intentCatalogue(Agent{ID: "build", Tools: documentToolIDs()}, lr.Dir)
	steps := []session.IntentStep{{Step: "Handle the paperwork", Status: session.IntentActive}}

	// One tool: querying a sheet needs query_sheet and nothing else.
	one := formatIntentHint(steps, catalogue, map[int][]string{0: {"query_sheet"}}, nil)
	if got := strings.Join(one.OfferIDs, ","); got != "query_sheet" {
		t.Errorf("a sheet step must offer only query_sheet, got %v", one.OfferIDs)
	}

	// Two: reading a PDF page brings the index that feeds it.
	two := formatIntentHint(steps, catalogue, map[int][]string{0: {"read_pdf_page"}}, nil)
	if got := strings.Join(two.OfferIDs, ","); got != "pdf_index,read_pdf_page" {
		t.Errorf("a PDF read must offer its pair, not the whole set, got %v", two.OfferIDs)
	}
	if !strings.Contains(two.Text, "read_pdf_page (tool)") {
		t.Errorf("hint does not label a named tool as a tool: %q", two.Text)
	}
	if got := two.Counts[0].Tools; got != 1 {
		t.Errorf("matched tools = %d, want the 1 tool the router named", got)
	}

	// The whole set: a step that names every document tool offers every one.
	all := formatIntentHint(steps, catalogue, map[int][]string{0: documentToolIDs()}, nil)
	if len(all.OfferIDs) != len(documentToolIDs()) {
		t.Fatalf("offered ids = %v, want all %d document tools", all.OfferIDs, len(documentToolIDs()))
	}
	for _, id := range documentToolIDs() {
		found := false
		for _, have := range all.OfferIDs {
			if have == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("document tool %q was not offered: %v", id, all.OfferIDs)
		}
	}
}

// TestIntentCatalogueListsLazyToolsOneByOne pins the catalogue side: every lazy
// tool the agent actually holds is its own entry, so the router can name one
// without implying the rest, and a tool the registry or the allowlist does not
// reach is never suggested.
func TestIntentCatalogueListsLazyToolsOneByOne(t *testing.T) {
	lr := &LoopRunner{Tools: tool.NewRegistry()}
	lr.Tools.Register(fakeCatalogTool{id: "pdf_index", desc: "pdf pages"})
	lr.Tools.Register(fakeCatalogTool{id: "schedule", desc: "reminder"})

	partial := Agent{ID: "build", Tools: append([]string{"schedule"}, documentToolIDs()...)}
	names := candidateNames(lr.intentCatalogue(partial, lr.Dir))
	if !strings.Contains(names, "pdf_index:tool") {
		t.Errorf("a held tool is missing from the catalogue: %v", names)
	}
	if !strings.Contains(names, "schedule:tool") {
		t.Errorf("a held lazy tool is missing from the catalogue: %v", names)
	}
	for _, absent := range []string{"docx_index", "latex_to_pdf", "query_sheet", "read_pdf_page"} {
		if strings.Contains(names, absent) {
			t.Errorf("a tool the registry does not hold was catalogued: %v", names)
		}
	}

	for _, id := range documentToolIDs() {
		lr.Tools.Register(fakeCatalogTool{id: id, desc: "document tool"})
	}
	names = candidateNames(lr.intentCatalogue(partial, lr.Dir))
	for _, id := range documentToolIDs() {
		if !strings.Contains(names, id+":tool") {
			t.Errorf("held tool %q is missing from the catalogue: %v", id, names)
		}
	}

	// The allowlist still gates it: a registry holding the tools says nothing
	// about an agent that was never offered them.
	headless := Agent{ID: "task", Tools: []string{"schedule"}}
	if names := candidateNames(lr.intentCatalogue(headless, lr.Dir)); strings.Contains(names, "pdf_index") {
		t.Errorf("a tool outside the allowlist was catalogued: %v", names)
	}
}

// TestLazyToolDescriptionsNameTheirArtifacts pins the matcher-facing side of a
// lazy tool's description: it is the only text the router reads, and it competes
// with skills and MCP servers on the words a plan step uses. A description that
// drops a file extension or a common alias silently stops matching the steps
// that name the tool's artifacts — the tool still works, it just never arrives.
// The tool's own ToolDef.Description() is a different text and is not pinned
// here; editing it changes nothing the router sees.
func TestLazyToolDescriptionsNameTheirArtifacts(t *testing.T) {
	want := map[string][]string{
		"docx_index":     {".docx", "Word"},
		"read_docx_page": {".docx", "Word"},
		"pdf_index":      {".pdf", "PDF"},
		"read_pdf_page":  {"PDF"},
		"latex_to_pdf":   {".tex", "LaTeX", "PDF"},
		"query_sheet":    {".csv", ".tsv", ".xlsx", ".xlsm", "Excel", "SQL"},
		"schedule":       {"reminder"},
	}
	if len(want) != len(lazyTools) {
		t.Fatalf("the pin covers %d tools, lazyTools has %d — add the new tool's description terms", len(want), len(lazyTools))
	}
	for _, lt := range lazyTools {
		terms, ok := want[lt.ID]
		if !ok {
			t.Errorf("lazy tool %q has no description pin", lt.ID)
			continue
		}
		for _, term := range terms {
			if !strings.Contains(lt.Description, term) {
				t.Errorf("%s: matcher description does not name %q, so a step about one will not match: %q", lt.ID, term, lt.Description)
			}
		}
	}
}

func candidateNames(cs []intentCandidate) string {
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		names = append(names, c.Name+":"+c.Kind)
	}
	return strings.Join(names, ",")
}

// TestProjectIndexPrompt_TellsTheBuildAgentToolsArriveOnDemand pins the prompt in
// step with the wire: the build agent's document rule must say the tools arrive
// on demand with a plan step, because step one no longer carries them. A note
// agent holds them from the first request, so the same sentence would be false
// there.
func TestProjectIndexPrompt_TellsTheBuildAgentToolsArriveOnDemand(t *testing.T) {
	build := projectIndexPrompt("build", true, true)
	if !strings.Contains(build, "arrive on the wire on demand") {
		t.Error("the build prompt does not say the document tools arrive on demand with a plan step")
	}
	if note := projectIndexPrompt("note", true, true); strings.Contains(note, "arrive on the wire") {
		t.Error("the note prompt promises an on-demand arrival, but that agent holds the tools from step one")
	}
}

// lazyDocProvider scripts one matcher turn: the first request answers with an
// update_intent call whose step describes a PDF, the routing completion (the
// only request that carries no tools) names read_pdf_page, and the rest finish.
// It records the tool names of every request that carried tools, so the test can
// watch exactly which tools arrive.
type lazyDocProvider struct {
	mu    sync.Mutex
	calls int
	names [][]string
}

func (p *lazyDocProvider) ID() string { return "mock" }

func (p *lazyDocProvider) Models() []provider.ModelInfo {
	return []provider.ModelInfo{{ID: "mock-model", ProviderID: "mock", ContextWindow: 128000}}
}

func (p *lazyDocProvider) StreamChat(_ context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	call := p.calls
	p.calls++
	routing := len(req.Tools) == 0
	if !routing {
		names := make([]string, 0, len(req.Tools))
		for _, def := range req.Tools {
			names = append(names, def.Name)
		}
		p.names = append(p.names, names)
	}
	p.mu.Unlock()

	ch := make(chan provider.StreamEvent, 8)
	go func() {
		defer close(ch)
		switch {
		case routing:
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: `{"matches":[{"step":0,"names":["read_pdf_page"]}]}`}
			fr := "stop"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
		case call == 0:
			callID := "intent_call"
			ch <- provider.StreamEvent{Type: provider.EventToolCallStart, ToolCallID: callID, ToolName: "update_intent"}
			input, _ := json.Marshal(map[string]any{"steps": []map[string]any{{
				"step":        "Read the invoice",
				"status":      session.IntentActive,
				"description": "Open invoice.pdf page by page and extract the line items",
			}}})
			ch <- provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCallID: callID, ToolInput: input}
			ch <- provider.StreamEvent{Type: provider.EventToolCallEnd, ToolCallID: callID}
			fr := "tool_use"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
		default:
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "done"}
			fr := "stop"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
		}
	}()
	return ch, nil
}

// TestRunLoop_PDFPairOfferedByMatcher is the end-to-end proof: a plan step that
// reads a PDF puts the PDF pair on the next provider request, while the rest of
// the document tools and schedule stay off it. The offer follows the step, not a
// fixed group.
func TestRunLoop_PDFPairOfferedByMatcher(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// Pre-seed the capability record so resolveImageSupport doesn't spend a
	// probe StreamChat call, which would offset the call counter.
	if err := session.SetModelCapability(database, &session.ModelCapability{ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now()}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &lazyDocProvider{}
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(browserIntentTool{}) // the noop update_intent the browser lazy test uses
	for _, id := range documentToolIDs() {
		tools.Register(fakeCatalogTool{id: id, desc: "document tool (test)"})
	}
	tools.Register(fakeCatalogTool{id: "schedule", desc: "reminder (test)"})

	lr := &LoopRunner{
		Store:    store,
		Bus:      bus.New(64),
		Registry: reg,
		Tools:    tools,
		Dir:      t.TempDir(),
		MaxSteps: 20,
	}

	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   "p",
		Directory:   t.TempDir(),
		Title:       "t",
		Model:       "mock-model",
		SessionType: "build",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	userMsg := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := store.CreateMessage(userMsg); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: "read the invoice PDF"})
	if err := store.CreatePart(&session.Part{ID: session.NewPartID(), MessageID: userMsg.ID, SessionID: sess.ID, Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now()}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}

	mock.mu.Lock()
	names := make([][]string, len(mock.names))
	copy(names, mock.names)
	mock.mu.Unlock()

	if len(names) != 2 {
		t.Fatalf("expected 2 tool-bearing requests (plan step + answer), got %d: %v", len(names), names)
	}

	has := func(wire []string, id string) bool {
		for _, n := range wire {
			if n == id {
				return true
			}
		}
		return false
	}

	first := names[0]
	if !has(first, "update_intent") {
		t.Errorf("the plan tool is missing from the matcher turn's first request; wire = %v", first)
	}
	for _, id := range append(documentToolIDs(), "schedule") {
		if has(first, id) {
			t.Errorf("lazy tool %q rode the wire before a plan step named one", id)
		}
	}

	second := names[1]
	for _, id := range []string{"pdf_index", "read_pdf_page"} {
		if !has(second, id) {
			t.Errorf("the PDF pair member %q is missing from the post-match wire; wire = %v", id, second)
		}
	}
	for _, id := range []string{"docx_index", "latex_to_pdf", "query_sheet", "read_docx_page", "schedule"} {
		if has(second, id) {
			t.Errorf("a tool the step never named rode the wire: %q; wire = %v", id, second)
		}
	}
}
