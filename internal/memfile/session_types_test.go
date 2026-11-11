package memfile

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"testing"
)

// A session type is "main" — a conversation a person opened — unless it is in
// the non-main set. That one set is written down in five places across two
// languages and no compiler ties them together:
//
//	internal/memfile/search.go                mainSessionFilter        which hits the palette shows
//	internal/agent/loop.go                    turnMemorableSession     which turns get a memory summary
//	web/src/components/session-palette.tsx    NON_MAIN_SESSION_TYPES   which rows "Continue" offers
//	internal/session/store.go                 Store.List               which sessions are listed
//	web/src/context/desktop-notification.tsx  SUPPRESSED_SESSION_TYPES which loops stay silent
//
// mainSessionFilter's comment claims it mirrors turnMemorableSession, and the
// palette's comment claims it mirrors mainSessionFilter, so those three must be
// identical. The last two are deliberately narrower — a listing hides
// housekeeping only, a notification is suppressed only for a loop whose progress
// the person follows elsewhere — and are pinned as subsets: a type added there
// alone still has to be classified everywhere, while today's narrower policy
// stays legal.
//
// The sets are read out of the source because nothing exports them and this
// package cannot import internal/agent (agent imports memfile). An exported
// predicate in internal/session would be the real single source of truth; until
// loop.go can consult it, this test is what fails when one copy moves alone.
func TestNonMainSessionTypesAgree(t *testing.T) {
	identical := []nonMainSite{
		{
			name:   "mainSessionFilter (memfile search)",
			path:   "internal/memfile/search.go",
			anchor: `mainSessionFilter = [^\n]*NOT IN \(([^)]*)\)`,
			item:   "'([^']*)'",
		},
		{
			name:   "turnMemorableSession (agent loop)",
			path:   "internal/agent/loop.go",
			anchor: `(?s)func turnMemorableSession\(sessionType string\) bool \{[^}]*?case ([^:]*):`,
			item:   `"([^"]*)"`,
		},
		{
			name:   "NON_MAIN_SESSION_TYPES (session palette)",
			path:   "web/src/components/session-palette.tsx",
			anchor: `NON_MAIN_SESSION_TYPES = new Set\(\[([^\]]*)\]\)`,
			item:   "'([^']*)'",
		},
	}

	want := identical[0].types(t)
	for _, site := range identical[1:] {
		if got := site.types(t); !slices.Equal(got, want) {
			t.Errorf("%s names %v, want the set mainSessionFilter names: %v", site.name, got, want)
		}
	}

	for _, site := range []nonMainSite{
		{
			name:   "Store.List (session store)",
			path:   "internal/session/store.go",
			anchor: `(?s)func \(s \*Store\) List\(directory string\).*?session_type NOT IN \(([^)]*)\)`,
			item:   "'([^']*)'",
		},
		{
			name:   "SUPPRESSED_SESSION_TYPES (desktop notifications)",
			path:   "web/src/context/desktop-notification.tsx",
			anchor: `SUPPRESSED_SESSION_TYPES = new Set\(\[([^\]]*)\]\)`,
			item:   "'([^']*)'",
		},
	} {
		for _, typ := range site.types(t) {
			if !slices.Contains(want, typ) {
				t.Errorf("%s hides %q, which mainSessionFilter still treats as a main conversation", site.name, typ)
			}
		}
	}
}

// nonMainSite is one hardcoded copy of the set: the file, a regexp whose capture
// group holds the quoted list, and the regexp that pulls one type name out of it.
type nonMainSite struct {
	name   string
	path   string // repo-relative, slash separated
	anchor string
	item   string
}

// types reads the site and returns its session types, sorted.
func (s nonMainSite) types(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(s.path)))
	if err != nil {
		t.Fatalf("%s: read %s: %v", s.name, s.path, err)
	}
	loc := regexp.MustCompile(s.anchor).FindStringSubmatchIndex(string(raw))
	if len(loc) < 4 || loc[2] < 0 {
		t.Fatalf("%s: %q not found in %s — the declaration moved or was rewritten, so this pin reads nothing", s.name, s.anchor, s.path)
	}
	region := string(raw)[loc[2]:loc[3]]
	var found []string
	for _, m := range regexp.MustCompile(s.item).FindAllStringSubmatch(region, -1) {
		found = append(found, m[1])
	}
	if len(found) == 0 {
		t.Fatalf("%s: no session types found in %q", s.name, region)
	}
	sort.Strings(found)
	return found
}
