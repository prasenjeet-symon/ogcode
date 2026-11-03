package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/usage"
)

// activityTestDay is one local calendar day as unix milliseconds, so the test
// seeds and asserts the same day boundaries the handler buckets by.
type activityTestDay struct {
	day   string // YYYY-MM-DD
	start int64  // local midnight, unix ms
	noon  int64
}

func newActivityTestDay(t *testing.T, offsetDays int) activityTestDay {
	t.Helper()
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day()+offsetDays, 0, 0, 0, 0, time.Local)
	return activityTestDay{
		day:   start.Format("2006-01-02"),
		start: start.UnixMilli(),
		noon:  start.Add(12 * time.Hour).UnixMilli(),
	}
}

// newActivityTestServer is newTestServer with the activity handler's own
// dependencies wired: the project session store and a ledger writing into the
// global DB under this workspace, exactly as the real server does.
func newActivityTestServer(t *testing.T) *Server {
	t.Helper()
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	srv.usage = usage.NewLedger(srv.globalDB, srv.dir)
	return srv
}

func activityItoa(v int64) string { return strconv.FormatInt(v, 10) }

func getActivity(t *testing.T, srv *Server, query string) (*httptest.ResponseRecorder, activitySeries) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/activity"+query, nil)
	srv.handleActivity(rec, req)
	var series activitySeries
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &series); err != nil {
			t.Fatalf("decode activity series: %v (body: %s)", err, rec.Body.String())
		}
	}
	return rec, series
}

// TestHandleActivity pins the wire shape the heatmap consumes: one sparse
// entry per local day that saw a session or spend, ascending, carrying the
// distinct session count from the project DB and the effective tokens from the
// ledger.
func TestHandleActivity(t *testing.T) {
	srv := newActivityTestServer(t)
	yesterday := newActivityTestDay(t, -1)
	today := newActivityTestDay(t, 0)
	twoDaysAgo := newActivityTestDay(t, -2)
	tomorrow := newActivityTestDay(t, 1)

	id := session.NewSessionID()
	if err := srv.store.Create(&session.Session{
		ID: id, ProjectID: srv.dir, Directory: srv.dir, Title: "t",
		SessionType: "build", CreatedAt: yesterday.noon, UpdatedAt: yesterday.noon,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	// The same session writes a message today, so it counts again today.
	if err := srv.store.CreateMessage(&session.MessageInfo{
		ID: session.NewMessageID(), SessionID: id, Role: session.RoleUser, CreatedAt: today.noon,
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	// Effective tokens are input + cache_write + output; cache reads and
	// reasoning rides inside output and must not be added again.
	srv.usage.RecordStep(string(id), "msg_yesterday", "openai", "gpt-test",
		session.TokenCounts{Input: 100, Output: 20, CacheWrite: 5, CacheRead: 999}, yesterday.noon)
	srv.usage.RecordStep(string(id), "msg_today", "openai", "gpt-test",
		session.TokenCounts{Input: 10, Output: 1}, today.noon)

	t.Run("sparse days in ascending order", func(t *testing.T) {
		rec, series := getActivity(t, srv, "?from="+activityItoa(twoDaysAgo.start)+"&to="+activityItoa(tomorrow.start))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/activity = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		if series.From != twoDaysAgo.start || series.To != tomorrow.start {
			t.Errorf("from/to = %d/%d, want %d/%d", series.From, series.To, twoDaysAgo.start, tomorrow.start)
		}
		want := []activityDay{
			{Day: yesterday.day, Sessions: 1, Tokens: 125},
			{Day: today.day, Sessions: 1, Tokens: 11},
		}
		if len(series.Days) != len(want) {
			t.Fatalf("days = %+v, want %+v", series.Days, want)
		}
		for i, w := range want {
			if series.Days[i] != w {
				t.Errorf("day %d = %+v, want %+v", i, series.Days[i], w)
			}
		}
	})

	t.Run("default range is the last 366 days", func(t *testing.T) {
		rec, series := getActivity(t, srv, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/activity = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		if delta := time.Since(time.UnixMilli(series.To)); delta < 0 || delta > time.Minute {
			t.Errorf("to = %d (%s), want about now", series.To, time.UnixMilli(series.To))
		}
		wantFrom := time.UnixMilli(series.To).AddDate(0, 0, -activityDefaultDays).UnixMilli()
		if series.From != wantFrom {
			t.Errorf("from = %d, want %d (%d days before to)", series.From, wantFrom, activityDefaultDays)
		}
	})

	t.Run("empty range serialises an empty array", func(t *testing.T) {
		rec, series := getActivity(t, srv, "?from="+activityItoa(tomorrow.start)+"&to="+activityItoa(tomorrow.start+86400000))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/activity = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		if len(series.Days) != 0 {
			t.Fatalf("days = %+v, want none", series.Days)
		}
		if body := rec.Body.String(); !strings.Contains(body, `"days":[]`) {
			t.Errorf("body = %s, want an empty array rather than null", body)
		}
	})

	t.Run("bad from is a 400 in the usage handler's shape", func(t *testing.T) {
		rec, _ := getActivity(t, srv, "?from=soon")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/activity = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode error body: %v (body: %s)", err, rec.Body.String())
		}
		if !strings.HasPrefix(body["error"], "from:") {
			t.Errorf("error = %q, want a from: message", body["error"])
		}
	})
}
