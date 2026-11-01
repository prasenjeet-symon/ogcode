package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/memfile"
	"github.com/prasenjeet-symon/ogcode/internal/project"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// TestSessionSearchRouteWinsOverSessionIDParam pins the one route ordering the
// search endpoint depends on: /api/session/search must reach the search
// handler, never GET /api/session/{sessionID} with id "search". A reshuffle of
// the /session block would silently turn every palette query into a lookup for
// a session called "search" — no compile error, no other failing test.
func TestSessionSearchRouteWinsOverSessionIDParam(t *testing.T) {
	t.Setenv("OGCODE_TURN_MEMORY", "") // enabled: the handler's gate, not the corpus
	srv := newTestServer(t)

	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session/search?q=auth", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/session/search = %d, want 200 — the request reached another handler (session id %q?): %s",
			rec.Code, "search", rec.Body.String())
	}

	var got struct {
		Query   string          `json:"query"`
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode search response: %v (body: %s)", err, rec.Body.String())
	}
	// The search payload carries the query it answers; a session body never does.
	if got.Query != "auth" {
		t.Fatalf("query echoed as %q, want %q — not the search handler (body: %s)", got.Query, "auth", rec.Body.String())
	}
	var hits []memfile.Hit
	if err := json.Unmarshal(got.Results, &hits); err != nil {
		t.Fatalf("results is not a list of hits: %v (body: %s)", err, rec.Body.String())
	}
	// No store on this server: an empty page, not null and not an error.
	if hits == nil {
		t.Fatalf("results serialized as null, want []: %s", rec.Body.String())
	}
}

// TestSessionSearchRejectsShortQueries pins the floor under the palette's
// keystrokes: a one-character query is refused with a 400 the client can show,
// so a stray keypress never scans the whole memory corpus.
func TestSessionSearchRejectsShortQueries(t *testing.T) {
	srv := newTestServer(t)
	r := chi.NewRouter()
	r.Get("/session/search", srv.handleSearchSessions)

	for _, q := range []string{"", "a", "  a  "} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/session/search?q="+url.QueryEscape(q), nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("q=%q = %d, want 400 (body: %s)", q, rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("q=%q: decode error body: %v (body: %s)", q, err, rec.Body.String())
		}
		if body["error"] != "query too short" {
			t.Fatalf("q=%q: error = %q, want %q", q, body["error"], "query too short")
		}
	}
}

// TestSessionSearchIgnoresACanceledClient pins the silence the palette needs:
// it aborts every superseded keystroke's request (session-palette.tsx), which
// cancels the search's context mid-query. That is the client walking away, not
// a server failure — answering 500 and logging it at ERROR turned ordinary
// typing into a wall of "context canceled" lines.
func TestSessionSearchIgnoresACanceledClient(t *testing.T) {
	t.Setenv("OGCODE_TURN_MEMORY", "")
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.search = memfile.NewSearchStore(srv.db)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/session/search?q=auth", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("canceled search = %d, want the handler to answer nothing (200 with an empty body): %s",
			rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("canceled search wrote a body, want silence: %s", rec.Body.String())
	}
}

// TestSessionSearchRanksTitleAboveBody seeds two conversations whose memory
// says the same thing in different places — one in its title, one only in a
// turn's body — and pins both halves of the answer the palette renders: the
// title match ranks first, and each row carries the fields the palette reads.
func TestSessionSearchRanksTitleAboveBody(t *testing.T) {
	t.Setenv("OGCODE_TURN_MEMORY", "")
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.search = memfile.NewSearchStore(srv.db)
	projectID := project.Resolve(srv.dir)

	// The store drops hits whose session row is gone, so both conversations
	// exist as real sessions as well as memory.
	newSeededSession := func(title string) session.SessionID {
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

	const term = "reconnect"
	titleID := newSeededSession("WebSocket reconnect storm")
	bodyID := newSeededSession("Release checklist")

	seed := func(id session.SessionID, title, body string) {
		t.Helper()
		if _, err := memfile.Write(srv.dir, memfile.Meta{
			SessionID: string(id), ProjectID: projectID, SessionType: "build",
			Title: title, CreatedAt: time.Now(),
		}, body); err != nil {
			t.Fatalf("seed memory for %s: %v", id, err)
		}
	}
	// The title matches; the body deliberately does not.
	seed(titleID, "WebSocket reconnect storm", "## Turn\nChecked the retry budget; nothing needed changing.")
	// The body matches; the title deliberately does not.
	seed(bodyID, "Release checklist", "## Turn\nThe reconnect path held; only the socket layer needed work.")

	search := func(query string) (hits []memfile.Hit, raw string) {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session/search?q="+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("search %q = %d: %s", query, rec.Code, rec.Body.String())
		}
		var body struct {
			Query   string        `json:"query"`
			Results []memfile.Hit `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode search %q: %v (body: %s)", query, err, rec.Body.String())
		}
		if body.Query != query {
			t.Fatalf("query echoed as %q, want %q", body.Query, query)
		}
		return body.Results, rec.Body.String()
	}

	hits, raw := search(term)
	if len(hits) < 2 {
		t.Fatalf("search %q returned %d hits, want both seeded sessions: %s", term, len(hits), raw)
	}
	if hits[0].SessionID != string(titleID) {
		t.Fatalf("rank 1 = %s (%q), want the title match %s — a title hit must outrank a body-only hit: %s",
			hits[0].SessionID, hits[0].Title, titleID, raw)
	}

	// The palette reads exactly these fields off each row.
	first := hits[0]
	if first.Title != "WebSocket reconnect storm" {
		t.Fatalf("title = %q, want the session's title", first.Title)
	}
	if first.Source != "title" {
		t.Fatalf("source = %q, want %q for a title match", first.Source, "title")
	}
	if first.UpdatedAt <= 0 {
		t.Fatalf("updatedAt = %d, want the session's stamp", first.UpdatedAt)
	}
	if first.MatchedTurns < 1 {
		t.Fatalf("matchedTurns = %d, want at least 1", first.MatchedTurns)
	}

	var bodyHit *memfile.Hit
	for i := range hits {
		if hits[i].SessionID == string(bodyID) {
			bodyHit = &hits[i]
			break
		}
	}
	if bodyHit == nil {
		t.Fatalf("the body-only match %s is missing from the results: %s", bodyID, raw)
	}
	if bodyHit.Source != "turn" {
		t.Fatalf("source = %q, want %q for a turn-body match", bodyHit.Source, "turn")
	}
	if !strings.Contains(strings.ToLower(bodyHit.Snippet), term) {
		t.Fatalf("snippet = %q, want it to carry the matched term %q", bodyHit.Snippet, term)
	}

	// A query nothing matches answers an empty list, never null — the client
	// renders "no results" off the array, not off a missing key.
	if empty, emptyRaw := search("zzzzunmatched"); len(empty) != 0 {
		t.Fatalf("unmatched query returned %d hits, want 0: %s", len(empty), emptyRaw)
	} else if !strings.Contains(emptyRaw, `"results":[]`) {
		t.Fatalf(`results not serialized as []: %s`, emptyRaw)
	}
}
