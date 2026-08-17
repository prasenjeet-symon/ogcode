package agent

import (
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/search"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func TestTuning(t *testing.T) {
	// nil SearchParams → built-in defaults
	lr := &LoopRunner{}
	if got := lr.tuning(); got.fetchTopK != session.DefaultSearchFetchTopK ||
		got.pageChars != session.DefaultSearchPageChars {
		t.Fatalf("nil SearchParams: got %+v, want defaults", got)
	}

	// explicit override is honoured
	lr.SearchParams = func() session.SearchConfig {
		return session.SearchConfig{FetchTopK: 8, PageChars: 12000}
	}
	if got := lr.tuning(); got.fetchTopK != 8 || got.pageChars != 12000 {
		t.Fatalf("override: got %+v, want {8,12000}", got)
	}

	// zero values from the accessor fall back to defaults (defensive)
	lr.SearchParams = func() session.SearchConfig { return session.SearchConfig{} }
	if got := lr.tuning(); got.fetchTopK != session.DefaultSearchFetchTopK ||
		got.pageChars != session.DefaultSearchPageChars {
		t.Fatalf("zero accessor: got %+v, want defaults", got)
	}
}

func TestExtractFirstURL(t *testing.T) {
	tests := map[string]string{
		"https://go.dev/doc":            "https://go.dev/doc",
		"1. https://go.dev/doc install": "https://go.dev/doc",
		"see [x](https://go.dev/x).":    "https://go.dev/x",
		"<http://a.b/c>":                "http://a.b/c",
		"no url here":                   "",
		"trailing http://a.b/c,":        "http://a.b/c",
	}
	for in, want := range tests {
		if got := extractFirstURL(in); got != want {
			t.Errorf("extractFirstURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstInt(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"3", 3, true},
		{"  12 ", 12, true},
		{"pick 7 please", 7, true},
		{"none", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := firstInt(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("firstInt(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestURLKeyAndDomain(t *testing.T) {
	if urlKey(" https://a.b/c/ ") != "https://a.b/c" {
		t.Errorf("urlKey trailing-slash/space normalisation failed")
	}
	if domainOf("https://www.go.dev/doc/install") != "go.dev" {
		t.Errorf("domainOf should strip www and path")
	}
	if domainOf("not a url") != "not a url" {
		t.Errorf("domainOf should return input when unparseable")
	}
}

func TestIsJunkResultHost(t *testing.T) {
	junk := []string{"https://www.google.us/", "https://google.com/search?q=x", "https://bing.com/", "https://duckduckgo.com/"}
	for _, u := range junk {
		if !isJunkResultHost(u) {
			t.Errorf("isJunkResultHost(%q) = false, want true", u)
		}
	}
	good := []string{"https://go.dev/doc", "https://github.com/golang/go", "https://stackoverflow.com/q/1"}
	for _, u := range good {
		if isJunkResultHost(u) {
			t.Errorf("isJunkResultHost(%q) = true, want false", u)
		}
	}
}

func TestPickFromLine(t *testing.T) {
	candidates := []search.SearchResult{
		{Title: "A", URL: "https://a.dev/x"},
		{Title: "B", URL: "https://b.dev/y"},
		{Title: "C", URL: "https://c.dev/z"},
	}
	byURL := map[string]search.SearchResult{}
	for _, c := range candidates {
		byURL[urlKey(c.URL)] = c
	}

	// by index
	if c, ok := pickFromLine("2", candidates, byURL); !ok || c.Title != "B" {
		t.Errorf("index pick failed: %+v ok=%v", c, ok)
	}
	// by real URL
	if c, ok := pickFromLine("https://c.dev/z", candidates, byURL); !ok || c.Title != "C" {
		t.Errorf("url pick failed: %+v ok=%v", c, ok)
	}
	// hallucinated URL is rejected (not in candidate set)
	if _, ok := pickFromLine("https://evil.example/hallucinated", candidates, byURL); ok {
		t.Errorf("hallucinated URL should be rejected")
	}
	// out-of-range index rejected
	if _, ok := pickFromLine("9", candidates, byURL); ok {
		t.Errorf("out-of-range index should be rejected")
	}
	// no signal
	if _, ok := pickFromLine("just some words", candidates, byURL); ok {
		t.Errorf("line with no number/url should be rejected")
	}
}

func TestAppendSourcesIfMissing(t *testing.T) {
	pages := []search.PageContent{{URL: "https://a.dev/x", Title: "A"}}
	picks := []search.SearchResult{{URL: "https://b.dev/y", Title: "B"}}

	// already has a section → unchanged
	withSection := "# Answer\n\n## Sources\n1. x"
	if got := appendSourcesIfMissing(withSection, picks, pages); got != withSection {
		t.Errorf("should not append when Sources already present")
	}

	// prefers fetched pages
	got := appendSourcesIfMissing("# Answer", picks, pages)
	if !containsAll(got, "## Sources", "https://a.dev/x", "A") {
		t.Errorf("expected sources from fetched pages, got:\n%s", got)
	}

	// falls back to picks when no pages were fetched
	got = appendSourcesIfMissing("# Answer", picks, nil)
	if !containsAll(got, "## Sources", "https://b.dev/y") {
		t.Errorf("expected sources from picks fallback, got:\n%s", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
