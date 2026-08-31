package search

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// loadFixture parses a recorded response from testdata.
func loadFixture(t *testing.T, name string) *goquery.Document {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	doc, err := goquery.NewDocumentFromReader(f)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return doc
}

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// TestParseYahooResults pins the Yahoo selectors against a recorded response.
// Yahoo wraps every result href in an r.search.yahoo.com click tracker, so the
// assertion that no URL points back at yahoo.com is what proves the unwrapping
// works — a parse that "succeeds" with ten tracker URLs is useless to the
// caller, which has to fetch them.
func TestParseYahooResults(t *testing.T) {
	got := parseYahooResults(loadFixture(t, "yahoo_results.html"), 10)
	if len(got) == 0 {
		t.Fatal("parsed no results from the Yahoo fixture")
	}
	for i, r := range got {
		if !strings.HasPrefix(r.URL, "http") {
			t.Errorf("result %d: URL %q is not absolute", i, r.URL)
		}
		if strings.Contains(r.URL, "yahoo.com") {
			t.Errorf("result %d: URL %q was not unwrapped from the Yahoo redirect", i, r.URL)
		}
		if strings.TrimSpace(r.Title) == "" {
			t.Errorf("result %d (%s): empty title", i, r.URL)
		}
	}
}

func TestResolveYahooURL(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{
			"unwraps tracker",
			"https://r.search.yahoo.com/_ylt=Awr;_ylu=Y29s/RV=2/RE=1789038649/RO=10/RU=https%3a%2f%2fpkg.go.dev%2fcontext/RK=2/RS=abc-",
			"https://pkg.go.dev/context",
		},
		{
			// %2b must survive as "+", not become a space: Google Source URLs
			// carry a literal + in the path.
			"preserves plus in path",
			"https://r.search.yahoo.com/RV=2/RU=https%3a%2f%2fchromium.googlesource.com%2fsrc%2f%2b%2fmaster%2fdocs.md/RK=2/",
			"https://chromium.googlesource.com/src/+/master/docs.md",
		},
		{
			"passes through a direct URL",
			"https://pkg.go.dev/context",
			"https://pkg.go.dev/context",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveYahooURL(tt.in); got != tt.want {
				t.Errorf("resolveYahooURL() = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestParseBraveResults(t *testing.T) {
	got := parseBraveResults(loadFixture(t, "brave_results.html"), 10)
	if len(got) == 0 {
		t.Fatal("parsed no results from the Brave fixture")
	}
	for i, r := range got {
		if !strings.HasPrefix(r.URL, "http") {
			t.Errorf("result %d: URL %q is not absolute", i, r.URL)
		}
		if strings.Contains(r.URL, "brave.com") {
			t.Errorf("result %d: URL %q points back at the engine", i, r.URL)
		}
		if strings.TrimSpace(r.Title) == "" {
			t.Errorf("result %d (%s): empty title", i, r.URL)
		}
	}
}

// TestParseResultsRespectsLimit guards the caller's contract: the pipeline sizes
// its ranking prompt by the limit it asked for.
func TestParseResultsRespectsLimit(t *testing.T) {
	if got := parseBraveResults(loadFixture(t, "brave_results.html"), 3); len(got) != 3 {
		t.Errorf("brave: want 3 results, got %d", len(got))
	}
	if got := parseYahooResults(loadFixture(t, "yahoo_results.html"), 3); len(got) != 3 {
		t.Errorf("yahoo: want 3 results, got %d", len(got))
	}
}

// TestExtractReadableArticle covers the happy path: a server-rendered doc page
// that Readability handles on its own.
func TestExtractReadableArticle(t *testing.T) {
	title, text := extractReadable(fixtureBytes(t, "article_pkggodev.html"), "https://pkg.go.dev/context")
	if !strings.Contains(strings.ToLower(title), "context") {
		t.Errorf("title = %q, want it to mention the page subject", title)
	}
	if len(text) < 5000 {
		t.Errorf("extracted %d chars, want a substantial article body", len(text))
	}
	if !strings.Contains(text, "Context") {
		t.Error("extracted text does not contain the article's subject matter")
	}
}

// TestExtractReadableRecoversFromShortReadability is the reason the structural
// fallback exists. On this page Readability locks onto the consent banner and
// returns a couple hundred characters, discarding the documentation entirely.
// Without the fallback the deep-research pipeline would synthesise an answer
// from a cookie notice and cite it as a source.
func TestExtractReadableRecoversFromShortReadability(t *testing.T) {
	body := fixtureBytes(t, "appshell_docs.html")
	_, text := extractReadable(body, "https://docs.anthropic.com/en/docs/build-with-claude/tool-use")

	if len(text) < readabilityMinChars {
		t.Fatalf("extracted only %d chars; the structural fallback did not engage", len(text))
	}
	if strings.HasPrefix(text, "Cookie settings") {
		t.Errorf("extracted the consent banner instead of the page body: %.120q", text)
	}
}

func TestTruncateCharsIsRuneSafe(t *testing.T) {
	// Byte-slicing "héllo" at 3 would split the é and produce invalid UTF-8.
	got, truncated := truncateChars("héllo", 3)
	if !truncated || got != "hél" {
		t.Errorf("truncateChars(\"héllo\", 3) = %q,%v; want \"hél\",true", got, truncated)
	}
	if got, truncated := truncateChars("hi", 5); truncated || got != "hi" {
		t.Errorf("short string should pass through unchanged, got %q,%v", got, truncated)
	}
}

func TestIsUsableResultURL(t *testing.T) {
	tests := []struct {
		url, engine string
		want        bool
	}{
		{"https://pkg.go.dev/context", "bing.com", true},
		{"https://www.bing.com/images/search", "bing.com", false},
		{"https://bing.com/ck/a", "bing.com", false},
		{"javascript:void(0)", "bing.com", false},
		{"/relative/path", "bing.com", false},
		{"https://notbing.com/page", "bing.com", true},
	}
	for _, tt := range tests {
		if got := isUsableResultURL(tt.url, tt.engine); got != tt.want {
			t.Errorf("isUsableResultURL(%q, %q) = %v; want %v", tt.url, tt.engine, got, tt.want)
		}
	}
}

// TestTTLCacheExpires pins lazy eviction: an entry past its TTL must read as a
// miss rather than serving stale search results for the rest of the process.
func TestTTLCacheExpires(t *testing.T) {
	c := newTTLCache[string](-time.Second) // already expired on write
	c.set("k", "v")
	if _, ok := c.get("k"); ok {
		t.Error("expired entry was served from the cache")
	}

	fresh := newTTLCache[string](time.Minute)
	fresh.set("k", "v")
	if v, ok := fresh.get("k"); !ok || v != "v" {
		t.Errorf("fresh entry = %q,%v; want \"v\",true", v, ok)
	}
}
