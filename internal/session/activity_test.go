package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/prasenjeet-symon/ogcode/internal/db"
)

// activityDayBase is three consecutive local calendar days as unix
// milliseconds, so a case seeds timestamps in the server's own time zone and
// the expected keys are the strings strftime produces there.
type activityDayBase struct {
	day    string // YYYY-MM-DD
	start  int64  // local midnight, unix ms
	noon   int64
	latest int64 // 23:59 local
}

func newActivityDayBase(t *testing.T, offsetDays int) activityDayBase {
	t.Helper()
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day()+offsetDays, 0, 0, 0, 0, time.Local)
	return activityDayBase{
		day:    start.Format("2006-01-02"),
		start:  start.UnixMilli(),
		noon:   start.Add(12 * time.Hour).UnixMilli(),
		latest: start.Add(23*time.Hour + 59*time.Minute).UnixMilli(),
	}
}

func activityTestStore(t *testing.T) *Store {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return NewStore(database)
}

// seedActivitySession creates a session in dir of the given type, created at
// createdAt, and returns its id.
func seedActivitySession(t *testing.T, store *Store, dir, sessionType string, createdAt int64) SessionID {
	t.Helper()
	id := NewSessionID()
	if err := store.Create(&Session{
		ID: id, ProjectID: dir, Directory: dir, Title: "t",
		SessionType: sessionType, CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return id
}

func seedActivityMessage(t *testing.T, store *Store, sessionID SessionID, at int64) {
	t.Helper()
	if err := store.CreateMessage(&MessageInfo{
		ID: NewMessageID(), SessionID: sessionID, Role: RoleUser, CreatedAt: at,
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}
}

// TestActiveDays pins the sparse per-day session counts behind the activity
// heatmap: a session counts on the day it was created and on every day it
// wrote a message, but once per day; note/index/search sessions and other
// workspaces stay out; and the day keys are the local calendar day.
func TestActiveDays(t *testing.T) {
	yesterday := newActivityDayBase(t, -1)
	today := newActivityDayBase(t, 0)
	twoDaysAgo := newActivityDayBase(t, -2)

	cases := []struct {
		name  string
		seed  func(t *testing.T, store *Store)
		want  map[string]int
		extra func(t *testing.T, got map[string]int)
	}{
		{
			name: "created yesterday counts on yesterday",
			seed: func(t *testing.T, store *Store) {
				seedActivitySession(t, store, "/p", "build", yesterday.noon)
			},
			want: map[string]int{yesterday.day: 1},
		},
		{
			name: "same session counts again on a day it has a message",
			seed: func(t *testing.T, store *Store) {
				id := seedActivitySession(t, store, "/p", "build", twoDaysAgo.noon)
				seedActivityMessage(t, store, id, yesterday.noon)
			},
			want: map[string]int{twoDaysAgo.day: 1, yesterday.day: 1},
		},
		{
			name: "created and messaged the same day counts once",
			seed: func(t *testing.T, store *Store) {
				id := seedActivitySession(t, store, "/p", "build", yesterday.latest)
				seedActivityMessage(t, store, id, yesterday.start)
			},
			want: map[string]int{yesterday.day: 1},
		},
		{
			name: "note, index and search sessions are excluded",
			seed: func(t *testing.T, store *Store) {
				for _, sessionType := range []string{"note", "index", "search"} {
					id := seedActivitySession(t, store, "/p", sessionType, yesterday.noon)
					seedActivityMessage(t, store, id, yesterday.noon)
				}
			},
			want: map[string]int{},
		},
		{
			name: "another workspace is excluded",
			seed: func(t *testing.T, store *Store) {
				id := seedActivitySession(t, store, "/other", "build", yesterday.noon)
				seedActivityMessage(t, store, id, yesterday.noon)
			},
			want: map[string]int{},
		},
		{
			name: "day keys are the local calendar day",
			seed: func(t *testing.T, store *Store) {
				// Created just before local midnight, messaged just after:
				// two days, not one UTC day.
				id := seedActivitySession(t, store, "/p", "build", yesterday.latest)
				seedActivityMessage(t, store, id, today.start)
			},
			want: map[string]int{yesterday.day: 1, today.day: 1},
			extra: func(t *testing.T, got map[string]int) {
				t.Helper()
				for day := range got {
					if _, err := time.ParseInLocation("2006-01-02", day, time.Local); err != nil {
						t.Errorf("day key %q is not a local YYYY-MM-DD date: %v", day, err)
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := activityTestStore(t)
			tc.seed(t, store)

			// Three whole local days ending now; the exclusive upper bound is
			// the start of tomorrow.
			from := twoDaysAgo.start
			to := newActivityDayBase(t, 1).start
			got, err := store.ActiveDays("/p", from, to)
			if err != nil {
				t.Fatalf("ActiveDays: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ActiveDays = %v, want %v", got, tc.want)
			}
			for day, want := range tc.want {
				if got[day] != want {
					t.Errorf("day %s = %d sessions, want %d (all: %v)", day, got[day], want, got)
				}
			}
			if tc.extra != nil {
				tc.extra(t, got)
			}
		})
	}
}

// TestActiveDaysRangeIsHalfOpen pins the range bounds: from is included and to
// is excluded, so a day at the upper edge is not counted.
func TestActiveDaysRangeIsHalfOpen(t *testing.T) {
	store := activityTestStore(t)
	yesterday := newActivityDayBase(t, -1)
	today := newActivityDayBase(t, 0)
	seedActivitySession(t, store, "/p", "build", today.noon)

	got, err := store.ActiveDays("/p", yesterday.start, today.start)
	if err != nil {
		t.Fatalf("ActiveDays: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ActiveDays = %v, want no days before the upper bound", got)
	}
}
