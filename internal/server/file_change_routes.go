package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/filechange"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

const (
	// fileChangeReadMax caps one page of history, the way handleBoardRead caps
	// one page of posts: a UI panel polls this, so it never asks for everything.
	fileChangeReadMax = 50
	// fileChangeOpTimeout bounds one history query. It outlives a UI request but
	// must not outlive a page render, since nothing else waits on it.
	fileChangeOpTimeout = 10 * time.Second
)

// handleFileChanges returns the mutation history the file-change gate recorded,
// newest first: who changed which file, what they said they were doing, and the
// diff. It is the review surface the plan keeps for later debugging — the gate
// itself renders its own catch-up from memory.
func (s *Server) handleFileChanges(w http.ResponseWriter, r *http.Request) {
	if s.fileChanges == nil {
		writeError(w, http.StatusServiceUnavailable, "the file change log is not available")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), fileChangeOpTimeout)
	defer cancel()

	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > fileChangeReadMax {
		limit = fileChangeReadMax
	}
	// The table is keyed by FileKey, not by a display path, so a relative path
	// is resolved exactly as the file tools resolve it.
	path := strings.TrimSpace(q.Get("path"))
	if path != "" {
		path = tool.FileKey(s.dir, path)
	}

	changes, err := s.fileChanges.List(ctx, filechange.Query{Path: path, Before: before, Limit: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The cursor is the oldest row on this page: passing it back as `before`
	// pages further into the past, and an empty page leaves it where it was.
	cursor := before
	if len(changes) > 0 {
		cursor = changes[len(changes)-1].ID
	}
	// A row stores the absolute FileKey, resolved through symlinks; the client
	// shows project paths, so compare against the project root resolved the same
	// way. A path outside the project is left as it is.
	root := tool.FileKey(s.dir, ".")
	for i := range changes {
		rel, err := filepath.Rel(root, changes[i].Path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		changes[i].Path = filepath.ToSlash(rel)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"changes": changes,
		"cursor":  cursor,
	})
}
