package agent

import (
	"log/slog"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// previewDomainEnv overrides the domain live-preview services are served under
// when this server is reached over the internet rather than on localhost.
const previewDomainEnv = "OGCODE_PREVIEW_DOMAIN"

// defaultPreviewDomain is the preview host suffix used when nothing overrides
// it. *.localhost resolves to loopback in browsers and curl (RFC 6761), so
// 3000.preview.localhost reaches this server on the user's own machine with no
// DNS or TLS setup at all.
const defaultPreviewDomain = "preview.localhost"

// PreviewDomain is the host suffix a loopback service is served under: the
// service on port 3000 is at http://3000.<domain>/. It is fixed for the process
// (an env-derived value), so a prompt section naming it stays byte-identical for
// the whole session and can live in the cacheable base.
//
// The configured value is normalized — case folded, a leading "*." or "." and a
// trailing "." dropped — and one that is not a bare DNS name (a scheme, a port,
// a path, an IP address) falls back to the default with a warning. The domain is
// matched against every request's Host and written into URLs and the system
// prompt, so a malformed one would otherwise match nothing and fail silently.
func PreviewDomain() string {
	raw := os.Getenv(previewDomainEnv)
	if strings.TrimSpace(raw) == "" {
		return defaultPreviewDomain
	}
	d, ok := normalizePreviewDomain(raw)
	if !ok {
		if _, warned := badPreviewDomainWarned.LoadOrStore(raw, true); !warned {
			slog.Warn("OGCODE_PREVIEW_DOMAIN is not a bare DNS name; using the default",
				"value", raw, "default", defaultPreviewDomain)
		}
		return defaultPreviewDomain
	}
	return d
}

// badPreviewDomainWarned remembers which malformed OGCODE_PREVIEW_DOMAIN values
// have been warned about, so a value read on every request logs once.
var badPreviewDomainWarned sync.Map

// normalizePreviewDomain folds a configured preview domain to its canonical
// form and reports whether it is a usable DNS name: dot-separated labels of
// letters, digits and inner hyphens, not ending in a numeric label (which would
// make it an IP address rather than a name a subdomain can hang off).
func normalizePreviewDomain(raw string) (string, bool) {
	d := strings.ToLower(strings.TrimSpace(raw))
	d = strings.TrimPrefix(d, "*.")
	d = strings.Trim(d, ".")
	if d == "" || len(d) > 253 {
		return "", false
	}
	labels := strings.Split(d, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", false
			}
		}
	}
	if _, err := strconv.Atoi(labels[len(labels)-1]); err == nil {
		return "", false
	}
	return d, true
}

// PreviewDomainIsLoopbackOnly reports whether domain is a *.localhost name.
// Such a name resolves to the machine the BROWSER runs on, so its preview URLs
// reach this server only from a browser on this server's own machine (or
// through a tunnel that forwards this server's port to the browser's machine);
// a browser anywhere else needs a real wildcard domain.
func PreviewDomainIsLoopbackOnly(domain string) bool {
	return domain == "localhost" || strings.HasSuffix(domain, ".localhost")
}

// ParsePreviewPort reads a port spelled as a preview host label or path
// segment: 1–5 ASCII digits, no sign and no leading zero, in 1–65535. It is
// stricter than strconv.Atoi, which also takes "+3000" and "03000", because one
// port must have exactly one spelling — a preview origin and the published-port
// check keyed on it must not be reachable under an alias.
func ParsePreviewPort(s string) (int, bool) {
	if len(s) == 0 || len(s) > 5 || s[0] == '0' {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if n > 65535 {
		return 0, false
	}
	return n, true
}

// previewAnnounceRe matches a live-preview URL under the default domain; see
// previewAnnounceRegexp.
var previewAnnounceRe = regexp.MustCompile(`(\d{1,5})\.` + regexp.QuoteMeta(defaultPreviewDomain))

// previewAnnounceRegexp matches <port>.<domain> in lower-cased text. The host
// boundaries on either side are checked by AnnouncedPreviewPorts itself: RE2 has
// no lookaround, and \b would accept a port inside a longer name
// (a.3000.preview.localhost, x-3000.preview.localhost) that the dispatcher does
// not route.
func previewAnnounceRegexp(domain string) *regexp.Regexp {
	if domain == defaultPreviewDomain {
		return previewAnnounceRe
	}
	return regexp.MustCompile(`(\d{1,5})\.` + regexp.QuoteMeta(domain))
}

// isHostByte reports whether c can be part of a hostname label.
func isHostByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// AnnouncedPreviewPorts returns the ports of the live-preview URLs in text —
// the <port>.<domain> form the agent is told to hand the user when it starts a
// service. It is deliberately narrow: a bare loopback URL (127.0.0.1:5432) an
// agent merely mentions is not an announcement, so it is left out, and so is a
// port label inside a longer hostname (a.3000.<domain>, 3000.<domain>.evil) the
// dispatcher would not route. The match is case-insensitive, like the Host the
// dispatcher reads; the result is deduped and sorted.
func AnnouncedPreviewPorts(text string) []int {
	if text == "" {
		return nil
	}
	domain := PreviewDomain()
	lower := strings.ToLower(text)
	found := map[int]bool{}
	for _, m := range previewAnnounceRegexp(domain).FindAllStringSubmatchIndex(lower, -1) {
		start, end := m[0], m[1]
		// The port label must start the hostname: nothing host-like (and no
		// dot — that would make it a nested label) directly before it.
		if start > 0 && (isHostByte(lower[start-1]) || lower[start-1] == '.') {
			continue
		}
		// And the domain must end it: a following label (3000.<domain>.evil)
		// names some other host. A lone trailing dot — the end of a sentence —
		// is fine.
		if end < len(lower) {
			next := lower[end]
			if isHostByte(next) {
				continue
			}
			if next == '.' && end+1 < len(lower) && isHostByte(lower[end+1]) {
				continue
			}
		}
		if n, ok := ParsePreviewPort(lower[m[2]:m[3]]); ok {
			found[n] = true
		}
	}
	if len(found) == 0 {
		return nil
	}
	ports := make([]int, 0, len(found))
	for p := range found {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// PreviewURL is the URL the user's browser opens to reach the loopback service
// on port, given the origin the browser is already using for this server. Each
// service gets its own hostname (3000.preview.localhost) rather than a path
// prefix, so an app that bootstraps off window.location.pathname — Next.js and
// friends — sees its own origin root and starts normally. The origin's scheme
// and port are carried over so the URL works the same locally and through a
// tunnel; a preview host always carries the same numeric port as the service
// name, so it never collides with the server's own host.
func PreviewURL(origin string, port int) string {
	scheme := "http"
	hostSuffix := ""
	if u, err := url.Parse(origin); err == nil && u.Host != "" {
		if u.Scheme != "" {
			scheme = u.Scheme
		}
		if _, p, err := net.SplitHostPort(u.Host); err == nil && p != "" {
			hostSuffix = ":" + p
		}
	}
	return scheme + "://" + strconv.Itoa(port) + "." + PreviewDomain() + hostSuffix + "/"
}

// originIsLoopback reports whether origin names this machine by a loopback
// name — the only case in which a *.localhost preview URL, which the browser
// resolves to its OWN machine, lands back on this server. An unparseable or
// empty origin is treated as loopback, so a missing origin never triggers a
// remote-browser warning it cannot back up.
func originIsLoopback(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return true
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
