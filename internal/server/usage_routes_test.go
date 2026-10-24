package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/board"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// TestSessionUsagePricesSpawnedTeammates pins the teammate half of the usage
// endpoint: the teammate this session spawned is priced as its own row, from
// its own steps, and the parent's totals stay its own. The test registry is
// empty, so nothing is priced and the cost is 0 — the token totals are what
// this pins.
func TestSessionUsagePricesSpawnedTeammates(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.board = board.Open(srv.db, srv.dir)

	parent := tokenTestSession(t, srv, "parent")
	child := tokenTestSession(t, srv, "child")
	addTokenSteps(t, srv, parent, 2, session.TokenCounts{Input: 10, Output: 5})
	const childIn, childOut, childRead, childWrite = 100, 40, 900, 11
	addTokenSteps(t, srv, child, 3, session.TokenCounts{
		Input: childIn, Output: childOut, CacheRead: childRead, CacheWrite: childWrite,
	})

	registerTokenHandle(t, srv, board.Handle{
		Handle: "build-test", SessionID: string(parent), Role: "build", Kind: board.KindSession, Status: board.StatusIdle,
	})
	registerTokenHandle(t, srv, board.Handle{
		Handle: "child-a", SessionID: string(child), Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-test", Status: board.StatusRunning,
	})

	r := chi.NewRouter()
	r.Get("/session/{sessionID}/usage", srv.handleSessionUsage)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/session/"+string(parent)+"/usage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET usage: status %d: %s", rec.Code, rec.Body)
	}
	var got sessionUsage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode usage: %v", err)
	}

	if len(got.Teammates) != 1 {
		t.Fatalf("teammates = %+v, want the one spawned teammate", got.Teammates)
	}
	tm := got.Teammates[0]
	if tm.Handle != "child-a" || tm.SessionID != string(child) || tm.Status != board.StatusRunning {
		t.Errorf("teammate identity = %+v; want child-a / %s / %s", tm, child, board.StatusRunning)
	}
	if want := 3 * childIn; tm.Totals.Input != want {
		t.Errorf("teammate totals.input = %d, want %d", tm.Totals.Input, want)
	}
	if want := 3 * childOut; tm.Totals.Output != want {
		t.Errorf("teammate totals.output = %d, want %d", tm.Totals.Output, want)
	}
	if want := 3 * (childIn + childWrite + childOut); tm.Totals.Effective != want {
		t.Errorf("teammate totals.effective = %d, want %d", tm.Totals.Effective, want)
	}
	if want := 3 * (childIn + childRead + childWrite + childOut); tm.Totals.Total != want {
		t.Errorf("teammate totals.total = %d, want %d", tm.Totals.Total, want)
	}

	// The parent's totals price its two steps only.
	if want := 2 * 10; got.Totals.Input != want {
		t.Errorf("parent totals.input = %d, want %d (teammate tokens must not be folded in)", got.Totals.Input, want)
	}
	if want := 2 * (10 + 5); got.Totals.Effective != want {
		t.Errorf("parent totals.effective = %d, want %d", got.Totals.Effective, want)
	}
}

// TestSessionUsageNoTeammatesMarshalsEmptyList pins that a session which spawned
// nobody answers "teammates":[], never null, so the pill can iterate it.
func TestSessionUsageNoTeammatesMarshalsEmptyList(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.board = board.Open(srv.db, srv.dir)

	id := tokenTestSession(t, srv, "alone")
	addTokenSteps(t, srv, id, 1, session.TokenCounts{Input: 3, Output: 1})

	r := chi.NewRouter()
	r.Get("/session/{sessionID}/usage", srv.handleSessionUsage)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/session/"+string(id)+"/usage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET usage: status %d: %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"teammates":[]`) {
		t.Fatalf("body %s does not carry an empty teammates list", body)
	}
}
