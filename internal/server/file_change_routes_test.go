package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/filechange"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// fileChangeTestServer is a server with the change log wired the way serve()
// wires it, so the test covers the route a UI panel will call.
func fileChangeTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newTestServer(t)
	srv.fileChanges = filechange.Open(srv.db, srv.dir)
	return srv
}

// TestFileChangeRoutePagesNewestFirst walks the read a panel does: newest
// first, one project-relative path, a cursor that pages into the past, and no
// row from another file leaking into a filtered read.
func TestFileChangeRoutePagesNewestFirst(t *testing.T) {
	srv := fileChangeTestServer(t)
	h := srv.routes()
	ctx := context.Background()
	// Rows are keyed the way the file tools key them — against a file that was
	// there when the change was recorded — so make the file real before keying.
	if err := os.MkdirAll(filepath.Join(srv.dir, "internal", "tool"), 0o755); err != nil {
		t.Fatalf("make the directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srv.dir, "internal", "tool", "edit.go"), []byte("package tool\n"), 0o644); err != nil {
		t.Fatalf("write the file: %v", err)
	}
	path := tool.FileKey(srv.dir, "internal/tool/edit.go")
	for i := 1; i <= 3; i++ {
		if err := srv.fileChanges.Append(ctx, filechange.Change{
			Path:    path,
			Rev:     int64(i),
			Session: "session-a",
			Agent:   "build",
			Tool:    "edit",
			Note:    fmt.Sprintf("change %d", i),
			At:      time.Now(),
		}); err != nil {
			t.Fatalf("append change %d: %v", i, err)
		}
	}
	if err := srv.fileChanges.Append(ctx, filechange.Change{
		Path:    tool.FileKey(srv.dir, "other.go"),
		Rev:     1,
		Session: "session-b",
		Tool:    "write",
		Note:    "other file",
		At:      time.Now(),
	}); err != nil {
		t.Fatalf("append other file: %v", err)
	}

	type page struct {
		Changes []filechange.Change `json:"changes"`
		Cursor  int64               `json:"cursor"`
	}
	decode := func(rec *httptest.ResponseRecorder) page {
		t.Helper()
		var p page
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("decode page: %v (body: %s)", err, rec.Body.String())
		}
		return p
	}

	rec := boardRequest(t, h, http.MethodGet, "/api/file-changes?path=internal/tool/edit.go&limit=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/file-changes = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	first := decode(rec)
	if len(first.Changes) != 2 || first.Changes[0].Note != "change 3" || first.Changes[1].Note != "change 2" {
		t.Fatalf("first page = %s, want change 3 then change 2", rec.Body.String())
	}
	if got := first.Changes[0].Path; got != "internal/tool/edit.go" {
		t.Fatalf("path = %q, want the project-relative %q", got, "internal/tool/edit.go")
	}
	if first.Cursor == 0 || first.Cursor != first.Changes[1].ID {
		t.Fatalf("cursor = %d, want the oldest row id %d on the page", first.Cursor, first.Changes[1].ID)
	}

	// The cursor pages further into the past.
	rec = boardRequest(t, h, http.MethodGet,
		fmt.Sprintf("/api/file-changes?path=internal/tool/edit.go&before=%d", first.Cursor), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET page 2 = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if p := decode(rec); len(p.Changes) != 1 || p.Changes[0].Note != "change 1" {
		t.Fatalf("second page = %s, want change 1 only", rec.Body.String())
	}

	// Unfiltered is the whole project's history.
	rec = boardRequest(t, h, http.MethodGet, "/api/file-changes", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET all = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if p := decode(rec); len(p.Changes) != 4 {
		t.Fatalf("unfiltered history has %d rows, want 4", len(p.Changes))
	}
}

// TestFileChangeRouteIs503WithoutTheStore is the board's own contract: a route
// whose store has not been built yet says so instead of panicking.
func TestFileChangeRouteIs503WithoutTheStore(t *testing.T) {
	srv := newTestServer(t)
	rec := boardRequest(t, srv.routes(), http.MethodGet, "/api/file-changes", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/file-changes without a store = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("503 body = %s, want an error object", rec.Body.String())
	}
}
