package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/git"
)

// resolveDir returns the directory to operate on: the ?directory= query param if
// present, else the server's working directory. This matches the convention
// used by the docindex routes.
func (s *Server) resolveGitDir(r *http.Request) string {
	if d := r.URL.Query().Get("directory"); d != "" {
		return d
	}
	return s.dir
}

// handleGitStatus returns the working-tree status of the workspace as a list of
// FileStatus entries. When the directory is not a git repo, isRepo is false and
// files is empty.
func (s *Server) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	dir := s.resolveGitDir(r)
	repo := git.IsRepo(dir)
	if !repo {
		writeJSON(w, http.StatusOK, map[string]any{"isRepo": false, "files": []any{}})
		return
	}
	files, err := git.Status(dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"isRepo": true, "files": files})
}

// handleGitDiff returns the raw unified diff for a single path. ?path= is
// required; ?staged=true diffs the index instead of the working tree.
func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	dir := s.resolveGitDir(r)
	if !git.IsRepo(dir) {
		http.Error(w, "not a git repository", http.StatusNotFound)
		return
	}
	staged := r.URL.Query().Has("staged")
	out, err := git.DiffFile(dir, path, staged)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diff": out})
}

// handleGitCommits returns recent commits. ?n= defaults to 20.
func (s *Server) handleGitCommits(w http.ResponseWriter, r *http.Request) {
	dir := s.resolveGitDir(r)
	if !git.IsRepo(dir) {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	n := 20
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	commits, err := git.RecentCommits(dir, n)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, commits)
}

// handleGitCommit returns the full unified diff for a single commit.
func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	sha := strings.TrimSpace(chi.URLParam(r, "sha"))
	if sha == "" {
		http.Error(w, "missing sha", http.StatusBadRequest)
		return
	}
	dir := s.resolveGitDir(r)
	if !git.IsRepo(dir) {
		http.Error(w, "not a git repository", http.StatusNotFound)
		return
	}
	out, err := git.ShowCommit(dir, sha)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diff": out})
}

// handleGitStage stages the given paths. Body: { paths: string[] }. Takes
// s.gitMu to avoid racing the agent's own git operations on the index lock.
func (s *Server) handleGitStage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := s.resolveGitDir(r)
	if !git.IsRepo(dir) {
		http.Error(w, "not a git repository", http.StatusNotFound)
		return
	}
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	if err := git.Stage(dir, body.Paths); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGitUnstage unstages the given paths. Body: { paths: string[] }. Takes
// s.gitMu for the same reason as handleGitStage.
func (s *Server) handleGitUnstage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := s.resolveGitDir(r)
	if !git.IsRepo(dir) {
		http.Error(w, "not a git repository", http.StatusNotFound)
		return
	}
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	if err := git.Unstage(dir, body.Paths); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGitCommitCreate commits the currently staged changes. Body:
// { message: string }. Takes s.gitMu. Returns 400 when nothing is staged.
func (s *Server) handleGitCommitCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	msg := strings.TrimSpace(body.Message)
	if msg == "" {
		http.Error(w, "missing message", http.StatusBadRequest)
		return
	}
	dir := s.resolveGitDir(r)
	if !git.IsRepo(dir) {
		http.Error(w, "not a git repository", http.StatusNotFound)
		return
	}
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	if err := git.CommitStaged(dir, msg); err != nil {
		// "Nothing staged" is a client error, not a server error.
		if strings.Contains(err.Error(), "Nothing staged") {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
