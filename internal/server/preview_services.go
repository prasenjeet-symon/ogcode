package server

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
)

// previewProbeTimeout bounds one reachability probe. An unresponsive peer — a
// raw TCP service that accepts a connection and then says nothing — must not
// hold the endpoint open. It is a ceiling, not a cost: a service that answers
// does so at once.
const previewProbeTimeout = 2 * time.Second

// previewProbeConcurrency caps how many ports are dialed at once. The preview
// page polls this on a timer, so the herd is kept small.
const previewProbeConcurrency = 12

// previewProbeMaxRedirects bounds how many same-origin redirects the probe
// follows to reach the page a tile would show (/ → /en/ → /en/login).
const previewProbeMaxRedirects = 5

// previewMaxRequestedPorts caps the ?ports= list. The page names at most the one
// port its URL deep-links to; the cap only keeps a hand-made request from
// ballooning the answer.
const previewMaxRequestedPorts = 8

// previewTitleBytes caps how much of a service's page is read to find its
// title. The head of the document is where <title> lives; reading further would
// pull whole apps down just to label a tile.
const previewTitleBytes = 64 * 1024

// previewTitleRe matches the document title. Dot-matches-all and
// case-insensitive, so a title split across lines is still found.
var previewTitleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// previewService is one loopback service the preview grid offers: what to name
// its tile, where the proxy dials, and whether it is answering right now.
type previewService struct {
	// Port is the loopback port the service listens on.
	Port int `json:"port"`
	// Title is the service's own <title>, or "" when it served none. The grid
	// falls back to the port when this is empty.
	Title string `json:"title"`
	// Target is where the proxy dials (always http://127.0.0.1:<port>).
	Target string `json:"target"`
	// URL is the service's own hostname URL on this server
	// (http://<port>.<preview-domain>[:server-port]/), which the grid links to
	// and embeds. Each service answers at its own origin root, so an app that
	// bootstraps off its origin works unchanged.
	URL string `json:"url"`
	// Up is true when the service answered, so the grid embeds it rather than
	// greying the tile. Always false for an unpublished port, which is never
	// probed.
	Up bool `json:"up"`
	// HTML is true when the service answered with a text/html page, so the
	// grid knows it can be embedded in an iframe. A service that answered but
	// with JSON or plain text, or redirected off its own origin, is up but not
	// HTML.
	HTML bool `json:"html"`
	// Published is true when the port is served at its preview hostname — the
	// agent announced it, or the user added it — and false for a port the page
	// only named (a deep link), which is listed so the user can add it but is
	// neither probed nor proxied until they do.
	Published bool `json:"published"`
}

// dropSelf removes the server's own port from a port list: a tile mirroring
// the ogcode UI inside the grid is a self-reference, and the proxy refuses that
// port anyway.
func dropSelf(ports []int, self int) []int {
	out := make([]int, 0, len(ports))
	for _, p := range ports {
		if p != self {
			out = append(out, p)
		}
	}
	return out
}

// parseRequestedPorts reads the ?ports= list: comma-separated loopback ports
// the page wants listed whether or not they are published. A malformed entry is
// skipped rather than failing the request — one bad port must not blank the
// whole grid — and the list is capped at previewMaxRequestedPorts.
func parseRequestedPorts(raw string) []int {
	var out []int
	seen := map[int]bool{}
	for _, seg := range strings.Split(raw, ",") {
		p, ok := agent.ParsePreviewPort(strings.TrimSpace(seg))
		if !ok || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) == previewMaxRequestedPorts {
			break
		}
	}
	return out
}

// previewProbeClient dials loopback services and follows a redirect only while
// it stays on the probed service's own origin — a relative Location, or one
// naming a loopback host on the same port. That reaches the page a tile would
// actually show (an app whose root redirects to /en/ or /login is still an
// app), while a redirect anywhere else is the service's answer, not an
// invitation: following it would send the probe off this machine, which the
// probe must never do. Every hop dials the literal 127.0.0.1, whatever
// loopback name the Location used.
var previewProbeClient = &http.Client{
	CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= previewProbeMaxRedirects {
			return http.ErrUseLastResponse
		}
		origin := via[0].URL
		if next.URL.Scheme != "http" || next.URL.Port() != origin.Port() || !isLoopbackHostname(next.URL.Hostname()) {
			return http.ErrUseLastResponse
		}
		next.URL.Host = origin.Host
		return nil
	},
}

// probePreviewService dials a loopback service's root and reports whether it
// answered, whether it served HTML, and its <title>. Asking for HTML and
// keeping only HTML answers is what separates an app worth embedding from the
// machine's other listeners — AirPlay answers 403, a database answers nothing,
// Ollama answers plain text.
func probePreviewService(port int) (title string, up, isHTML bool) {
	ctx, cancel := context.WithTimeout(context.Background(), previewProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, previewBaseURL(port).String()+"/", nil)
	if err != nil {
		return "", false, false
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := previewProbeClient.Do(req)
	if err != nil {
		return "", false, false
	}
	defer resp.Body.Close()
	// A 5xx is the service saying it is broken, not up.
	if resp.StatusCode >= 500 {
		return "", false, false
	}
	// Still a redirect after following every same-origin hop: it leaves the
	// service's origin (a login provider, another port) or loops. The service
	// answered, but a frame cannot show where it goes, so the tile offers a new
	// tab instead of an embed.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return "", true, false
	}
	isHTML = strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html")
	body, _ := io.ReadAll(io.LimitReader(resp.Body, previewTitleBytes))
	if m := previewTitleRe.FindSubmatch(body); m != nil {
		title = cleanTitle(string(m[1]))
	}
	return title, true, isHTML
}

// cleanTitle turns a raw <title> into a one-line label: entities decoded,
// whitespace collapsed, and long titles clipped rune-safely.
func cleanTitle(raw string) string {
	t := strings.Join(strings.Fields(html.UnescapeString(raw)), " ")
	if r := []rune(t); len(r) > 80 {
		t = strings.TrimSpace(string(r[:80])) + "…"
	}
	return t
}

// probePreviewResult is one port's probe outcome, keyed by port in the map
// probePreviewPorts returns.
type probePreviewResult struct {
	title  string
	up     bool
	isHTML bool
}

// probePreviewPorts dials every port concurrently, bounded by
// previewProbeConcurrency. Concurrency is the point: serially, a grid of
// several services would add a timeout per dead port to every poll.
func probePreviewPorts(ports []int) map[int]probePreviewResult {
	out := make(map[int]probePreviewResult, len(ports))
	var mu sync.Mutex
	sem := make(chan struct{}, previewProbeConcurrency)
	var wg sync.WaitGroup
	for _, p := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			title, up, isHTML := probePreviewService(p)
			mu.Lock()
			out[p] = probePreviewResult{title: title, up: up, isHTML: isHTML}
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return out
}

// gatherPreviewServices builds the grid list. Published ports are probed once
// each and kept whether or not they answer right now: the tile reports
// reachability itself (a service being restarted is a normal state a vanishing
// tile would hide). Unpublished ports — ones the page only named — are listed
// without being probed: the probe reads a port's page and title, and this
// endpoint must not become a way to fingerprint whatever else listens on this
// machine's loopback.
func gatherPreviewServices(published, unpublished []int, origin string) []previewService {
	probes := probePreviewPorts(published)

	out := make([]previewService, 0, len(published)+len(unpublished))
	for _, p := range published {
		pr := probes[p]
		out = append(out, previewService{Port: p, Title: pr.title, Target: previewBaseURL(p).String(), URL: agent.PreviewURL(origin, p), Up: pr.up, HTML: pr.isHTML, Published: true})
	}
	for _, p := range unpublished {
		out = append(out, previewService{Port: p, Target: previewBaseURL(p).String(), URL: agent.PreviewURL(origin, p)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// handlePreviewServices answers GET /api/preview/services with the preview
// grid's services for one project: the ports published for it — announced by
// its agent in a live-preview URL, or added by the user — plus any port the page
// names through ?ports= (a deep link), which is listed as unpublished unless the
// server serves it already. The server's own port is never listed either way. A
// ?directory= names the project when the caller is looking at one other than
// the server's own; the probe per published port is why the page polls this
// rather than the server pushing.
func (s *Server) handlePreviewServices(w http.ResponseWriter, req *http.Request) {
	dir := req.URL.Query().Get("directory")
	if dir == "" {
		dir = s.dir
	}
	self := s.Port()
	published := dropSelf(previewPortsForProject(s.store, dir), self)
	listed := make(map[int]bool, len(published))
	for _, p := range published {
		listed[p] = true
	}
	var unpublished []int
	for _, p := range dropSelf(parseRequestedPorts(req.URL.Query().Get("ports")), self) {
		if listed[p] {
			continue
		}
		// Published for another directory on this server: the proxy serves it,
		// so the tile must not claim otherwise.
		if ok, err := s.store.PreviewPortPublished(p); err == nil && ok {
			published = append(published, p)
			continue
		}
		unpublished = append(unpublished, p)
	}
	services := gatherPreviewServices(published, unpublished, requestOrigin(req))
	if services == nil {
		services = []previewService{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": services})
}

// previewWriteAllowed guards the requests that change what the preview proxy
// serves. Publishing a port exposes it at a hostname that may be reachable by
// people who cannot use this UI, so the request must come from this UI, not
// from some other page riding the user's browser: its body must be JSON (which
// a cross-origin page cannot send without passing a CORS preflight), and a
// browser that says where the request came from (Sec-Fetch-Site) must name this
// origin itself. A non-browser client sends no Sec-Fetch-Site and is let
// through on the JSON check alone.
func previewWriteAllowed(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

// handlePublishPreviewPort answers POST /api/preview/ports
// ({"port": N, "directory"?: D}) by publishing a port the user added on the
// Preview page. It is recorded exactly like one the agent announced, so its tile
// is listed and its preview hostname is served. The server's own port cannot
// be published.
func (s *Server) handlePublishPreviewPort(w http.ResponseWriter, req *http.Request) {
	if !previewWriteAllowed(req) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "preview ports can only be changed from this server's own UI"})
		return
	}
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no project store"})
		return
	}
	var body struct {
		Port      int    `json:"port"`
		Directory string `json:"directory"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected {\"port\": <1-65535>}"})
		return
	}
	if body.Port < 1 || body.Port > 65535 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "port must be between 1 and 65535"})
		return
	}
	if body.Port == s.Port() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "port " + strconv.Itoa(body.Port) + " is this ogcode server itself"})
		return
	}
	dir := body.Directory
	if dir == "" {
		dir = s.dir
	}
	if err := s.store.RecordAnnouncedPort(dir, body.Port); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"port": body.Port, "url": agent.PreviewURL(requestOrigin(req), body.Port)})
}

// handleUnpublishPreviewPort answers DELETE /api/preview/ports/{port}: the
// port is forgotten for every directory, which takes its tile off the grid and
// stops the proxy serving its hostname at once. The agent announcing it again
// publishes it again.
func (s *Server) handleUnpublishPreviewPort(w http.ResponseWriter, req *http.Request) {
	if !previewWriteAllowed(req) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "preview ports can only be changed from this server's own UI"})
		return
	}
	port, ok := agent.ParsePreviewPort(chi.URLParam(req, "port"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected /api/preview/ports/<1-65535>"})
		return
	}
	if err := s.store.ForgetAnnouncedPort(port); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
