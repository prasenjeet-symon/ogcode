package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/board"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// TestSessionTokensTotalsTheWholeTranscript pins the fix for a token pill that
// under-reported on a long session: the endpoint totals every assistant step in
// the session, so a session with more steps than fit in one transcript page
// still reports its whole spend, not just the newest page's.
func TestSessionTokensTotalsTheWholeTranscript(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)

	id := session.NewSessionID()
	if err := srv.store.Create(&session.Session{
		ID: id, ProjectID: srv.dir, Directory: srv.dir, Title: "t",
		SessionType: "build", CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Deliberately more steps than a transcript page holds, so a figure summed
	// from the client's newest page would fall short of the true total.
	const steps = transcriptPageSize + 100
	const perStepIn, perStepOut, perStepReason, perStepRead, perStepWrite = 10, 5, 3, 100, 2
	for i := 0; i < steps; i++ {
		if err := srv.store.CreateMessage(&session.MessageInfo{
			ID: session.NewMessageID(), SessionID: id, Role: session.RoleAssistant, CreatedAt: session.Now(),
			Tokens: &session.TokenCounts{
				Input: perStepIn, Output: perStepOut, Reasoning: perStepReason,
				CacheRead: perStepRead, CacheWrite: perStepWrite,
			},
		}); err != nil {
			t.Fatalf("create message %d: %v", i, err)
		}
	}
	// A user message carrying no tokens must not be counted.
	if err := srv.store.CreateMessage(&session.MessageInfo{
		ID: session.NewMessageID(), SessionID: id, Role: session.RoleUser, CreatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create user message: %v", err)
	}

	const uIn, uOut, uReason, uRead, uWrite = 7, 4, 1, 50, 3
	if err := srv.store.AddUtilityUsage(id, session.TokenCounts{
		Input: uIn, Output: uOut, Reasoning: uReason, CacheRead: uRead, CacheWrite: uWrite,
	}); err != nil {
		t.Fatalf("add utility usage: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/session/{sessionID}/token", srv.handleSessionTokens)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/session/"+string(id)+"/token", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET token: status %d: %s", rec.Code, rec.Body)
	}
	var got sessionTokens
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode tokens: %v", err)
	}

	wantIn := steps*perStepIn + uIn
	wantOut := steps*perStepOut + uOut
	wantReason := steps*perStepReason + uReason
	wantRead := steps*perStepRead + uRead
	wantWrite := steps*perStepWrite + uWrite
	if got.Input != wantIn || got.Output != wantOut || got.Reasoning != wantReason ||
		got.CacheRead != wantRead || got.CacheWrite != wantWrite {
		t.Errorf("component totals = %+v; want in=%d out=%d reasoning=%d cacheRead=%d cacheWrite=%d",
			got, wantIn, wantOut, wantReason, wantRead, wantWrite)
	}
	// Utility reports its own Effective; the headline totals fold the utility
	// components in, matching TokenCounts.Consumed/Effective.
	if want := uIn + uWrite + uOut; got.Utility != want {
		t.Errorf("utility = %d, want %d", got.Utility, want)
	}
	if want := wantIn + wantRead + wantWrite + wantOut; got.Total != want {
		t.Errorf("total = %d, want %d", got.Total, want)
	}
	if want := wantIn + wantWrite + wantOut; got.Effective != want {
		t.Errorf("effective = %d, want %d", got.Effective, want)
	}
}

// TestSessionTokensUnknownSessionIs404 pins that the endpoint distinguishes a
// missing session from a store failure.
func TestSessionTokensUnknownSessionIs404(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)

	r := chi.NewRouter()
	r.Get("/session/{sessionID}/token", srv.handleSessionTokens)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/session/ses_missing/token", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body)
	}
}

// tokenTestSession creates a build session row in the test server's store.
func tokenTestSession(t *testing.T, srv *Server, title string) session.SessionID {
	t.Helper()
	id := session.NewSessionID()
	if err := srv.store.Create(&session.Session{
		ID: id, ProjectID: srv.dir, Directory: srv.dir, Title: title,
		SessionType: "build", CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create session %q: %v", title, err)
	}
	return id
}

// addTokenSteps records n assistant messages carrying the same counts.
func addTokenSteps(t *testing.T, srv *Server, id session.SessionID, n int, counts session.TokenCounts) {
	t.Helper()
	for i := 0; i < n; i++ {
		c := counts
		if err := srv.store.CreateMessage(&session.MessageInfo{
			ID: session.NewMessageID(), SessionID: id, Role: session.RoleAssistant, CreatedAt: session.Now(),
			Tokens: &c,
		}); err != nil {
			t.Fatalf("create step %d for %s: %v", i, id, err)
		}
	}
}

// registerTokenHandle joins one handle to the test board.
func registerTokenHandle(t *testing.T, srv *Server, h board.Handle) {
	t.Helper()
	if _, err := srv.board.Register(context.Background(), h); err != nil {
		t.Fatalf("register handle %q: %v", h.Handle, err)
	}
}

// TestSessionTokensRollsUpSpawnedTeammates pins the teammate half of the token
// endpoint: the teammate handles this session spawned are listed with their own
// tokens, while a teammate of another parent, a plain session handle and a
// teammate whose session row was deleted are all left out. It also pins the
// invariant the pill relies on — the parent's own totals never contain a
// teammate's tokens, so a client that wants the subtree folds them in itself
// and one that wants only this session's spend reads the headline fields.
func TestSessionTokensRollsUpSpawnedTeammates(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.board = board.Open(srv.db, srv.dir)

	parent := tokenTestSession(t, srv, "parent")
	child := tokenTestSession(t, srv, "child")
	sibling := tokenTestSession(t, srv, "sibling")
	plain := tokenTestSession(t, srv, "plain")
	deleted := tokenTestSession(t, srv, "deleted")

	const parentIn, parentOut = 10, 5
	addTokenSteps(t, srv, parent, 2, session.TokenCounts{Input: parentIn, Output: parentOut})
	const childIn, childOut, childReason, childRead, childWrite = 100, 40, 7, 900, 11
	addTokenSteps(t, srv, child, 3, session.TokenCounts{
		Input: childIn, Output: childOut, Reasoning: childReason, CacheRead: childRead, CacheWrite: childWrite,
	})
	// Every excluded session gets one step; none of them may move the reply.
	for _, id := range []session.SessionID{sibling, plain, deleted} {
		addTokenSteps(t, srv, id, 1, session.TokenCounts{Input: 1, Output: 1})
	}

	registerTokenHandle(t, srv, board.Handle{
		Handle: "build-test", SessionID: string(parent), Role: "build", Kind: board.KindSession, Status: board.StatusIdle,
	})
	registerTokenHandle(t, srv, board.Handle{
		Handle: "build-other", Role: "build", Kind: board.KindSession,
	})
	registerTokenHandle(t, srv, board.Handle{
		Handle: "child-a", SessionID: string(child), Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-test", Status: board.StatusRunning,
	})
	registerTokenHandle(t, srv, board.Handle{
		Handle: "child-b", SessionID: string(sibling), Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-other", Status: board.StatusRunning,
	})
	// A session handle that names the parent as its spawner is still not a
	// teammate.
	registerTokenHandle(t, srv, board.Handle{
		Handle: "plain", SessionID: string(plain), Role: "build",
		Kind: board.KindSession, SpawnedBy: "build-test", Status: board.StatusIdle,
	})
	registerTokenHandle(t, srv, board.Handle{
		Handle: "gone", SessionID: string(deleted), Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-test", Status: board.StatusDone,
	})
	// A deleted teammate keeps its roster row but loses its transcript.
	if err := srv.store.Delete(deleted); err != nil {
		t.Fatalf("delete teammate session: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/session/{sessionID}/token", srv.handleSessionTokens)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/session/"+string(parent)+"/token", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET token: status %d: %s", rec.Code, rec.Body)
	}
	var got sessionTokens
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode tokens: %v", err)
	}

	if len(got.Teammates) != 1 {
		t.Fatalf("teammates = %+v, want only the one spawned by build-test", got.Teammates)
	}
	tm := got.Teammates[0]
	if tm.Handle != "child-a" || tm.SessionID != string(child) || tm.Status != board.StatusRunning {
		t.Errorf("teammate identity = %+v; want child-a / %s / %s", tm, child, board.StatusRunning)
	}
	if want := 3 * childIn; tm.Input != want {
		t.Errorf("teammate input = %d, want %d", tm.Input, want)
	}
	if want := 3 * childOut; tm.Output != want {
		t.Errorf("teammate output = %d, want %d", tm.Output, want)
	}
	if want := 3 * childReason; tm.Reasoning != want {
		t.Errorf("teammate reasoning = %d, want %d", tm.Reasoning, want)
	}
	if want := 3 * childRead; tm.CacheRead != want {
		t.Errorf("teammate cacheRead = %d, want %d", tm.CacheRead, want)
	}
	if want := 3 * childWrite; tm.CacheWrite != want {
		t.Errorf("teammate cacheWrite = %d, want %d", tm.CacheWrite, want)
	}
	if want := 3 * (childIn + childWrite + childOut); tm.Effective != want {
		t.Errorf("teammate effective = %d, want %d", tm.Effective, want)
	}
	if want := 3 * (childIn + childRead + childWrite + childOut); tm.Total != want {
		t.Errorf("teammate total = %d, want %d", tm.Total, want)
	}

	// The parent's own figures are its two steps and nothing else.
	if want := 2 * parentIn; got.Input != want {
		t.Errorf("parent input = %d, want %d (teammate tokens must not be folded in)", got.Input, want)
	}
	if want := 2 * parentOut; got.Output != want {
		t.Errorf("parent output = %d, want %d", got.Output, want)
	}
	if want := 2 * (parentIn + parentOut); got.Effective != want {
		t.Errorf("parent effective = %d, want %d", got.Effective, want)
	}
	if want := 2 * (parentIn + parentOut); got.Total != want {
		t.Errorf("parent total = %d, want %d", got.Total, want)
	}
}

// TestSessionTokensNoTeammatesMarshalsEmptyList pins the shape a client always
// reads: a session that spawned nobody serializes "teammates" as [], never
// null, so the pill can iterate it without a nil check.
func TestSessionTokensNoTeammatesMarshalsEmptyList(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.board = board.Open(srv.db, srv.dir)

	id := tokenTestSession(t, srv, "alone")
	addTokenSteps(t, srv, id, 1, session.TokenCounts{Input: 3, Output: 1})

	r := chi.NewRouter()
	r.Get("/session/{sessionID}/token", srv.handleSessionTokens)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/session/"+string(id)+"/token", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET token: status %d: %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"teammates":[]`) {
		t.Fatalf("body %s does not carry an empty teammates list", body)
	}
}

// TestSessionTokensSkipsATeammateWhoseReadErrors pins the per-child
// degradation: a teammate whose session row cannot be READ — a transient store
// error, not the nil,nil a deleted row returns — is skipped exactly like a
// deleted one, so one bad child does not 500 the whole token rollup and the
// parent's totals still answer.
func TestSessionTokensSkipsATeammateWhoseReadErrors(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.board = board.Open(srv.db, srv.dir)

	parent := tokenTestSession(t, srv, "parent")
	child := tokenTestSession(t, srv, "child")
	addTokenSteps(t, srv, parent, 1, session.TokenCounts{Input: 10, Output: 5})

	registerTokenHandle(t, srv, board.Handle{
		Handle: "build-test", SessionID: string(parent), Role: "build", Kind: board.KindSession, Status: board.StatusIdle,
	})
	registerTokenHandle(t, srv, board.Handle{
		Handle: "child-a", SessionID: string(child), Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build-test", Status: board.StatusRunning,
	})
	// Poison the child's row as a hand-seeded row could: Get scans permission
	// into a string, so this NULL makes store.Get return an error for the
	// child alone while the parent's row — created through store.Create —
	// still reads fine.
	if _, err := srv.db.Exec(`UPDATE session SET permission = NULL WHERE id = ?`, child); err != nil {
		t.Fatalf("poison teammate session row: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/session/{sessionID}/token", srv.handleSessionTokens)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/session/"+string(parent)+"/token", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET token: status %d: %s", rec.Code, rec.Body)
	}
	var got sessionTokens
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode tokens: %v", err)
	}
	if len(got.Teammates) != 0 {
		t.Fatalf("teammates = %+v, want the unreadable child skipped", got.Teammates)
	}
	// The parent's own figure is its one step, untouched by the child.
	if want := 10; got.Input != want {
		t.Errorf("parent input = %d, want %d", got.Input, want)
	}
	if want := 15; got.Effective != want {
		t.Errorf("parent effective = %d, want %d", got.Effective, want)
	}
}
