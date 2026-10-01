package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// TestGetMessagesPagesWithHasOlder pins the transcript pager's contract over
// HTTP: each page is at most transcriptPageSize messages, oldest first; the
// X-Has-Older header says whether a further ?before= fetch would find anything;
// and a message without parts is encoded with "parts": [] so the client never
// meets a null.
func TestGetMessagesPagesWithHasOlder(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)

	id := session.NewSessionID()
	if err := srv.store.Create(&session.Session{
		ID: id, ProjectID: srv.dir, Directory: srv.dir, Title: "t",
		SessionType: "build", CreatedAt: session.Now(), UpdatedAt: session.Now(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	for i := 0; i < transcriptPageSize+1; i++ {
		if err := srv.store.CreateMessage(&session.MessageInfo{
			ID: session.NewMessageID(), SessionID: id, Role: session.RoleAssistant, CreatedAt: session.Now(),
		}); err != nil {
			t.Fatalf("create message %d: %v", i, err)
		}
	}

	r := chi.NewRouter()
	r.Get("/session/{sessionID}/message", srv.handleGetMessages)
	get := func(query string) ([]map[string]any, string) {
		t.Helper()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/session/"+string(id)+"/message"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d: %s", query, rec.Code, rec.Body)
		}
		var page []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode page: %v", err)
		}
		return page, rec.Header().Get(hasOlderHeader)
	}

	first, more := get("")
	if len(first) != transcriptPageSize || more != "true" {
		t.Fatalf("first page = %d messages, %s=%q; want %d and true", len(first), hasOlderHeader, more, transcriptPageSize)
	}
	if parts, ok := first[0]["parts"].([]any); !ok || len(parts) != 0 {
		t.Errorf("a message without parts encoded parts as %#v, want []", first[0]["parts"])
	}

	oldest := first[0]["info"].(map[string]any)["id"].(string)
	second, more := get("?before=" + oldest)
	if len(second) != 1 || more != "false" {
		t.Errorf("second page = %d messages, %s=%q; want 1 and false", len(second), hasOlderHeader, more)
	}

	// A refresh asks for the newest few: the tail of the transcript, not its
	// head, with the header still saying more lies above.
	msgID := func(m map[string]any) string { return m["info"].(map[string]any)["id"].(string) }
	recent, more := get("?limit=50")
	if len(recent) != 50 || more != "true" {
		t.Fatalf("?limit=50 = %d messages, %s=%q; want 50 and true", len(recent), hasOlderHeader, more)
	}
	if msgID(recent[0]) != msgID(first[len(first)-50]) || msgID(recent[49]) != msgID(first[len(first)-1]) {
		t.Errorf("?limit=50 returned %s..%s, want the newest 50 (%s..%s)",
			msgID(recent[0]), msgID(recent[49]), msgID(first[len(first)-50]), msgID(first[len(first)-1]))
	}

	// Anything that is not a usable size falls back to the full page.
	for _, q := range []string{"?limit=0", "?limit=-5", "?limit=abc", "?limit=100000"} {
		if page, _ := get(q); len(page) != transcriptPageSize {
			t.Errorf("%s returned %d messages, want the full page of %d", q, len(page), transcriptPageSize)
		}
	}
}
