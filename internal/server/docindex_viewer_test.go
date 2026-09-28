package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/docindex"
)

// pngBytes is enough of a PNG for a response body to be compared byte for byte.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func assetRequest(t *testing.T, srv *Server, workspace, path string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"path": {path}, "directory": {workspace}}
	req := httptest.NewRequest(http.MethodGet, "/api/docindex/docs/raw?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	srv.handleReadDocAsset(rec, req)
	return rec
}

func writeAsset(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A README's relative image is the case the endpoint exists for: the bytes come
// back as the image type, sandboxed, so an SVG opened directly cannot script
// the ogcode origin.
func TestReadDocAsset_ServesAWorkspaceImage(t *testing.T) {
	srv := autoIndexServer(t)
	ws := t.TempDir()
	logo := filepath.Join(ws, "assets", "logo.png")
	writeAsset(t, logo, pngBytes)

	rec := assetRequest(t, srv, ws, logo)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("Content-Security-Policy = %q, want a sandbox", csp)
	}
	if !bytes.Equal(rec.Body.Bytes(), pngBytes) {
		t.Errorf("body = %q, want the file's bytes", rec.Body.Bytes())
	}
}

// Only images are served. The text viewer already reads source files as JSON;
// a raw endpoint that served them too would be a second, looser way in.
func TestReadDocAsset_RefusesANonImage(t *testing.T) {
	srv := autoIndexServer(t)
	ws := t.TempDir()
	notes := filepath.Join(ws, "notes.txt")
	writeAsset(t, notes, []byte("plain text"))

	if rec := assetRequest(t, srv, ws, notes); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestReadDocAsset_RefusesAPathOutsideTheWorkspace(t *testing.T) {
	srv := autoIndexServer(t)
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.png")
	writeAsset(t, outside, pngBytes)

	if rec := assetRequest(t, srv, ws, outside); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	// Climbing out with .. is the same request by another spelling.
	climb := filepath.Join(ws, "..", filepath.Base(filepath.Dir(outside)), "elsewhere.png")
	if rec := assetRequest(t, srv, ws, climb); rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d for a .. path, want it refused", rec.Code)
	}
}

// The type is the target's, not the link's: an image-named symlink to a file
// outside the workspace is refused by where it points.
func TestReadDocAsset_RefusesASymlinkThatLeavesTheWorkspace(t *testing.T) {
	srv := autoIndexServer(t)
	ws := t.TempDir()
	target := filepath.Join(t.TempDir(), "secret.png")
	writeAsset(t, target, pngBytes)
	link := filepath.Join(ws, "logo.png")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if rec := assetRequest(t, srv, ws, link); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestGetIndexedDoc_ReturnsWhatTheIndexRecorded(t *testing.T) {
	srv := autoIndexServer(t)
	doc := filepath.Join(srv.dir, "guide.pdf")
	for page, labels := range map[int][]string{
		1: {"Install Guide", "System Requirements"},
		2: {"Troubleshooting"},
	} {
		if err := srv.docindexStore.Upsert(&docindex.PageEntry{
			DocPath: doc, PageNum: page, Labels: labels, Keywords: []string{"setup"},
		}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/docindex/docs/entry?path="+url.QueryEscape(doc), nil)
	rec := httptest.NewRecorder()
	srv.handleGetIndexedDoc(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got docindex.DocSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PageCount != 2 || len(got.Pages) != 2 {
		t.Fatalf("pageCount = %d, pages = %d, want 2 and 2", got.PageCount, len(got.Pages))
	}
	// Pages come back in page order, so the viewer can list them as the
	// document reads.
	if got.Pages[0].PageNum != 1 || got.Pages[0].Labels[0] != "Install Guide" {
		t.Errorf("first page = %+v, want page 1 labelled Install Guide", got.Pages[0])
	}
	if got.IndexedAt == 0 {
		t.Error("indexedAt is unset; the viewer shows it as when the file was indexed")
	}
}

func TestGetIndexedDoc_UnindexedFileIsNotFound(t *testing.T) {
	srv := autoIndexServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/docindex/docs/entry?path="+url.QueryEscape(filepath.Join(srv.dir, "new.md")), nil)
	rec := httptest.NewRecorder()
	srv.handleGetIndexedDoc(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
