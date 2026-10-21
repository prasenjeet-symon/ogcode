package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// descentMarker is the phrase the reminder is asserted by.
const descentMarker = "The search ran before the map did"

// A search that runs before anything has located files is the case the gate
// exists for: the map was never descended, so the model is reading matches
// without knowing which branch they belong to.
func TestDescentGateNudgesASearchBeforeTheDescent(t *testing.T) {
	for _, name := range []string{"grep", "glob"} {
		g := newDescentGate("sess", true)
		res := tool.Result{Output: "42 matches"}
		g.observe(name, json.RawMessage(`{"pattern":"capabilit"}`), &res)
		if !strings.Contains(res.Output, "The search ran before the map did") {
			t.Errorf("%s: reminder not attached: %q", name, res.Output)
		}
		if res.Metadata["descentReminder"] != true {
			t.Errorf("%s: reminder not marked as harness-injected: %v", name, res.Metadata)
		}
	}
}

// A project with no index has nothing to descend, so the reminder would send
// the agent to a tool that comes back empty.
func TestDescentGateIsQuietWithoutALiveIndex(t *testing.T) {
	g := newDescentGate("sess", false)
	res := tool.Result{Output: "42 matches"}
	g.observe("grep", nil, &res)
	if res.Output != "42 matches" {
		t.Errorf("reminder attached with no live index: %q", res.Output)
	}
}

// The root call is the map, not the descent: the labels it returns name folders
// to open. This is the call the screenshot opened with, immediately before the
// search that should have followed a subdir call.
func TestDescentGateRootMapIsNotADescent(t *testing.T) {
	g := newDescentGate("sess", true)
	root := tool.Result{Output: "5 folders, 20 files"}
	g.observe("codebase_map", json.RawMessage(`{}`), &root)
	res := tool.Result{Output: "42 matches"}
	g.observe("grep", nil, &res)
	if !strings.Contains(res.Output, "The search ran before the map did") {
		t.Errorf("root map should not satisfy the descent: %q", res.Output)
	}
}

func TestDescentGateADescentSilencesIt(t *testing.T) {
	g := newDescentGate("sess", true)
	mapped := tool.Result{Output: "internal/ — 40 files"}
	g.observe("codebase_map", json.RawMessage(`{"subdir":"internal"}`), &mapped)
	res := tool.Result{Output: "42 matches"}
	g.observe("grep", nil, &res)
	if res.Output != "42 matches" {
		t.Errorf("reminder attached after a subdir call: %q", res.Output)
	}
}

// A read or file_map names a file: the agent has a location, and a later search
// is about that file rather than a guess at the tree.
func TestDescentGateANamedFileCountsAsLocated(t *testing.T) {
	for _, name := range []string{"read", "file_map"} {
		g := newDescentGate("sess", true)
		first := tool.Result{Output: "package authz"}
		g.observe(name, json.RawMessage(`{"path":"internal/auth/authz.go"}`), &first)
		res := tool.Result{Output: "42 matches"}
		g.observe("grep", nil, &res)
		if res.Output != "42 matches" {
			t.Errorf("%s: reminder attached after a named file: %q", name, res.Output)
		}
	}
}

// One reminder per turn: a second adds context without adding a decision, and
// the agent has already been told what to do.
func TestDescentGateNudgesOncePerTurn(t *testing.T) {
	g := newDescentGate("sess", true)
	first := tool.Result{Output: "42 matches"}
	g.observe("grep", nil, &first)
	second := tool.Result{Output: "7 matches"}
	g.observe("grep", nil, &second)
	if strings.Contains(second.Output, "The search ran before the map did") {
		t.Errorf("reminder attached twice: %q", second.Output)
	}
}

// A call the permission gate refused never ran, so there is nothing to correct.
func TestDescentGateIgnoresDeniedCalls(t *testing.T) {
	g := newDescentGate("sess", true)
	res := tool.Result{Output: "denied", Denied: true}
	g.observe("grep", nil, &res)
	if res.Output != "denied" {
		t.Errorf("reminder attached to a denied call: %q", res.Output)
	}
}

// Nothing else in the turn — a bash build, an update_intent — is the gate's
// business, and none of them should consume the one reminder.
func TestDescentGateOnlyWatchesSearches(t *testing.T) {
	g := newDescentGate("sess", true)
	for _, name := range []string{"bash", "update_intent", "check_syntax", "write"} {
		res := tool.Result{Output: "ok"}
		g.observe(name, nil, &res)
		if res.Output != "ok" {
			t.Errorf("%s: reminder attached to an unrelated call: %q", name, res.Output)
		}
	}
	res := tool.Result{Output: "42 matches"}
	g.observe("grep", nil, &res)
	if !strings.Contains(res.Output, "The search ran before the map did") {
		t.Error("an unrelated call consumed the reminder")
	}
}

// The subdir is read from the call's own arguments, so a malformed payload
// cannot be mistaken for a descent.
func TestCodebaseMapSubdir(t *testing.T) {
	cases := map[string]string{
		``:                                "",
		`{}`:                              "",
		`{"subdir":"internal/tool"}`:      "internal/tool",
		`{"subdir":"internal","other":1}`: "internal",
		`{"subdir":}`:                     "",
		`null`:                            "",
	}
	for in, want := range cases {
		if got := codebaseMapSubdir(json.RawMessage(in)); got != want {
			t.Errorf("codebaseMapSubdir(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRunLoop_DescentReminderReachesTheModel is the wiring test: the reminder is
// only worth anything if it survives into the request the provider actually
// receives. The loop rebuilds the model-facing history from the store on every
// step, so a reminder that was not written into the stored tool output would be
// dropped — invisible to every unit test above.
func TestRunLoop_DescentReminderReachesTheModel(t *testing.T) {
	reqs := runDescentScript(t, 2, 1098)
	if len(reqs) < 2 {
		t.Fatalf("only %d requests made; the script did not run a search round", len(reqs))
	}
	// The request after the first search round carries the reminder.
	if got := countMarker(reqs[1], descentMarker); got != 1 {
		t.Errorf("%d descent reminders in the request after the first search, want 1", got)
	}
	// One per turn: a second search in the same turn adds nothing the model has
	// not already been told.
	if got := countMarker(reqs[len(reqs)-1], descentMarker); got != 1 {
		t.Errorf("%d descent reminders in the final request after two search rounds, want 1", got)
	}
}

// A project with no index has nothing to descend, so the loop must not arm the
// gate however many searches the turn runs.
func TestRunLoop_DescentReminderStaysOffWithoutAnIndex(t *testing.T) {
	reqs := runDescentScript(t, 2, 0)
	if len(reqs) < 2 {
		t.Fatalf("only %d requests made; the script did not run a search round", len(reqs))
	}
	if got := countMarker(reqs[len(reqs)-1], descentMarker); got != 0 {
		t.Errorf("%d descent reminders with no index, want 0", got)
	}
}

// runDescentScript drives a turn of `rounds` grep rounds in a project whose index
// reports `indexed` files, and returns every request made.
func runDescentScript(t *testing.T, rounds, indexed int) [][]provider.ModelMessage {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := session.SetModelCapability(database, &session.ModelCapability{
		ModelID: "mock-model", SupportsImages: false, ProbedAt: session.Now(),
	}); err != nil {
		t.Fatalf("set capability: %v", err)
	}

	store := session.NewStore(database)
	reg := provider.NewRegistry()
	mock := &toolRoundsProvider{rounds: rounds}
	reg.Register(mock)

	tools := tool.NewRegistry()
	tools.Register(noopGrepTool{})

	lr := &LoopRunner{
		Store: store, Bus: bus.New(256), Registry: reg, Tools: tools,
		Dir: t.TempDir(), MaxSteps: 40,
		IndexedFileCount: func(string) int { return indexed },
	}
	sess := &session.Session{
		ID: session.NewSessionID(), ProjectID: "p", Directory: t.TempDir(),
		Title: "t", Model: "mock-model", SessionType: "build",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}
	if err := store.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	userMsg := &session.MessageInfo{ID: session.NewMessageID(), SessionID: sess.ID, Role: session.RoleUser, CreatedAt: session.Now()}
	if err := store.CreateMessage(userMsg); err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: "find every call site"})
	if err := store.CreatePart(&session.Part{
		ID: session.NewPartID(), MessageID: userMsg.ID, SessionID: sess.ID,
		Type: session.PartText, Data: textData, CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user part: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- lr.RunLoop(context.Background(), sess.ID, "build", 0, 0) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop returned error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("RunLoop did not complete in time")
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	return mock.messages
}
