package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// newGitTestServer creates a temp git repo with one committed file and returns
// a Server whose dir points at it.
func newGitTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitCmd(t, dir, "init", "--quiet")
	gitCmd(t, dir, "config", "user.name", "test")
	gitCmd(t, dir, "config", "user.email", "test@local")
	writeFile(t, dir, "README.md", "seed\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "seed")
	return &Server{dir: dir}, dir
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, string(out), err)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHandleGitStatus_Clean(t *testing.T) {
	srv, _ := newGitTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/git/status", nil)
	rec := httptest.NewRecorder()
	srv.handleGitStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %v, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		IsRepo bool  `json:"isRepo"`
		Files  []any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.IsRepo {
		t.Error("expected isRepo=true")
	}
	if len(resp.Files) != 0 {
		t.Errorf("expected 0 files, got %d", len(resp.Files))
	}
}

func TestHandleGitStatus_Modified(t *testing.T) {
	srv, dir := newGitTestServer(t)
	writeFile(t, dir, "README.md", "changed\n")

	req := httptest.NewRequest(http.MethodGet, "/api/git/status", nil)
	rec := httptest.NewRecorder()
	srv.handleGitStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %v: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		IsRepo bool             `json:"isRepo"`
		Files  []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(resp.Files))
	}
	if resp.Files[0]["path"] != "README.md" {
		t.Errorf("path = %v", resp.Files[0]["path"])
	}
}

func TestHandleGitStatus_NotARepo(t *testing.T) {
	srv := &Server{dir: t.TempDir()}
	req := httptest.NewRequest(http.MethodGet, "/api/git/status", nil)
	rec := httptest.NewRecorder()
	srv.handleGitStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %v", rec.Code)
	}
	var resp struct {
		IsRepo bool `json:"isRepo"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.IsRepo {
		t.Error("expected isRepo=false")
	}
}

func TestHandleGitDiff_Unstaged(t *testing.T) {
	srv, dir := newGitTestServer(t)
	writeFile(t, dir, "README.md", "changed\n")

	req := httptest.NewRequest(http.MethodGet, "/api/git/diff?path=README.md", nil)
	rec := httptest.NewRecorder()
	srv.handleGitDiff(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %v: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !contains(resp.Diff, "-seed") || !contains(resp.Diff, "+changed") {
		t.Fatalf("diff missing hunks:\n%s", resp.Diff)
	}
}

func TestHandleGitDiff_Staged(t *testing.T) {
	srv, dir := newGitTestServer(t)
	writeFile(t, dir, "README.md", "changed\n")
	gitCmd(t, dir, "add", "README.md")

	req := httptest.NewRequest(http.MethodGet, "/api/git/diff?path=README.md&staged=true", nil)
	rec := httptest.NewRecorder()
	srv.handleGitDiff(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %v: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !contains(resp.Diff, "+changed") {
		t.Fatalf("staged diff missing change:\n%s", resp.Diff)
	}
}

func TestHandleGitDiff_MissingPath(t *testing.T) {
	srv, _ := newGitTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/git/diff", nil)
	rec := httptest.NewRecorder()
	srv.handleGitDiff(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %v", rec.Code)
	}
}

func TestHandleGitCommits(t *testing.T) {
	srv, _ := newGitTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/git/commits?n=5", nil)
	rec := httptest.NewRecorder()
	srv.handleGitCommits(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %v: %s", rec.Code, rec.Body.String())
	}
	var commits []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &commits); err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(commits))
	}
	if commits[0]["message"] != "seed" {
		t.Errorf("message = %v", commits[0]["message"])
	}
}

func TestHandleGitCommit(t *testing.T) {
	srv, dir := newGitTestServer(t)

	// Get the commit sha via git log.
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := string(out[:len(out)-1])

	req := httptest.NewRequest(http.MethodGet, "/api/git/commit/"+sha, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sha", sha)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	srv.handleGitCommit(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %v: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Diff string `json:"diff"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !contains(resp.Diff, "seed") {
		t.Fatalf("commit diff missing content:\n%s", resp.Diff)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestHandleGitStageAndCommit(t *testing.T) {
	srv, dir := newGitTestServer(t)
	writeFile(t, dir, "new.txt", "hello\n")

	// Stage the new file.
	body := `{"paths":["new.txt"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/git/stage", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleGitStage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stage: status = %v: %s", rec.Code, rec.Body.String())
	}

	// The file should now appear staged in status.
	req = httptest.NewRequest(http.MethodGet, "/api/git/status", nil)
	rec = httptest.NewRecorder()
	srv.handleGitStatus(rec, req)
	var resp struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(resp.Files))
	}
	if resp.Files[0]["staged"] != true {
		t.Errorf("expected staged=true, got %v", resp.Files[0]["staged"])
	}

	// Commit with nothing staged via a fresh untracked-different file would
	// fail, but we have staged "new.txt". Commit it.
	commitBody := `{"message":"add new file"}`
	req = httptest.NewRequest(http.MethodPost, "/api/git/commit", strings.NewReader(commitBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.handleGitCommitCreate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit: status = %v: %s", rec.Code, rec.Body.String())
	}

	// The commit should appear in /commits.
	req = httptest.NewRequest(http.MethodGet, "/api/git/commits", nil)
	rec = httptest.NewRecorder()
	srv.handleGitCommits(rec, req)
	var commits []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &commits); err != nil {
		t.Fatal(err)
	}
	if len(commits) < 2 {
		t.Fatalf("expected >=2 commits, got %d", len(commits))
	}
	if commits[0]["message"] != "add new file" {
		t.Errorf("latest message = %v", commits[0]["message"])
	}
}

func TestHandleGitCommitNothingStaged(t *testing.T) {
	srv, _ := newGitTestServer(t)
	body := `{"message":"empty"}`
	req := httptest.NewRequest(http.MethodPost, "/api/git/commit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleGitCommitCreate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for nothing staged, got %v: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGitUnstage(t *testing.T) {
	srv, dir := newGitTestServer(t)
	writeFile(t, dir, "new.txt", "hello\n")

	// Stage then unstage.
	stageBody := `{"paths":["new.txt"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/git/stage", strings.NewReader(stageBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleGitStage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stage: %v: %s", rec.Code, rec.Body.String())
	}

	unstageBody := `{"paths":["new.txt"]}`
	req = httptest.NewRequest(http.MethodPost, "/api/git/unstage", strings.NewReader(unstageBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.handleGitUnstage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unstage: %v: %s", rec.Code, rec.Body.String())
	}

	// After unstaging, the file should be untracked (not staged).
	req = httptest.NewRequest(http.MethodGet, "/api/git/status", nil)
	rec = httptest.NewRecorder()
	srv.handleGitStatus(rec, req)
	var resp struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(resp.Files))
	}
	if resp.Files[0]["staged"] == true {
		t.Errorf("expected staged=false after unstage, got %v", resp.Files[0]["staged"])
	}
}
