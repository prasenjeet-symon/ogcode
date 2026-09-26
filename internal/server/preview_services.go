package server

import (
	"context"
	"html"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// previewProbeTimeout bounds one reachability probe. Discovery dials every
// listening port on the machine, so an unresponsive peer — a raw TCP service
// that accepts a connection and then says nothing — must not hold the endpoint
// open. It is a ceiling, not a cost: a service that answers does so at once.
const previewProbeTimeout = 2 * time.Second

// previewProbeConcurrency caps how many ports are dialed at once. A busy
// machine has dozens of listeners, and the preview page polls this on a timer,
// so the herd is kept small enough not to look like a port scan.
const previewProbeConcurrency = 12

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
	// Up is true when the service answered, so the grid embeds it rather than
	// greying the tile.
	Up bool `json:"up"`
	// Source is "auto" for a discovered listener, "manual" for a port the page
	// asked for (the user added it, or a /preview/<port>/ link named it). A
	// manual port is shown whether or not it answers; an auto one is only shown
	// when it serves an HTML page.
	Source string `json:"source"`
}

// previewListeners lists the loopback-reachable ports something is listening
// on, for the preview grid to probe. Discovery is best-effort: when lsof is
// absent (Windows) or fails, the list is empty and the grid falls back to the
// ports the page names explicitly — the manual add still works.
func previewListeners() []int {
	out, err := previewListenersOutput()
	if err != nil {
		return nil
	}
	return parsePreviewListeners(out)
}

// previewListenersOutput shells out to lsof. Kept separate from
// previewListeners so tests can drive the parse without a real process
// boundary (the same split as adbDevicesOutput).
func previewListenersOutput() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// -F makes lsof emit parseable field output: one "n<address>" line per open
	// socket. -nP keeps it from resolving names or service names, which is both
	// faster and gives the port as a number.
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
	return string(out), err
}

// parsePreviewListeners reads `lsof -Fn` output and returns the ports reachable
// at 127.0.0.1, sorted and deduped. A listener on 127.0.0.1, 0.0.0.0 or * is
// reachable there; one bound to a LAN address or to IPv6 loopback is not — the
// proxy dials the literal 127.0.0.1, so showing those would offer a tile that
// cannot load.
func parsePreviewListeners(out string) []int {
	seen := map[int]bool{}
	var ports []int
	for _, line := range strings.Split(out, "\n") {
		addr, ok := strings.CutPrefix(line, "n")
		if !ok {
			continue
		}
		host, portStr, ok := splitListenAddress(addr)
		if !ok || !loopbackReachable(host) {
			continue
		}
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 || seen[p] {
			continue
		}
		seen[p] = true
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// splitListenAddress splits an lsof address into its host and port. The port is
// after the last colon, so a bracketed IPv6 form ("[::1]:3000") still splits.
func splitListenAddress(addr string) (host, port string, ok bool) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", "", false
	}
	return addr[:i], addr[i+1:], true
}

// loopbackReachable reports whether a listener bound to host answers at
// 127.0.0.1. "*" is lsof's all-interfaces form and includes loopback.
func loopbackReachable(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "*", "0.0.0.0":
		return true
	}
	return false
}

// previewCandidatesFrom drops the server's own port and every port already
// requested from the discovered list, so a port is auto-detected or explicitly
// requested — never both.
func previewCandidatesFrom(discovered, requested []int, self int) []int {
	inRequest := make(map[int]bool, len(requested))
	for _, p := range requested {
		inRequest[p] = true
	}
	var out []int
	for _, p := range discovered {
		if p == self || inRequest[p] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// parseRequestedPorts reads the ?ports= list: comma-separated loopback ports
// the page wants listed whether or not they are discovered. A malformed entry
// is skipped rather than failing the request — one bad port must not blank the
// whole grid.
func parseRequestedPorts(raw string) []int {
	var out []int
	seen := map[int]bool{}
	for _, seg := range strings.Split(raw, ",") {
		p, err := strconv.Atoi(strings.TrimSpace(seg))
		if err != nil || p < 1 || p > 65535 || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, false
	}
	defer resp.Body.Close()
	// A 5xx is the service saying it is broken, not up — mirroring
	// previewReachable, so the tile and the expanded view agree.
	if resp.StatusCode >= 500 {
		return "", false, false
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
// previewProbeConcurrency. Concurrency is the point: serially, a machine with
// dozens of listeners would add a timeout per dead port to every poll.
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

// gatherPreviewServices probes candidates and requested ports once and builds
// the grid list. A candidate is kept only when it answers with HTML — that is
// what makes it an app a browser can show — while a requested port is always
// kept, so a manual entry or a deep link that is down still shows its tile
// rather than silently vanishing.
func gatherPreviewServices(candidates, requested []int) []previewService {
	all := make([]int, 0, len(candidates)+len(requested))
	all = append(all, candidates...)
	all = append(all, requested...)
	probes := probePreviewPorts(all)

	out := make([]previewService, 0, len(all))
	for _, p := range candidates {
		pr := probes[p]
		if !pr.up || !pr.isHTML {
			continue
		}
		out = append(out, previewService{Port: p, Title: pr.title, Target: previewBaseURL(p).String(), Up: true, Source: "auto"})
	}
	for _, p := range requested {
		pr := probes[p]
		out = append(out, previewService{Port: p, Title: pr.title, Target: previewBaseURL(p).String(), Up: pr.up, Source: "manual"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// handlePreviewServices answers GET /api/preview/services with the loopback
// services the preview grid shows: the apps it discovers on this machine, plus
// any port the page names through ?ports= (a port the user added, or one a
// /preview/<port>/ link points at). Discovery is a shell-out plus a probe per
// listener, so the page polls it rather than the server pushing.
func (s *Server) handlePreviewServices(w http.ResponseWriter, req *http.Request) {
	requested := parseRequestedPorts(req.URL.Query().Get("ports"))
	candidates := previewCandidatesFrom(previewListeners(), requested, s.Port())
	services := gatherPreviewServices(candidates, requested)
	if services == nil {
		services = []previewService{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": services})
}
