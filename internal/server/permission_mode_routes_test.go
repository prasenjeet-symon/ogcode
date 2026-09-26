package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/bus"
	"github.com/prasenjeet-symon/ogcode/internal/permission"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// newPermissionTestServer wires just enough of the server for the session
// routes: the stores the handlers touch plus a permission manager backed by the
// test's global DB. newTestServer leaves permissions nil, so it cannot exercise
// the mode-persistence path.
func newPermissionTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.bus = bus.New(64)
	srv.permissions = permission.NewManager(permission.NewStore(srv.globalDB))
	return srv, srv.routes()
}

func postSession(t *testing.T, h http.Handler, dir string) session.Session {
	t.Helper()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"directory":"` + dir + `"}`)
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/session", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/session = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var sess session.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil {
		t.Fatalf("decode session: %v (body: %s)", err, rec.Body.String())
	}
	return sess
}

// A new session must start in whatever mode the user last chose anywhere, so a
// user who prefers Auto does not reselect it for every session. Before this,
// every session started with an empty permission and read as Ask.
func TestNewSessionInheritsStoredDefaultMode(t *testing.T) {
	srv, h := newPermissionTestServer(t)

	if got := postSession(t, h, srv.dir).Permission; got != "ask" {
		t.Fatalf("default-mode session permission = %q, want ask", got)
	}

	// Flip the mode in one session — this is what the composer toggle PATCHes.
	sess := postSession(t, h, srv.dir)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/session/"+string(sess.ID),
		strings.NewReader(`{"permission":"auto"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/session = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// The next new session inherits Auto.
	if got := postSession(t, h, srv.dir).Permission; got != "auto" {
		t.Fatalf("session after mode flip = %q, want auto", got)
	}
}

// The stored default must survive a restart, which is a fresh server over the
// same global DB. This is the whole point of persisting it.
func TestStoredDefaultModeSurvivesRestart(t *testing.T) {
	srv, h := newPermissionTestServer(t)
	sess := postSession(t, h, srv.dir)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/session/"+string(sess.ID),
		strings.NewReader(`{"permission":"auto"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want 200", rec.Code)
	}

	// A second server sharing the same global DB — the restart path.
	restarted := &Server{
		db:          srv.db,
		globalDB:    srv.globalDB,
		store:       session.NewStore(srv.db),
		bus:         bus.New(64),
		dir:         srv.dir,
		permissions: permission.NewManager(permission.NewStore(srv.globalDB)),
	}
	if got := restarted.permissions.DefaultMode(); got != permission.ModeAuto {
		t.Fatalf("default mode after restart = %q, want auto", got)
	}
}
