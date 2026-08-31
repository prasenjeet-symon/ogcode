package search

import (
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"

	utls "github.com/refraction-networking/utls"
)

// A persona is one coherent browser identity: a TLS ClientHello, the User-Agent
// that browser sends, and the exact header set it sends alongside it.
//
// Coherence is the whole point, and it is worth being precise about why. Bot
// detection does not look for "a bot"; it looks for combinations no real
// browser produces. A Go client claiming to be Chrome is caught not because the
// User-Agent is wrong but because the TLS handshake underneath it is Go's — and
// a client that fixes the handshake but then advertises Chrome 133 in its
// ClientHello while its sec-ch-ua header says 131 has simply moved the
// contradiction somewhere else. Every field below therefore comes from one
// browser at one version, and they are only ever changed together.
//
// That constraint also fixes the version. uTLS can only forge the ClientHellos
// it ships specs for, and Chrome 133 is the newest it has; so the Chrome
// personas say 133 rather than something more current. A slightly old browser
// is unremarkable in real traffic. A brand-new User-Agent over an old
// handshake is not.
type persona struct {
	name  string
	hello utls.ClientHelloID

	userAgent      string
	accept         string
	acceptLanguage string
	acceptEncoding string

	// Client hints. Chromium-family browsers send these on every navigation;
	// Firefox and Safari send none, and inventing them for those personas would
	// be exactly the kind of contradiction described above.
	secCHUA         string
	secCHUAMobile   string
	secCHUAPlatform string

	// secFetch reports whether this browser sends the Sec-Fetch-* metadata
	// headers. Safari 16.0 predates them.
	secFetch bool
}

// personas is the pool one is drawn from per backend instance.
//
// Chrome dominates real traffic, so two of the four are Chrome on the two
// desktop platforms that matter. Firefox and Safari are included because their
// handshakes are genuinely different shapes, not just different version
// numbers, and a population where every ogcode user presents an identical
// ClientHello is its own signal.
var personas = []persona{
	{
		name:            "chrome-133-macos",
		hello:           utls.HelloChrome_133,
		userAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
		accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
		acceptLanguage:  "en-US,en;q=0.9",
		acceptEncoding:  "gzip, deflate, br, zstd",
		secCHUA:         `"Not(A:Brand";v="99", "Google Chrome";v="133", "Chromium";v="133"`,
		secCHUAMobile:   "?0",
		secCHUAPlatform: `"macOS"`,
		secFetch:        true,
	},
	{
		name:            "chrome-133-windows",
		hello:           utls.HelloChrome_133,
		userAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36",
		accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
		acceptLanguage:  "en-US,en;q=0.9",
		acceptEncoding:  "gzip, deflate, br, zstd",
		secCHUA:         `"Not(A:Brand";v="99", "Google Chrome";v="133", "Chromium";v="133"`,
		secCHUAMobile:   "?0",
		secCHUAPlatform: `"Windows"`,
		secFetch:        true,
	},
	{
		name:           "firefox-120-windows",
		hello:          utls.HelloFirefox_120,
		userAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:120.0) Gecko/20100101 Firefox/120.0",
		accept:         "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		acceptLanguage: "en-US,en;q=0.5",
		// Firefox only gained zstd in 126, so 120 must not advertise it.
		acceptEncoding: "gzip, deflate, br",
		secFetch:       true,
	},
	{
		name:           "safari-16-macos",
		hello:          utls.HelloSafari_16_0,
		userAgent:      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Safari/605.1.15",
		accept:         "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		acceptLanguage: "en-US,en;q=0.9",
		acceptEncoding: "gzip, deflate, br",
		// Sec-Fetch-* landed in Safari 16.4.
		secFetch: false,
	},
}

// pickPersona chooses one identity for the lifetime of a backend.
//
// Per process, not per request. Rotating the User-Agent between requests is a
// popular idea and an actively harmful one: a single IP whose browser changes
// from request to request is a pattern no household produces, and it is easier
// to spot than any one User-Agent. Real variety comes from different ogcode
// installs drawing differently, which this gives us for free.
func pickPersona() persona { return personas[rand.IntN(len(personas))] }

// personaByName returns the named persona, for OGCODE_SEARCH_PERSONA. The
// second result reports whether the name matched.
func personaByName(name string) (persona, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range personas {
		if p.name == name {
			return p, true
		}
	}
	return persona{}, false
}

// navigation describes how a request would have been reached in a browser,
// which is what the Sec-Fetch-* headers encode. Getting these wrong is a
// cheaper tell than the User-Agent: a "navigation" that claims no referrer
// while carrying one, or a document request marked as a subresource fetch, is
// a combination the browser itself would never emit.
type navigation struct {
	// referer is the page the user would have been on. Empty for a URL typed
	// into the address bar — which is what a search query is.
	referer string
}

// applyHeaders sets the headers this persona sends on a top-level document
// navigation, in the order the browser lists them.
//
// The order is written out deliberately even though Go will not preserve it on
// the wire: net/http sorts header keys for HTTP/1.1, and x/net/http2 sorts them
// again when encoding HPACK. Reproducing a browser's header order would mean
// forking both, which is a large amount of surface to own for one signal —
// and the Safari backend, which is a browser and therefore gets the order right
// for free, already exists for targets that check it. Keeping the source order
// honest means that if we ever do own that code, the intent is already here.
func (p persona) applyHeaders(req *http.Request, nav navigation) {
	set := func(k, v string) {
		if v != "" {
			req.Header.Set(k, v)
		}
	}

	set("sec-ch-ua", p.secCHUA)
	set("sec-ch-ua-mobile", p.secCHUAMobile)
	set("sec-ch-ua-platform", p.secCHUAPlatform)
	set("upgrade-insecure-requests", "1")
	set("user-agent", p.userAgent)
	set("accept", p.accept)

	if nav.referer != "" {
		set("referer", nav.referer)
	}

	if p.secFetch {
		// sec-fetch-site is the one field here that carries real information:
		// "none" means the URL was typed or bookmarked, "cross-site" means it
		// was clicked from a different origin. Sending "none" while also
		// sending a Referer is self-contradictory, so the two are derived from
		// the same fact.
		set("sec-fetch-site", secFetchSite(req.URL, nav.referer))
		set("sec-fetch-mode", "navigate")
		set("sec-fetch-user", "?1")
		set("sec-fetch-dest", "document")
	}

	set("accept-encoding", p.acceptEncoding)
	set("accept-language", p.acceptLanguage)
}

// secFetchSite classifies the relationship between the referring page and the
// target, the way a browser does.
func secFetchSite(target *url.URL, referer string) string {
	if referer == "" {
		return "none"
	}
	ref, err := url.Parse(referer)
	if err != nil || ref.Host == "" || target == nil {
		return "cross-site"
	}
	if strings.EqualFold(ref.Host, target.Host) {
		return "same-origin"
	}
	if sameRegistrableSuffix(ref.Host, target.Host) {
		return "same-site"
	}
	return "cross-site"
}

// sameRegistrableSuffix is a deliberately rough same-site test: it compares the
// last two labels of each host. A full public-suffix list would be exact, but
// the only consumer is a header whose value is "same-site" versus "cross-site"
// on a request we are making anyway, and being wrong about co.uk costs nothing
// that a whole PSL dependency would buy back.
func sameRegistrableSuffix(a, b string) bool {
	return lastLabels(a, 2) == lastLabels(b, 2)
}

func lastLabels(host string, n int) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	parts := strings.Split(host, ".")
	if len(parts) <= n {
		return host
	}
	return strings.Join(parts[len(parts)-n:], ".")
}
