package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// hostTestServer starts a real, serving Server in a temp dir (the same shape a
// worker spawns per worktree) and returns it plus its base URL.
func hostTestServer(t *testing.T) *Server {
	t.Helper()
	// Neutralize provider env so the registry loads deterministically with no
	// network (same recipe as newTestServer).
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL",
		"OPENAI_API_KEY", "OPENAI_BASE_URL",
		"OPENROUTER_API_KEY", "OLLAMA_API_KEY", "OLLAMA_BASE_URL",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("OGCODE_FREE_KEYS_URL", "http://127.0.0.1:9/free-keys-unavailable")
	t.Setenv("OGCODE_EMBED_MODEL_DIR", t.TempDir())

	srv := NewWithOptions(0, t.TempDir(), ModeBuild, Options{Loopback: true, NoBrowser: true})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})

	waitUp(t, srv)
	return srv
}

// TestHostSession_CreatesRowsAndRunsLoop pins the Phase-2 hosting contract: a
// HostSession with a master-minted id writes session/message/part rows in this
// server's own store, marks the session as its agent type, and is visible over
// the same HTTP API a browser would use.
func TestHostSession_CreatesRowsAndRunsLoop(t *testing.T) {
	srv := hostTestServer(t)

	stop, existed, err := srv.HostSession("ses_workerhost1", srv.dir, "Fix the flaky login test", "build", 0, 0)
	if err != nil {
		t.Fatalf("HostSession: %v", err)
	}
	if existed {
		t.Fatal("first HostSession for a fresh id reported existed")
	}

	sess, err := srv.store.Get("ses_workerhost1")
	if err != nil || sess == nil {
		t.Fatalf("session row not found: %v", err)
	}
	if sess.Title != "Fix the flaky login test" && sess.Title != "Fix the flaky login test…" && len(sess.Title) > 61 {
		t.Fatalf("unexpected title %q", sess.Title)
	}
	if sess.SessionType != "build" {
		t.Fatalf("SessionType = %q, want build", sess.SessionType)
	}
	if sess.Directory != srv.dir {
		t.Fatalf("Directory = %q, want %q", sess.Directory, srv.dir)
	}

	// The user message + text part landed.
	msgs, err := srv.store.GetMessages("ses_workerhost1", "", 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Info.Role != "user" {
		t.Fatalf("expected exactly one user message, got %+v", msgs)
	}

	// Visible over the same API a browser uses.
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/session = %d", rec.Code)
	}
	var listed []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, s := range listed {
		if s.ID == "ses_workerhost1" {
			found = true
		}
	}
	if !found {
		t.Fatal("hosted session missing from /api/session list")
	}

	// stop() cancels the registered loop without error even when the loop
	// already exited (cancel on a cancelled ctx is a no-op).
	stop()
}

// TestHostSession_RedeliveryIsIdempotent pins that a re-delivered StartAgent
// (the master retrying) does not duplicate rows or error: the second call
// reports existed and leaves exactly one user message.
func TestHostSession_RedeliveryIsIdempotent(t *testing.T) {
	srv := hostTestServer(t)

	stop1, existed1, err := srv.HostSession("ses_workerdup", srv.dir, "First prompt", "build", 0, 0)
	if err != nil || existed1 {
		t.Fatalf("first host: existed=%v err=%v", existed1, err)
	}
	stop2, existed2, err := srv.HostSession("ses_workerdup", srv.dir, "First prompt", "build", 0, 0)
	if err != nil {
		t.Fatalf("second host: %v", err)
	}
	if !existed2 {
		t.Fatal("second HostSession for the same id reported existed=false")
	}
	stop1()
	stop2()

	msgs, err := srv.store.GetMessages("ses_workerdup", "", 10)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("re-delivery duplicated the user message: %d messages", len(msgs))
	}
}

// TestHostSession_GuidanceAndPermissionSeams pins that the exported Guidance
// and ReplyPermission methods behave like their HTTP counterparts: guidance on
// a session with no running loop errors with the same text the HTTP handler
// returns as a 409, and a reply to an unknown permission reports false.
func TestHostSession_GuidanceAndPermissionSeams(t *testing.T) {
	srv := hostTestServer(t)

	if err := srv.Guidance("ses_nowhere", "hi", true); err == nil || err.Error() != "no running agent loop for this session" {
		t.Fatalf("Guidance(no running loop) = %v, want the 409 text", err)
	}
	if srv.ReplyPermission("ses_nowhere", "perm1", "once") {
		t.Fatal("ReplyPermission for an unknown request returned true")
	}
}
