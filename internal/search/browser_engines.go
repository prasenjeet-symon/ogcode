package search

import "github.com/PuerkitoBio/goquery"

// browserEngines are the engines the browser-driven backends run — the macOS
// Safari backend and the Windows Chromium backend. They are the same engines
// the native path uses, reached through the same URL builders and read by the
// same parsers; only the transport differs.
//
// Sharing the whole chain rather than a subset matters more than it looks. A
// browser's advantage is not that it can parse something the native path
// cannot; it is that the requests come from a real browser with the user's own
// cookies, so the rate limiting that eventually catches a bare HTTP client does
// not apply. An engine that is missing here is an engine with no second chance
// once the native path is throttled.
//
// It lives in this untagged file so both backends share one definition rather
// than keeping a copy each, which would drift the moment an engine is added.
var browserEngines = []struct {
	name  string
	url   func(query string, limit int) string
	parse func(*goquery.Document, int) []SearchResult
	// ready is a fragment that only appears once real results have rendered.
	//
	// Byte-count stability is not enough to know a search page is done. Brave
	// answers a fresh browser with a JavaScript challenge that sits perfectly
	// still at ~73KB for about eight seconds before swapping itself for the
	// results — long enough that a stability check calls it loaded, the parser
	// finds nothing, and the engine looks like it blocked us. Waiting for the
	// marker waits for the page the parser actually needs.
	//
	// The Safari backend polls for it; the Chromium backend lets the page's
	// virtual-time budget run instead and reads the finished DOM, so it does
	// not consume this field.
	ready string
	// needsDOM marks an engine whose results exist only after its scripts run.
	// Skipped entirely when the rendered DOM cannot be read, because the served
	// markup for these is an empty shell and loading it would cost a page load
	// for a guaranteed zero results.
	needsDOM bool
}{
	// Google leads when it is reachable at all. It is the best answer to a
	// research query and it is the one engine here that no HTTP client can
	// have: it ships a shell and builds the results in the page, so reading it
	// requires a browser whose DOM we can see. That combination is exactly what
	// these backends are.
	{"google", func(q string, _ int) string { return googleSearchURL(q) }, parseGoogleResults, `<h3`, true},
	{"duckduckgo", func(q string, _ int) string { return duckDuckGoSearchURL(q) }, parseDuckDuckGoResults, `result__a`, false},
	{"brave", func(q string, _ int) string { return braveSearchURL(q) }, parseBraveResults, `data-type="web"`, false},
	{"bing", func(q string, _ int) string { return bingSearchURL(q) }, parseBingResults, `b_algo`, false},
	{"yahoo", yahooSearchURL, parseYahooResults, `class="algo`, false},
}
