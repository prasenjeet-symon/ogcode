package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
)

// previewPrefix is the legacy ogcode-side URL prefix a live local service used
// to be reached under. It survives only as a redirect shim: a request to
// /preview/3000/foo is redirected to the service's own hostname
// (http://3000.preview.localhost:PORT/foo). Live services are served at
// <port>.<preview-domain>, so an app that bootstraps off its own origin sees
// "/" and works unchanged.
const previewPrefix = "/preview"

// previewTargetKey carries the upstream base URL (scheme + host, no path) for
// one request through the shared reverse proxy. The proxy itself is built once,
// but the target changes per request, so the port travels on the context rather
// than in a proxy per port (which would grow without bound as ports come and go).
type previewTargetKey struct{}

// parsePreviewPath splits the escaped path after /preview/ into the loopback
// port and the upstream path, still escaped. ok is false when the first segment
// is not a usable port, so a typo like /preview/xyz/ answers a clear 400 instead
// of dialing garbage.
func parsePreviewPath(rest string) (port int, upath string, ok bool) {
	seg, tail, _ := strings.Cut(rest, "/")
	p, ok := agent.ParsePreviewPort(seg)
	if !ok {
		return 0, "", false
	}
	return p, "/" + tail, true
}

// parsePreviewHost reads a request Host against the preview domain (suffix).
// It strips a trailing :port (the ogcode server's own port, which the browser
// includes on localhost) and a trailing root dot, then reports:
//
//   - inDomain: the host is the preview domain itself or any name under it.
//     That whole namespace belongs to previews, so such a host must never fall
//     through to ogcode's own UI and API — an operator may expose the preview
//     wildcard more widely than the main host (the point of a preview link is
//     to be shared), and a host like x.<domain> would otherwise be a way around
//     whatever guards the main one.
//   - port, ok: the host is exactly <port>.<domain> with a canonical port label
//     — digits only, no sign or leading zero, in range. A nested label
//     (3000.a.<domain>), a non-numeric one, or the bare domain is in the domain
//     but not a preview (ok=false).
//
// A host that merely ends in the suffix text (evil-preview.localhost) is not in
// the domain, and an empty suffix matches nothing.
func parsePreviewHost(host, suffix string) (port int, ok, inDomain bool) {
	suffix = strings.Trim(strings.ToLower(strings.TrimSpace(suffix)), ".")
	if suffix == "" {
		return 0, false, false
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		if _, err := strconv.Atoi(host[i+1:]); err == nil {
			host = host[:i]
		}
	}
	h := strings.TrimSuffix(strings.ToLower(host), ".")
	if h == suffix {
		return 0, false, true
	}
	if !strings.HasSuffix(h, "."+suffix) {
		return 0, false, false
	}
	label := strings.TrimSuffix(h, "."+suffix)
	if strings.Contains(label, ".") {
		return 0, false, true
	}
	p, ok := agent.ParsePreviewPort(label)
	return p, ok, true
}

// previewBaseURL is the loopback origin a preview request dials. The host is a
// literal here — never anything from the request — so the hostname's port is
// the only thing a caller controls and it can only ever reach 127.0.0.1. That
// is what keeps this from becoming an SSRF lever onto other hosts. It is not
// what keeps it safe on this host: a loopback port is often private precisely
// BECAUSE it is loopback-only (a database, a debugger, the Docker API, this
// server itself), so the dispatcher also requires the port to be published —
// see previewRefusal.
func previewBaseURL(port int) *url.URL {
	return &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}
}

// requestScheme is the scheme the client used to reach this server: https when
// the connection is TLS or a TLS-terminating proxy in front says so through
// X-Forwarded-Proto, http otherwise. Any other X-Forwarded-Proto value is
// ignored rather than echoed.
func requestScheme(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))); proto == "http" || proto == "https" {
		scheme = proto
	}
	return scheme
}

// previewProxy lazily builds the reverse proxy that forwards a preview request
// to the loopback service on the port its hostname names. Built once and
// shared: the per-request target rides on the request context, read by Rewrite
// below.
var (
	previewProxyOnce sync.Once
	previewProxy     *httputil.ReverseProxy
)

func getPreviewProxy() *httputil.ReverseProxy {
	previewProxyOnce.Do(func() {
		previewProxy = &httputil.ReverseProxy{
			// Rewrite, not Director, so the target can come from the request
			// context per call. SetURL joins the (empty) target path with the
			// request path, leaving the upstream path intact; the query string
			// is carried through untouched. It also sets the upstream Host to
			// 127.0.0.1:<port>, which is what a dev server's DNS-rebinding guard
			// (Vite, webpack-dev-server) accepts.
			Rewrite: func(pr *httputil.ProxyRequest) {
				if base, ok := pr.In.Context().Value(previewTargetKey{}).(*url.URL); ok && base != nil {
					pr.SetURL(base)
				}
				// Keep the chain a proxy in front of this server started, then
				// append the peer this server saw — what a proxy-aware app reads
				// to learn the client, as behind any other reverse proxy.
				if prior, ok := pr.In.Header["X-Forwarded-For"]; ok {
					pr.Out.Header["X-Forwarded-For"] = append([]string(nil), prior...)
				}
				pr.SetXForwarded()
				// SetXForwarded derives the proto from this hop alone, which is
				// plain http behind a TLS-terminating proxy. An app that builds
				// absolute URLs or enforces https from it would then emit http://
				// links (mixed content) or redirect-loop, so pass on the scheme the
				// browser actually used.
				pr.Out.Header.Set("X-Forwarded-Proto", requestScheme(pr.In))
			},
			// Flush immediately: a player or an SSE stream must reach the
			// browser live rather than buffering (same contract as the scrcpy
			// and controlplane tunnel proxies). WebSocket upgrades are handled
			// by ReverseProxy itself.
			FlushInterval: -1,
			// The preview exists to embed a service in an iframe, so a service
			// that guards itself with X-Frame-Options or a CSP frame-ancestors
			// directive would otherwise paint a blank box. Drop those headers on
			// the way through — the target is the user's own loopback process, so
			// there is no third party being un-framed.
			//
			// The body is never touched: because each service is served at its
			// own origin root, its HTML needs no rewriting. The one place a
			// service names its own origin in a header is a redirect, and there
			// it sees 127.0.0.1:<port> — the Host this proxy sends — so a
			// Location on that origin is mapped back onto the preview host.
			ModifyResponse: func(resp *http.Response) error {
				resp.Header.Del("X-Frame-Options")
				stripFrameAncestors(resp.Header)
				rewritePreviewLocation(resp)
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				where := ""
				if base, ok := r.Context().Value(previewTargetKey{}).(*url.URL); ok && base != nil {
					where = base.String() + r.URL.Path
				}
				http.Error(w,
					"no service is reachable at "+where+": "+err.Error(),
					http.StatusBadGateway)
			},
		}
	})
	return previewProxy
}

// rewritePreviewLocation maps a redirect that names the upstream's own
// loopback origin (http://127.0.0.1:<port>/…, localhost, [::1]) back onto the
// preview origin the browser is using. Such a Location is exactly what an app
// that builds absolute URLs from its Host header emits behind this proxy, and
// followed as-is it sends the browser to ITS OWN machine's loopback — nothing
// there for a viewer on another machine, and out of the preview for a local
// one. A relative Location, or one naming any other origin, is left alone.
func rewritePreviewLocation(resp *http.Response) {
	loc := resp.Header.Get("Location")
	if loc == "" || resp.Request == nil {
		return
	}
	u, err := url.Parse(loc)
	if err != nil || u.Host == "" {
		return
	}
	if u.Scheme != "" && u.Scheme != "http" {
		return
	}
	upstream := resp.Request.URL
	if upstream == nil || u.Port() != upstream.Port() || !isLoopbackHostname(u.Hostname()) {
		return
	}
	in, ok := resp.Request.Context().Value(previewInboundKey{}).(previewInbound)
	if !ok || in.host == "" {
		return
	}
	u.Scheme = in.scheme
	u.Host = in.host
	resp.Header.Set("Location", u.String())
}

// isLoopbackHostname reports whether a URL hostname names this machine's
// loopback interface.
func isLoopbackHostname(h string) bool {
	h = strings.ToLower(h)
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// previewInboundKey carries the browser-facing origin of a preview request
// (its scheme and Host) to ModifyResponse, which sees only the outbound
// request.
type previewInboundKey struct{}

type previewInbound struct {
	scheme string
	host   string
}

// previewRefusal decides whether the preview host for port may be served, and
// why not when it may not. Two ports are refused:
//
//   - this server's own port. Proxied, the preview host would be a second door
//     into ogcode's whole API — the agent, its shell, the settings — on a
//     hostname an operator may well have left outside the authentication that
//     guards the main one, and it is never a service anyone needs previewed.
//
//   - any port that is not published: announced by this project's agent in a
//     live-preview URL, or added on the Preview page. "Only loopback" keeps the
//     proxy from reaching other hosts, but a loopback port is often private
//     precisely because it is loopback-only — a database console, a debugger, a
//     cloud CLI's local endpoint, another ogcode — and a preview hostname is
//     meant to be shared. So it opens exactly the ports someone chose to show.
//
// A lookup failure refuses too: serving an unvetted port is the worse error.
func (s *Server) previewRefusal(port int) string {
	if port == s.Port() {
		return "port " + strconv.Itoa(port) + " is this ogcode server itself, which is never served as a preview"
	}
	ok, err := s.store.PreviewPortPublished(port)
	if err != nil {
		slog.Warn("preview: published-port lookup failed; refusing", "port", port, "err", err)
		return "could not check whether port " + strconv.Itoa(port) + " is published; try again"
	}
	if !ok {
		return "port " + strconv.Itoa(port) + " is not a published preview on this server. " +
			"A port is served here once the agent hands back its preview URL, or once it is added on the Preview page."
	}
	return ""
}

// previewHostDispatch routes any request whose Host names a preview subdomain
// (<port>.<preview-domain>) to the loopback service on that port. It runs ahead
// of every other middleware except the request id, so a preview host answers for
// every path — including /api/... — as the service itself would, and gets none
// of ogcode's own plumbing:
//
//   - not the CORS middleware, which would answer every OPTIONS itself (the
//     service's own preflight handling never ran) and stamp
//     Access-Control-Allow-Origin: * on every response — duplicating a
//     service's own header, which browsers reject, and otherwise opening each
//     loopback service to reads from any website.
//   - not RealIP, which believes a client's X-Forwarded-For; the proxy passes on
//     the peer it actually saw.
//
// Any other name under the preview domain answers 404 rather than reaching
// ogcode (see parsePreviewHost). Every request under the domain — refused ones
// included, which is where probing shows up — is access-logged. The request
// path is forwarded verbatim: the service is at its own origin root, so "/"
// stays "/".
func (s *Server) previewHostDispatch(next http.Handler) http.Handler {
	preview := accessLog(slog.LevelInfo)(http.HandlerFunc(s.servePreviewHost))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, _, inDomain := parsePreviewHost(req.Host, agent.PreviewDomain()); !inDomain {
			next.ServeHTTP(w, req)
			return
		}
		preview.ServeHTTP(w, req)
	})
}

// servePreviewHost answers one request whose Host is under the preview domain:
// a 404 for a name that is not <port>.<domain>, a 403 for a port that may not
// be served (previewRefusal), and otherwise the proxied service.
func (s *Server) servePreviewHost(w http.ResponseWriter, req *http.Request) {
	port, ok, _ := parsePreviewHost(req.Host, agent.PreviewDomain())
	if !ok {
		http.Error(w, "preview: "+req.Host+" is not a preview address; expected <port>."+agent.PreviewDomain(), http.StatusNotFound)
		return
	}
	// CONNECT asks a proxy for a raw tunnel; a preview is an HTTP origin, so
	// there is nothing to tunnel to.
	if req.Method == http.MethodConnect {
		http.Error(w, "preview: CONNECT is not supported", http.StatusMethodNotAllowed)
		return
	}
	if why := s.previewRefusal(port); why != "" {
		http.Error(w, "preview: "+why, http.StatusForbidden)
		return
	}
	ctx := context.WithValue(req.Context(), previewTargetKey{}, previewBaseURL(port))
	ctx = context.WithValue(ctx, previewInboundKey{}, previewInbound{scheme: requestScheme(req), host: req.Host})
	getPreviewProxy().ServeHTTP(w, req.WithContext(ctx))
}

// servePreview keeps the legacy /preview/<port>/… form working as a redirect
// shim onto the service's own hostname. Nothing is proxied here any more: a
// request to /preview/3000/foo answers a redirect to
// http://3000.preview.localhost/foo (on the ogcode origin so the port carries
// over), and the browser re-requests it on the preview host, where
// previewHostDispatch proxies it.
//
// The redirect is temporary (307): its target depends on the preview domain and
// on the origin this request came in by, neither of which a browser may cache
// forever. A port that is not published is sent to the Preview page instead
// (303), where it can be added — its hostname would only answer 403.
//
// The exact, port-less /preview is deliberately left unregistered so it falls
// through to the SPA, where the preview page lives.
func (s *Server) servePreview(r chiRouter) {
	r.Handle(previewPrefix+"/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rest := strings.TrimPrefix(req.URL.EscapedPath(), previewPrefix+"/")
		// A trailing slash with nothing after it (/preview/) is not a target:
		// send it to the port-less page rather than answering 400, so the URL a
		// user types by hand lands where they meant.
		if rest == "" {
			http.Redirect(w, req, previewPrefix, http.StatusMovedPermanently)
			return
		}
		port, upath, ok := parsePreviewPath(rest)
		if !ok {
			http.Error(w,
				"preview: expected /preview/<port>/…, got "+req.URL.Path,
				http.StatusBadRequest)
			return
		}
		if port == s.Port() {
			http.Error(w, "preview: port "+strconv.Itoa(port)+" is this ogcode server itself", http.StatusBadRequest)
			return
		}
		if published, err := s.store.PreviewPortPublished(port); err != nil || !published {
			http.Redirect(w, req, previewPrefix+"?port="+strconv.Itoa(port), http.StatusSeeOther)
			return
		}
		u, err := url.Parse(agent.PreviewURL(requestOrigin(req), port))
		if err != nil {
			http.Error(w, "preview: cannot build redirect: "+err.Error(), http.StatusBadRequest)
			return
		}
		u.Path, err = url.PathUnescape(upath)
		if err != nil {
			http.Error(w, "preview: malformed path: "+err.Error(), http.StatusBadRequest)
			return
		}
		u.RawPath = upath
		u.RawQuery = req.URL.RawQuery
		http.Redirect(w, req, u.String(), http.StatusTemporaryRedirect)
	}))
}

// stripFrameAncestors drops the frame-ancestors directive from the
// Content-Security-Policy (and its Report-Only sibling) headers, so a service
// that forbids framing still renders inside the preview's iframe.
func stripFrameAncestors(h http.Header) {
	for _, key := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		vals := h.Values(key)
		if len(vals) == 0 {
			continue
		}
		kept := make([]string, 0, len(vals))
		for _, v := range vals {
			if s := stripFrameAncestorsOne(v); s != "" {
				kept = append(kept, s)
			}
		}
		h.Del(key)
		for _, s := range kept {
			h.Add(key, s)
		}
	}
}

// stripFrameAncestorsOne drops the frame-ancestors directive from one
// Content-Security-Policy header value, leaving the rest intact.
func stripFrameAncestorsOne(v string) string {
	parts := strings.Split(v, ";")
	kept := parts[:0]
	for _, p := range parts {
		if f := strings.Fields(p); len(f) > 0 && strings.EqualFold(f[0], "frame-ancestors") {
			continue
		}
		kept = append(kept, p)
	}
	if strings.TrimSpace(strings.Join(kept, "")) == "" {
		return ""
	}
	return strings.Join(kept, ";")
}
