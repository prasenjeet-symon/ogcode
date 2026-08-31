package search

import (
	"strings"
	"testing"
)

// TestParseDuckDuckGoResults pins the html-endpoint selectors against a
// recorded response.
//
// Like Yahoo, every result href is wrapped in a redirector, so the assertion
// that nothing points back at duckduckgo.com is what proves the unwrapping
// works: a parse that "succeeds" with ten /l/ tracker URLs is useless to a
// caller whose next step is to fetch them.
func TestParseDuckDuckGoResults(t *testing.T) {
	got := parseDuckDuckGoResults(loadFixture(t, "duckduckgo_results.html"), 10)
	if len(got) < 5 {
		t.Fatalf("parsed %d results from the DuckDuckGo fixture, want at least 5", len(got))
	}
	for i, r := range got {
		if !strings.HasPrefix(r.URL, "http") {
			t.Errorf("result %d: URL %q is not absolute", i, r.URL)
		}
		if strings.Contains(r.URL, "duckduckgo.com") {
			t.Errorf("result %d: URL %q was not unwrapped from the /l/ redirect", i, r.URL)
		}
		if strings.TrimSpace(r.Title) == "" {
			t.Errorf("result %d (%s): empty title", i, r.URL)
		}
	}
	// Snippets are what the ranker reads to choose between candidates, so an
	// engine that parses titles and URLs but silently drops every snippet is
	// still broken for our purposes.
	withSnippet := 0
	for _, r := range got {
		if strings.TrimSpace(r.Snippet) != "" {
			withSnippet++
		}
	}
	if withSnippet*2 < len(got) {
		t.Errorf("only %d of %d results carried a snippet", withSnippet, len(got))
	}
}

func TestParseDuckDuckGoResults_RespectsLimit(t *testing.T) {
	if got := parseDuckDuckGoResults(loadFixture(t, "duckduckgo_results.html"), 3); len(got) != 3 {
		t.Errorf("asked for 3 results, got %d", len(got))
	}
}

func TestResolveDuckDuckGoURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "protocol-relative redirect is unwrapped",
			in:   "//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2Fcontext&rut=abc",
			want: "https://pkg.go.dev/context",
		},
		{
			name: "absolute redirect is unwrapped",
			in:   "https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fa%2Bb",
			want: "https://example.com/a+b",
		},
		{
			name: "a direct href is left alone",
			in:   "https://example.com/page",
			want: "https://example.com/page",
		},
		{
			name: "a redirect with no target is left alone rather than mangled",
			in:   "https://duckduckgo.com/l/?rut=abc",
			want: "https://duckduckgo.com/l/?rut=abc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveDuckDuckGoURL(tt.in); got != tt.want {
				t.Errorf("resolveDuckDuckGoURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestParseBingResults pins Bing's selectors and, more importantly, its
// base64url click-tracker unwrapping.
func TestParseBingResults(t *testing.T) {
	got := parseBingResults(loadFixture(t, "bing_results.html"), 10)
	if len(got) < 5 {
		t.Fatalf("parsed %d results from the Bing fixture, want at least 5", len(got))
	}
	for i, r := range got {
		if !strings.HasPrefix(r.URL, "http") {
			t.Errorf("result %d: URL %q is not absolute", i, r.URL)
		}
		if strings.Contains(r.URL, "bing.com/ck/") {
			t.Errorf("result %d: URL %q was not decoded from the ck/a tracker", i, r.URL)
		}
		if strings.TrimSpace(r.Title) == "" {
			t.Errorf("result %d (%s): empty title", i, r.URL)
		}
	}
}

func TestResolveBingURL(t *testing.T) {
	// "https://pkg.go.dev/context" base64url-encoded, unpadded, behind Bing's
	// two-character tag.
	const encoded = "https://www.bing.com/ck/a?!&&u=a1aHR0cHM6Ly9wa2cuZ28uZGV2L2NvbnRleHQ"

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "tracker is decoded",
			in:   encoded,
			want: "https://pkg.go.dev/context",
		},
		{
			name: "a direct href is left alone",
			in:   "https://example.com/page",
			want: "https://example.com/page",
		},
		{
			name: "a tracker with no u parameter is left alone",
			in:   "https://www.bing.com/ck/a?!&&p=deadbeef",
			want: "https://www.bing.com/ck/a?!&&p=deadbeef",
		},
		{
			name: "undecodable payload is left alone rather than mangled",
			in:   "https://www.bing.com/ck/a?u=a1!!!not-base64!!!",
			want: "https://www.bing.com/ck/a?u=a1!!!not-base64!!!",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveBingURL(tt.in); got != tt.want {
				t.Errorf("resolveBingURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Both new engines were added because a browser fingerprint changed what they
// return, not because their markup changed. If a future refactor drops them
// from the chain the regression is silent — the remaining engines still answer
// — so the chain's membership is pinned.
func TestBuildEngines_ChainOrder(t *testing.T) {
	names := engineNames(buildEngines(""))
	want := []string{"duckduckgo", "brave", "bing", "yahoo"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("engine chain is %v, want %v", names, want)
	}
}

// Every engine has to be able to say what results page it served, because that
// is the Referer a later fetch of one of its results presents. An engine with
// no page builder would silently fetch with no referrer.
func TestBuildEngines_AllReportTheirSearchPage(t *testing.T) {
	for _, e := range buildEngines("https://searx.example.org") {
		if got := e.searchURL("golang context", 8); !strings.HasPrefix(got, "http") {
			t.Errorf("%s: searchURL returned %q, want an absolute URL", e.name, got)
		}
	}
}

// TestParseGoogleResults pins the structural selector against a recorded
// rendered DOM.
//
// The fixture is Safari's document.documentElement.outerHTML for a real search,
// with script and style bodies, SVGs and inline images stripped — the parser
// never looks at any of them, and keeping them would have put 1.3MB in the
// repository for a 340KB test. Everything the selector touches is intact.
//
// It matters that this is a *rendered* capture: Google's served markup contains
// none of these results, which is exactly why the engine is Safari-only.
func TestParseGoogleResults(t *testing.T) {
	got := parseGoogleResults(loadFixture(t, "google_results.html"), 10)
	if len(got) < 5 {
		t.Fatalf("parsed %d results from the Google fixture, want at least 5", len(got))
	}
	for i, r := range got {
		if !strings.HasPrefix(r.URL, "http") {
			t.Errorf("result %d: URL %q is not absolute", i, r.URL)
		}
		if strings.Contains(r.URL, "google.com/") {
			t.Errorf("result %d: URL %q points back into Google", i, r.URL)
		}
		if strings.TrimSpace(r.Title) == "" {
			t.Errorf("result %d (%s): empty title", i, r.URL)
		}
	}
}

func TestParseGoogleResults_RespectsLimit(t *testing.T) {
	if got := parseGoogleResults(loadFixture(t, "google_results.html"), 4); len(got) != 4 {
		t.Errorf("asked for 4 results, got %d", len(got))
	}
}

func TestResolveGoogleURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "redirect is unwrapped",
			in:   "/url?q=https%3A%2F%2Fpkg.go.dev%2Fcontext&sa=U&ved=x",
			want: "https://pkg.go.dev/context",
		},
		{
			name: "a direct href is left alone",
			in:   "https://example.com/page",
			want: "https://example.com/page",
		},
		{
			name: "an internal link with no target is left alone",
			in:   "/url?sa=t&ved=x",
			want: "/url?sa=t&ved=x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveGoogleURL(tt.in); got != tt.want {
				t.Errorf("resolveGoogleURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
