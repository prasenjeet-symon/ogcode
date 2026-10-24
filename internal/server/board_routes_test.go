package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/board"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// boardRequest sends one request through the real router, so a test covers the
// route, the handler and the JSON the drawer parses rather than a handler the
// client never reaches that way.
func boardRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestBoardRosterRouteDrawsTheTree walks the read the drawer uses: an empty
// board answers with an empty list (never null), and a spawned teammate comes
// back with the spawnedBy edge that nests it under its parent.
func TestBoardRosterRouteDrawsTheTree(t *testing.T) {
	srv := boardTestServer(t)
	h := srv.routes()
	ctx := context.Background()

	if _, err := srv.board.Register(ctx, board.Handle{
		Handle: "build", SessionID: "ses_build", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	}); err != nil {
		t.Fatalf("register parent: %v", err)
	}

	rec := boardRequest(t, h, http.MethodGet, "/api/board/roster", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/board/roster = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var root struct {
		Agents []board.Handle `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatalf("decode roster: %v (body: %s)", err, rec.Body.String())
	}
	if len(root.Agents) != 1 || root.Agents[0].Handle != "build" {
		t.Fatalf("roster = %+v, want the one session", root.Agents)
	}
	if root.Agents[0].SpawnedBy != "" {
		t.Errorf("session spawnedBy = %q, want empty (it is the root of its tree)", root.Agents[0].SpawnedBy)
	}

	if _, err := srv.board.Register(ctx, board.Handle{
		Handle: "worker", SessionID: "ses_worker", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build", Status: board.StatusRunning,
	}); err != nil {
		t.Fatalf("register teammate: %v", err)
	}

	rec = boardRequest(t, h, http.MethodGet, "/api/board/roster", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/board/roster = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatalf("decode roster: %v (body: %s)", err, rec.Body.String())
	}
	if len(root.Agents) != 2 {
		t.Fatalf("roster = %+v, want the session and its teammate", root.Agents)
	}
	worker := root.Agents[1]
	if worker.Handle != "worker" || worker.SpawnedBy != "build" {
		t.Fatalf("teammate row = %+v, want worker spawned by build", worker)
	}
	if worker.SessionID != "ses_worker" || worker.Status != board.StatusRunning || worker.Kind != board.KindTeammate {
		t.Fatalf("teammate row lost a field the drawer needs: %+v", worker)
	}
}

// TestBoardRosterRouteEmptyIsAnArray pins the shape the drawer parses: an empty
// board is [], not null.
func TestBoardRosterRouteEmptyIsAnArray(t *testing.T) {
	rec := boardRequest(t, boardTestServer(t).routes(), http.MethodGet, "/api/board/roster", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/board/roster = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"agents":[]`) {
		t.Fatalf("empty board = %s, want an empty agents array", rec.Body.String())
	}
}

// TestBoardStopTeammateRoute pins the three answers the Stop button can get: a
// stopped teammate, a handle nobody holds, and a session (which is stopped from
// its own page instead).
func TestBoardStopTeammateRoute(t *testing.T) {
	srv := boardTestServer(t)
	ctx := context.Background()
	h := srv.routes()
	for _, in := range []struct{ handle, kind string }{
		{"tm-one", board.KindTeammate},
		{"ses_live", board.KindSession},
	} {
		if _, err := srv.board.Register(ctx, board.Handle{
			Handle: in.handle, SessionID: "ses_" + in.handle, Role: "build",
			Kind: in.kind, Status: board.StatusRunning,
		}); err != nil {
			t.Fatalf("register %s: %v", in.handle, err)
		}
	}

	rec := boardRequest(t, h, http.MethodPost, "/api/board/teammate/tm-one/stop", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stop tm-one = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var res struct {
		Handle string `json:"handle"`
		Notice string `json:"notice"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode stop: %v (body: %s)", err, rec.Body.String())
	}
	if res.Handle != "tm-one" || res.Notice != "Stopped tm-one." {
		t.Fatalf("stop = handle %q notice %q, want tm-one/Stopped tm-one.", res.Handle, res.Notice)
	}
	got, err := srv.board.Get(ctx, "tm-one")
	if err != nil {
		t.Fatalf("get tm-one: %v", err)
	}
	if got.Status != board.StatusStopped {
		t.Fatalf("tm-one status = %q, want stopped", got.Status)
	}

	rec = boardRequest(t, h, http.MethodPost, "/api/board/teammate/ghost/stop", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stop an unknown handle = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	rec = boardRequest(t, h, http.MethodPost, "/api/board/teammate/ses_live/stop", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("stop a session handle = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	got, err = srv.board.Get(ctx, "ses_live")
	if err != nil {
		t.Fatalf("get ses_live: %v", err)
	}
	if got.Status != board.StatusRunning {
		t.Fatalf("ses_live status = %q, want running: a session is not stopped from the board", got.Status)
	}
}

// TestBoardRoutesUnavailableWithoutABoard pins the guard every handler shares:
// a server without a board answers 503 rather than panicking on a nil store.
func TestBoardRoutesUnavailableWithoutABoard(t *testing.T) {
	h := newTestServer(t).routes()
	requests := []struct{ method, path, body string }{
		{http.MethodGet, "/api/board/roster", ""},
		{http.MethodPost, "/api/board/teammate/tm-one/stop", ""},
	}
	for _, in := range requests {
		rec := boardRequest(t, h, in.method, in.path, in.body)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", in.method, in.path, rec.Code)
		}
	}
}

// rosterRowJSON is the roster wire row as the drawer's client decodes it: the
// board handle's fields and the joined title/model all flat at the top level.
type rosterRowJSON struct {
	Handle    string `json:"handle"`
	SessionID string `json:"sessionId"`
	Role      string `json:"role"`
	Kind      string `json:"kind"`
	SpawnedBy string `json:"spawnedBy"`
	Status    string `json:"status"`
	Title     string `json:"title"`
	Model     string `json:"model"`
}

// TestBoardRosterCarriesTheSessionTitleAndModel pins the join the drawer labels
// its rows with: a handle whose session exists shows the session's title and
// model beside the board's own wire fields, all at the top level.
func TestBoardRosterCarriesTheSessionTitleAndModel(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	ctx := context.Background()
	h := srv.routes()

	if _, err := srv.board.Register(ctx, board.Handle{
		Handle: "build", SessionID: "ses_build", Role: "build",
		Kind: board.KindSession, Status: board.StatusRunning,
	}); err != nil {
		t.Fatalf("register parent: %v", err)
	}
	if _, err := srv.board.Register(ctx, board.Handle{
		Handle: "worker", SessionID: "ses_worker", Role: "build",
		Kind: board.KindTeammate, SpawnedBy: "build", Status: board.StatusRunning,
	}); err != nil {
		t.Fatalf("register teammate: %v", err)
	}
	if err := srv.store.Create(&session.Session{
		ID: "ses_worker", Title: "Fix auth flow", Model: "claude-opus-4.7",
		CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	rec := boardRequest(t, h, http.MethodGet, "/api/board/roster", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/board/roster = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var root struct {
		Agents []rosterRowJSON `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatalf("decode roster: %v (body: %s)", err, rec.Body.String())
	}
	if len(root.Agents) != 2 {
		t.Fatalf("roster = %+v, want the parent and its teammate", root.Agents)
	}
	var worker rosterRowJSON
	for _, row := range root.Agents {
		if row.Handle == "worker" {
			worker = row
		}
	}
	if worker.Handle != "worker" {
		t.Fatalf("roster has no worker row: %+v", root.Agents)
	}
	if worker.Title != "Fix auth flow" || worker.Model != "claude-opus-4.7" {
		t.Fatalf("worker row = title %q model %q, want the session's own", worker.Title, worker.Model)
	}
	if worker.SessionID != "ses_worker" || worker.Status != board.StatusRunning || worker.Kind != board.KindTeammate {
		t.Fatalf("worker row lost a wire field: %+v", worker)
	}
}

// TestBoardRosterSurvivesAMissingSession pins the join's failure mode: a row
// whose session row is deleted under it comes back as itself, with empty title
// and model, and the roster as a whole still answers 200 — one missing session
// never costs the drawer its other agents.
func TestBoardRosterSurvivesAMissingSession(t *testing.T) {
	srv := boardTestServer(t)
	srv.store = session.NewStore(srv.db)
	ctx := context.Background()
	h := srv.routes()

	if _, err := srv.board.Register(ctx, board.Handle{
		Handle: "worker", SessionID: "ses_gone", Role: "build",
		Kind: board.KindTeammate, Status: board.StatusRunning,
	}); err != nil {
		t.Fatalf("register teammate: %v", err)
	}
	if err := srv.store.Create(&session.Session{
		ID: "ses_gone", CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := srv.store.Delete("ses_gone"); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	rec := boardRequest(t, h, http.MethodGet, "/api/board/roster", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/board/roster = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var root struct {
		Agents []rosterRowJSON `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &root); err != nil {
		t.Fatalf("decode roster: %v (body: %s)", err, rec.Body.String())
	}
	if len(root.Agents) != 1 || root.Agents[0].Handle != "worker" {
		t.Fatalf("roster = %+v, want the one teammate row", root.Agents)
	}
	if root.Agents[0].Title != "" || root.Agents[0].Model != "" {
		t.Fatalf("row title/model = %q %q, want empty: its session is gone", root.Agents[0].Title, root.Agents[0].Model)
	}
}
