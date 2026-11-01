package server

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/prasenjeet-symon/ogcode/internal/memfile"
	"github.com/prasenjeet-symon/ogcode/internal/project"
)

const (
	// searchDefaultLimit is how many sessions one query returns when the
	// client does not say — enough to fill the palette without a scroll.
	searchDefaultLimit = 20
	// searchMaxLimit caps ?limit=: the answer is a per-session digest with up
	// to three turn snippets each, so a larger page is a bigger payload, not a
	// better answer.
	searchMaxLimit = 50
	// searchMinQueryRunes is the shortest query accepted. One character is
	// almost never a real question, and answering it means scanning the whole
	// memory corpus — so it is refused rather than made cheap.
	searchMinQueryRunes = 2
)

// searchSessionsResponse is what the palette receives: the query the answer
// belongs to, so a client that fires queries faster than answers can drop a
// response that arrived after a newer keystroke.
type searchSessionsResponse struct {
	Query   string        `json:"query"`
	Results []memfile.Hit `json:"results"`
}

// handleSearchSessions answers GET /api/session/search?q=&limit= — the sessions
// whose memory is about the query, ranked by the store (see
// internal/memfile/search.go). It is the server half of the ⌘K palette: the
// session list can only filter titles, and this is the question "which
// conversation was that about?".
//
// Registered as a static segment above the {sessionID} subrouter so chi's tree
// prefers it; if it ever moved below, `search` would be read as a session id.
func (s *Server) handleSearchSessions(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(q) < searchMinQueryRunes {
		writeError(w, http.StatusBadRequest, "query too short")
		return
	}

	// A server without the store (tests, another embedder) or with per-turn
	// memory switched off has nothing to search: answer an empty page rather
	// than a nil dereference, so the palette degrades to its recents list.
	if s.search == nil || !memfile.TurnMemoryEnabled() {
		writeJSON(w, http.StatusOK, searchSessionsResponse{Query: q, Results: []memfile.Hit{}})
		return
	}

	limit := searchDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(n, searchMaxLimit)
		}
	}

	// Hits are scoped to this server's project; the directory is passed as the
	// corpus root because the store reads .ogcode/memory/ from disk on its
	// first call (the lazy backfill).
	projectID := project.Resolve(s.dir)
	hits, err := s.search.Search(r.Context(), projectID, s.dir, q, limit)
	if err != nil {
		// The palette aborts the previous keystroke's request on every new one
		// (its own AbortController, so a stale answer cannot overwrite a newer
		// one), and nothing is left to answer a request whose client is gone.
		// The store runs on r.Context(), so the abort surfaces here as
		// "context canceled" mid-query: that is the palette working, not a
		// failure, and logging it at ERROR turned ordinary typing into a wall
		// of "search sessions" errors.
		if r.Context().Err() != nil {
			return
		}
		slog.Error("search sessions", "query", q, "err", err)
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	// JSON null reads as "no such list" to the client; an empty answer is a
	// list with no rows.
	if hits == nil {
		hits = []memfile.Hit{}
	}
	writeJSON(w, http.StatusOK, searchSessionsResponse{Query: q, Results: hits})
}
