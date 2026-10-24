package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/board"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
)

// permissionTreeServer is a server with a board and a permission manager over
// the shape that matters here: a user-facing session ("sess-parent") whose
// teammate t1 ("sess-child") spawned a teammate of its own, t2 ("sess-grand").
func permissionTreeServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	srv := boardTestServer(t)
	srv.permissions = permission.NewManager(nil)
	registerBoardAgent(t, srv, board.Handle{
		Directory: srv.dir, Handle: "parent", SessionID: "sess-parent",
		Role: "build", Kind: board.KindSession, Status: board.StatusRunning,
	})
	registerBoardAgent(t, srv, board.Handle{
		Directory: srv.dir, Handle: "t1", SessionID: "sess-child",
		Role: "build", Kind: board.KindTeammate, SpawnedBy: "parent", Status: board.StatusRunning,
	})
	registerBoardAgent(t, srv, board.Handle{
		Directory: srv.dir, Handle: "t2", SessionID: "sess-grand",
		Role: "build", Kind: board.KindTeammate, SpawnedBy: "t1", Status: board.StatusRunning,
	})
	return srv, srv.routes()
}

func listPermissions(t *testing.T, h http.Handler, sessionID string) []permission.Request {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session/"+sessionID+"/permission", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET permission = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got []permission.Request
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode permissions: %v (body: %s)", err, rec.Body.String())
	}
	return got
}

// A teammate's pending request must appear in the queue of the session the
// human is watching, at any nesting depth, labelled with the teammate that
// raised it — otherwise the only way to answer it is to open the teammate's own
// page, which nothing points at.
func TestParentSessionListsTeammatePermissions(t *testing.T) {
	srv, h := permissionTreeServer(t)
	srv.permissions.Create(permission.Request{
		ID: "child-perm", SessionID: "sess-child", Tool: "bash",
		Input: `{"command":"rm -rf build"}`, Patterns: []string{"rm -rf build"},
	})
	srv.permissions.Create(permission.Request{
		ID: "grand-perm", SessionID: "sess-grand", Tool: "write",
		Input: `{}`, Patterns: []string{"main.go"},
	})

	byID := map[permission.PermissionID]permission.Request{}
	for _, req := range listPermissions(t, h, "sess-parent") {
		byID[req.ID] = req
	}
	if len(byID) != 2 {
		t.Fatalf("parent queue = %+v, want both teammates' requests", byID)
	}
	if got := byID["child-perm"].Teammate; got != "t1" {
		t.Fatalf("child request teammate = %q, want t1", got)
	}
	if got := byID["child-perm"].SessionID; got != "sess-child" {
		t.Fatalf("child request session = %q, want sess-child (the blocked loop)", got)
	}
	if got := byID["grand-perm"].Teammate; got != "t2" {
		t.Fatalf("grandchild request teammate = %q, want t2", got)
	}
}

// A session with no teammates lists only its own requests — the feature must
// not widen the queue of an ordinary session.
func TestSessionWithoutTeammatesListsOnlyItsOwn(t *testing.T) {
	srv, h := permissionTreeServer(t)
	// sess-grand spawned nobody, and sess-lonely never joined the board at all.
	srv.permissions.Create(permission.Request{ID: "own", SessionID: "sess-grand", Tool: "bash", Patterns: []string{"make test"}})
	srv.permissions.Create(permission.Request{ID: "parents", SessionID: "sess-parent", Tool: "bash", Patterns: []string{"make build"}})
	srv.permissions.Create(permission.Request{ID: "lonely", SessionID: "sess-lonely", Tool: "bash", Patterns: []string{"ls"}})

	if got := listPermissions(t, h, "sess-grand"); len(got) != 1 || got[0].ID != "own" {
		t.Fatalf("leaf teammate = %+v, want only its own request", got)
	}
	if got := listPermissions(t, h, "sess-lonely"); len(got) != 1 || got[0].ID != "lonely" {
		t.Fatalf("unboarded session = %+v, want only its own request", got)
	}
	if got := listPermissions(t, h, "nobody"); len(got) != 0 {
		t.Fatalf("unknown session = %+v, want none", got)
	}
}

// Answering a teammate's prompt from the parent's composer must reach the
// teammate's blocked loop and clear BOTH queues: the one that showed the prompt
// and the one the teammate owns.
func TestReplyingFromTheParentUnblocksTheTeammate(t *testing.T) {
	srv, h := permissionTreeServer(t)
	events := srv.bus.SubscribeAll()
	pr := srv.permissions.Create(permission.Request{
		ID: "child-perm", SessionID: "sess-child", Tool: "bash", Patterns: []string{"make test"},
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/session/sess-parent/permission/child-perm", strings.NewReader(`{"response":"once"}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST reply = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	select {
	case got := <-pr.ReplyCh:
		if got != "once" {
			t.Fatalf("teammate's loop got %q, want once", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("teammate's loop was never unblocked")
	}

	told := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(told) < 2 {
		select {
		case ev := <-events:
			if ev.Type != "permission.replied" {
				continue
			}
			var p map[string]string
			if err := json.Unmarshal(ev.Properties, &p); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			if p["permissionId"] != "child-perm" {
				t.Fatalf("event names %q, want child-perm", p["permissionId"])
			}
			told[p["sessionId"]] = true
		case <-deadline:
			t.Fatalf("permission.replied reached %v, want both sess-parent and sess-child", told)
		}
	}
	if !told["sess-parent"] || !told["sess-child"] {
		t.Fatalf("permission.replied reached %v, want both sess-parent and sess-child", told)
	}

	// And it is gone: a second answer is a stale prompt.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/session/sess-parent/permission/child-perm", strings.NewReader(`{"response":"once"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second reply = %d, want 404", rec.Code)
	}
}

// parentSessionOf is the LoopRunner's hook: a teammate resolves to the root
// session the user watches, carrying its own handle; a session is its own
// audience.
func TestParentSessionOfClimbsToTheRootSession(t *testing.T) {
	srv, _ := permissionTreeServer(t)

	parent, teammate := srv.parentSessionOf("sess-grand")
	if parent != "sess-parent" || teammate != "t2" {
		t.Fatalf("grandchild = (%q, %q), want (sess-parent, t2)", parent, teammate)
	}
	if parent, teammate := srv.parentSessionOf("sess-child"); parent != "sess-parent" || teammate != "t1" {
		t.Fatalf("child = (%q, %q), want (sess-parent, t1)", parent, teammate)
	}
	if parent, teammate := srv.parentSessionOf("sess-parent"); parent != "" || teammate != "" {
		t.Fatalf("session = (%q, %q), want none", parent, teammate)
	}
	if parent, teammate := srv.parentSessionOf("nobody"); parent != "" || teammate != "" {
		t.Fatalf("unknown = (%q, %q), want none", parent, teammate)
	}
}

// Answering from the teammate's own page must clear the parent's composer too:
// the parent holds a mirrored copy of the same prompt, so leaving it there shows
// a prompt that 404s when clicked.
func TestReplyingFromTheChildPageClearsTheParentQueue(t *testing.T) {
	srv, h := permissionTreeServer(t)
	events := srv.bus.SubscribeAll()
	pr := srv.permissions.Create(permission.Request{
		ID: "child-perm", SessionID: "sess-child", Tool: "bash", Patterns: []string{"make test"},
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/api/session/sess-child/permission/child-perm", strings.NewReader(`{"response":"always"}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST reply = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	select {
	case got := <-pr.ReplyCh:
		if got != "always" {
			t.Fatalf("teammate's loop got %q, want always", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("teammate's loop was never unblocked")
	}

	told := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for !told["sess-child"] || !told["sess-parent"] {
		select {
		case ev := <-events:
			if ev.Type != "permission.replied" {
				continue
			}
			var p map[string]string
			if err := json.Unmarshal(ev.Properties, &p); err != nil {
				t.Fatalf("decode event: %v", err)
			}
			if p["permissionId"] != "child-perm" {
				t.Fatalf("event names %q, want child-perm", p["permissionId"])
			}
			told[p["sessionId"]] = true
		case <-deadline:
			t.Fatalf("permission.replied reached %v, want sess-child and sess-parent", told)
		}
	}
}
