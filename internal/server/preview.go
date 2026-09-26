package server

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// previewPrefix is the ogcode-side URL prefix a live local service is reached
// under. A request to /preview/3000/foo is forwarded to 127.0.0.1:3000/foo, so a
// dev server, a player or a dashboard running on this machine is browsable at
// the ogcode origin with no port forwarding.
const previewPrefix = "/preview"

// previewTargetKey carries the upstream base URL (scheme + host, no path) for
// one request through the shared reverse proxy. The proxy itself is built once,
// but the target changes per request, so the port travels on the context rather
// than in a proxy per port (which would grow without bound as ports come and go).
type previewTargetKey struct{}

// parsePreviewPath splits the path after /preview/ into the loopback port and
// the upstream path. ok is false when the first segment is not a usable port, so
// a typo like /preview/xyz/ answers a clear 400 instead of dialing garbage.
func parsePreviewPath(rest string) (port int, upath string, ok bool) {
	seg, tail, _ := strings.Cut(rest, "/")
	p, err := strconv.Atoi(seg)
	if err != nil || p < 1 || p > 65535 {
		return 0, "", false
	}
	if tail == "" {
		return p, "/", true
	}
	return p, "/" + tail, true
}

// previewBaseURL is the loopback origin a preview request dials. The host is a
// literal here — never anything from the request — so the path's port is the
// only thing a caller controls and it can only ever reach 127.0.0.1. That is
// what keeps this from becoming an SSRF lever onto other hosts, and it is why
// there is no allowlist: loopback-only is the whole rule.
func previewBaseURL(port int) *url.URL {
	return &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}
}

// previewProxy lazily builds the reverse proxy that forwards /preview/<port>/* to
// the loopback service on that port. Built once and shared: the per-request
// target rides on the request context, read by Rewrite below.
var (
	previewProxyOnce sync.Once
	previewProxy     *httputil.ReverseProxy
)

func getPreviewProxy() *httputil.ReverseProxy {
	previewProxyOnce.Do(func() {
		previewProxy = &httputil.ReverseProxy{
			// Rewrite, not Director, so the target can come from the request
			// context per call. SetURL joins the (empty) target path with the
			// already-stripped request path, leaving the upstream path intact;
			// the query string is carried through untouched.
			Rewrite: func(pr *httputil.ProxyRequest) {
				if base, ok := pr.In.Context().Value(previewTargetKey{}).(*url.URL); ok && base != nil {
					pr.SetURL(base)
				}
				pr.SetXForwarded()
			},
			// Flush immediately: a player or an SSE stream must reach the
			// browser live rather than buffering (same contract as the scrcpy
			// and controlplane tunnel proxies). WebSocket upgrades are handled
			// by ReverseProxy itself.
			FlushInterval: -1,
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

// servePreview wires a live local service onto /preview/<port>/*. The port in
// the path selects a 127.0.0.1 target; the rest of the path is forwarded as-is
// (the /preview/<port> prefix is stripped), so an app that serves at its own
// root — a dev server, an HLS player page — works unchanged.
//
// This is dumb plumbing on purpose, the same shape as serveScrcpy: the local
// process stays independent and ogcode learns nothing about what it serves.
// Registered before the SPA fallback so /preview paths never answer with the
// embedded index.html; the exact, port-less /preview is deliberately left
// unregistered so it falls through to the SPA, where the preview page lives.
func (s *Server) servePreview(r chiRouter) {
	r.Handle(previewPrefix+"/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rest := strings.TrimPrefix(req.URL.Path, previewPrefix+"/")
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
		// Redirect only the bare /preview/<port> to a trailing slash: a document
		// served at /preview/3000 resolves its relative links against /preview/,
		// not /preview/3000/, which would send them to the wrong place (the same
		// reason serveScrcpy redirects /scrcpy). A deeper path like
		// /preview/3000/app.js must pass through untouched — it is a real
		// upstream path, not a prefix that needs a slash.
		if !strings.Contains(rest, "/") {
			http.Redirect(w, req, req.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		req = req.WithContext(context.WithValue(req.Context(), previewTargetKey{}, previewBaseURL(port)))
		req.URL.Path = upath
		req.URL.RawPath = ""
		getPreviewProxy().ServeHTTP(w, req)
	}))
}

// previewReachable reports whether a loopback service answers on port, for UI
// hints and tests. It dials the root: whatever the app serves, an HTTP answer
// means the process is up. Bounded, because the preview page polls it — an
// unresponsive peer must not hold the endpoint open.
func previewReachable(port int) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(previewBaseURL(port).String() + "/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}

// handlePreviewStatus answers GET /api/preview/status?port=N with whether a
// service answers on that loopback port and where the proxy would dial. The
// preview page polls this to choose between embedding the service and showing
// the "nothing there" hint.
func (s *Server) handlePreviewStatus(w http.ResponseWriter, req *http.Request) {
	port, err := strconv.Atoi(req.URL.Query().Get("port"))
	if err != nil || port < 1 || port > 65535 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "expected ?port=<1-65535>",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"up":     previewReachable(port),
		"target": previewBaseURL(port).String(),
	})
}
