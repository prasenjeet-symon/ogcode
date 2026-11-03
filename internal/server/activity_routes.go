package server

import (
	"net/http"
	"sort"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/usage"
)

// activityDefaultDays is how far back the series reaches when ?from is absent:
// one year of calendar days, so a heatmap always has a year to draw.
const activityDefaultDays = 366

// activityDay is one local day of the activity series: how many distinct
// sessions worked in this workspace that day and the tokens they spent.
type activityDay struct {
	Day      string `json:"day"` // YYYY-MM-DD, local time
	Sessions int    `json:"sessions"`
	Tokens   int    `json:"tokens"`
}

// activitySeries is the sparse per-day activity behind the web heatmap: only
// days with any activity appear, in ascending day order.
type activitySeries struct {
	From int64         `json:"from"`
	To   int64         `json:"to"`
	Days []activityDay `json:"days"`
}

// handleActivity returns this workspace's per-day activity between ?from and ?to
// (unix ms, from inclusive and to exclusive; both optional, defaulting to the
// last 366 days up to now). Sessions are counted from the project's own
// database and tokens from the global spend ledger, both scoped to this
// workspace — the ledger rows written under the workspace directory, its task
// worktrees included.
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	from, err := msParam(r, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, "from: "+err.Error())
		return
	}
	to, err := msParam(r, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, "to: "+err.Error())
		return
	}
	if to <= 0 {
		to = time.Now().UnixMilli()
	}
	if from <= 0 {
		from = time.UnixMilli(to).AddDate(0, 0, -activityDefaultDays).UnixMilli()
	}

	active, err := s.store.ActiveDays(s.dir, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sum, err := usage.Summarize(s.usage.DB(), s.registry, from, to, s.dir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	days := make(map[string]*activityDay, len(active))
	for day, sessions := range active {
		days[day] = &activityDay{Day: day, Sessions: sessions}
	}
	// A ledger day can exist without a session row — usage from a session
	// since deleted — so the token days are merged in their own right.
	for _, d := range sum.Days {
		if a := days[d.Day]; a != nil {
			a.Tokens = d.Effective
			continue
		}
		days[d.Day] = &activityDay{Day: d.Day, Tokens: d.Effective}
	}

	out := make([]activityDay, 0, len(days))
	for _, d := range days {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })

	writeJSON(w, http.StatusOK, activitySeries{From: from, To: to, Days: out})
}
