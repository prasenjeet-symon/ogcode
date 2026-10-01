package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
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
