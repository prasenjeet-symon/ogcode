package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjeet-symon/ogcode/internal/docindex"
	"github.com/prasenjeet-symon/ogcode/internal/gitignore"
	"github.com/prasenjeet-symon/ogcode/internal/indexer"
)

func (s *Server) handleDocIndexBuildStatus(w http.ResponseWriter, r *http.Request) {
	s.docindexMu.Lock()
	running := s.docindexRunning
	progress := s.indexerProgress
	s.docindexMu.Unlock()

	result := map[string]any{"running": running}
	if running && progress != nil {
		total := progress.Total.Load()
		completed := progress.Completed.Load()
		failed := progress.Failed.Load()
		result["total"] = total
		result["completed"] = completed
		result["failed"] = failed
		if total > 0 {
			result["percent"] = int(float64(completed+failed) / float64(total) * 100)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleBuildDocIndex(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Directory string `json:"directory"`
		Rebuild   bool   `json:"rebuild"`
		Model     string `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	dir := input.Directory
	if dir == "" {
		dir = s.dir
	}

	s.docindexMu.Lock()
	if s.docindexRunning {
		s.docindexMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{"running": true})
		return
	}
	s.docindexRunning = true
	s.docindexMu.Unlock()

	if input.Rebuild {
		if err := s.docindexStore.DeleteAllByPrefix(dir); err != nil {
			s.docindexMu.Lock()
			s.docindexRunning = false
			s.docindexMu.Unlock()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	excludes, err := s.docindexStore.ListExcludes(dir)
	if err != nil {
		slog.Warn("fetch excludes failed, indexing without them", "err", err)
	}
	var excludePatterns []string
	for _, e := range excludes {
		excludePatterns = append(excludePatterns, e.Pattern)
	}

	go func() {
		defer func() {
			s.docindexMu.Lock()
			s.docindexRunning = false
			s.indexerProgress = nil
			s.docindexMu.Unlock()
			s.bus.Publish("docindex.built", map[string]string{"directory": dir})
		}()

		idx := indexer.New(dir, s.docindexStore, s.loopRunner).WithExcludes(excludePatterns)
		if input.Model != "" {
			idx = idx.WithModel(input.Model)
		}

		// Store progress tracker so the status endpoint can report progress.
		s.docindexMu.Lock()
		s.indexerProgress = idx.Progress()
		s.docindexMu.Unlock()

		if err := idx.Run(context.Background()); err != nil {
			slog.Error("docindex build failed", "dir", dir, "err", err)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{"running": true})
}

// handleDocIndexPreview reports what an index run would do before one starts.
//
// It runs the indexer's own walk rather than a second implementation of it, so
// the numbers the dialog shows are the ones the run will act on — including the
// excludes and .gitignore rules currently in force. A preview that drifted from
// the run would be worse than none: it would be trusted.
func (s *Server) handleDocIndexPreview(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("directory")
	if dir == "" {
		dir = s.dir
	}

	excludes, err := s.docindexStore.ListExcludes(dir)
	if err != nil {
		slog.Warn("fetch excludes failed, previewing without them", "err", err)
	}
	var patterns []string
	for _, e := range excludes {
		patterns = append(patterns, e.Pattern)
	}

	plan, err := indexer.New(dir, s.docindexStore, s.loopRunner).WithExcludes(patterns).Preview()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleListIndexedDocs(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("directory")
	if dir == "" {
		dir = s.dir
	}
	docs, err := s.docindexStore.ListDocsSummary(dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if docs == nil {
		docs = []*docindex.DocSummary{}
	}
	writeJSON(w, http.StatusOK, docs)
}

// handleListIndexFiles returns every indexable file in the workspace, each
// annotated with whether the index already holds it. The walk respects the
// same excludes and .gitignore an index run does, so the tree matches what a
// run would see — including files that have not been indexed yet.
func (s *Server) handleListIndexFiles(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("directory")
	if dir == "" {
		dir = s.dir
	}

	excludes, err := s.docindexStore.ListExcludes(dir)
	if err != nil {
		slog.Warn("fetch excludes failed, listing without them", "err", err)
	}
	var patterns []string
	for _, e := range excludes {
		patterns = append(patterns, e.Pattern)
	}

	files, err := indexer.New(dir, s.docindexStore, s.loopRunner).WithExcludes(patterns).FileList()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, files)
}

// maxDocContentBytes caps what the file viewer will pull into the browser.
const maxDocContentBytes = 2 << 20 // 2 MiB

// handleReadDocContent returns the source text of a single file in the
// workspace so the index UI can display it. The path must resolve inside the
// workspace directory; it does not need to be indexed already, because the
// file tree shows files that have not been indexed yet too. A path that is
// inside the workspace is one an index run would have walked, so allowing it
// can only ever hand back something indexing already had access to.
func (s *Server) handleReadDocContent(w http.ResponseWriter, r *http.Request) {
	docPath := r.URL.Query().Get("path")
	if docPath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	dir := r.URL.Query().Get("directory")
	if dir == "" {
		dir = s.dir
	}

	// Resolve symlinks on both sides before comparing: a symlink inside the
	// workspace must not become a read of something outside it.
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		http.Error(w, "workspace unavailable", http.StatusInternalServerError)
		return
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(docPath))
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		http.Error(w, "file is outside the workspace", http.StatusForbidden)
		return
	}

	info, err := os.Stat(resolved)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return
	}

	f, err := os.Open(resolved)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	// Read one byte past the cap so a file sitting exactly on it is not
	// reported as truncated.
	data, err := io.ReadAll(io.LimitReader(f, maxDocContentBytes+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	truncated := len(data) > maxDocContentBytes
	if truncated {
		data = data[:maxDocContentBytes]
	}

	result := map[string]any{
		"path":      docPath,
		"size":      info.Size(),
		"truncated": truncated,
		"binary":    false,
		"content":   "",
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		result["binary"] = true
	} else {
		result["content"] = string(data)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleListExcludes(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("directory")
	if dir == "" {
		dir = s.dir
	}
	if err := s.docindexStore.SeedDefaultExcludes(dir); err != nil {
		slog.Warn("seed excludes failed", "err", err)
	}
	entries, err := s.docindexStore.ListExcludes(dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []*docindex.ExcludeEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleAddExclude(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Directory string `json:"directory"`
		Pattern   string `json:"pattern"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if input.Pattern == "" {
		http.Error(w, "pattern is required", http.StatusBadRequest)
		return
	}
	if input.Directory == "" {
		input.Directory = s.dir
	}
	e, err := s.docindexStore.AddExclude(input.Directory, input.Pattern)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) handleDeleteExclude(w http.ResponseWriter, r *http.Request) {
	if err := s.docindexStore.DeleteExclude(chi.URLParam(r, "id")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// gitignoreRule is one pattern line from a .gitignore, carrying the line it
// sits on so the panel can point at the right place in the file.
type gitignoreRule struct {
	Line    int    `json:"line"`
	Pattern string `json:"pattern"`
	Negated bool   `json:"negated"`
}

// The panel summarises a .gitignore rather than reproducing it, so both lists
// are capped. A repository with more rules than this is not one anybody reads
// in a dialog, and the caps are reported so the UI can say what it left out
// instead of quietly showing a short list as though it were the whole file.
const (
	maxGitignoreRules   = 300
	maxNestedGitignores = 50
)

// handleGitignoreInfo reports what the workspace's .gitignore currently says.
//
// The index has no exclusion policy of its own beyond this file and whatever
// the user adds by hand, so the UI can only teach that honestly if it can show
// the file it is pointing people at. Telling someone "edit .gitignore" while
// being unable to say whether one exists, or what is already in it, is advice
// they have to go and verify elsewhere.
func (s *Server) handleGitignoreInfo(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("directory")
	if dir == "" {
		dir = s.dir
	}
	path := filepath.Join(dir, ".gitignore")

	result := map[string]any{
		"path":      path,
		"exists":    false,
		"rules":     []gitignoreRule{},
		"nested":    []string{},
		"truncated": false,
	}

	rules, truncated, err := readGitignoreRules(path)
	switch {
	case err == nil:
		result["exists"] = true
		result["rules"] = rules
		result["truncated"] = truncated
	case !os.IsNotExist(err):
		slog.Warn("read .gitignore failed", "path", path, "err", err)
	}

	if nested := findNestedGitignores(dir); len(nested) > 0 {
		result["nested"] = nested
	}
	writeJSON(w, http.StatusOK, result)
}

// readGitignoreRules returns the pattern lines of a .gitignore in file order.
//
// Comments and blank lines are dropped because they are not rules, and a count
// that included them would overstate how much the file actually excludes.
func readGitignoreRules(path string) ([]gitignoreRule, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	var rules []gitignoreRule
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if len(rules) >= maxGitignoreRules {
			return rules, true, nil
		}
		rules = append(rules, gitignoreRule{
			Line:    line,
			Pattern: text,
			Negated: strings.HasPrefix(text, "!"),
		})
	}
	return rules, false, sc.Err()
}

// findNestedGitignores lists the .gitignore files below the workspace root.
//
// They belong in the panel because they outrank the root file: a rule in a
// subdirectory decides that subtree, so someone who reads only the root file
// and finds nothing matching still has no explanation for a missing directory.
// The walk prunes ignored directories with the same matcher the indexer uses,
// which keeps it out of vendor and node_modules trees rather than descending
// into the very things the file excludes.
func findNestedGitignores(root string) []string {
	m := gitignore.New(root)
	var found []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if d.IsDir() {
			if path != root && m.Match(path, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != ".gitignore" || filepath.Dir(path) == filepath.Clean(root) {
			return nil
		}
		if len(found) >= maxNestedGitignores {
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	return found
}
