package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/id"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// TestParseRequestedPorts covers the ?ports= reader: comma-separated, trimmed,
// deduped, with a bad entry dropped rather than failing the request — including
// the non-canonical spellings Atoi would accept — and the list capped.
func TestParseRequestedPorts(t *testing.T) {
	got := parseRequestedPorts("3000, 8080,3000,xyz,0,70000,,+4000,05000")
	want := []int{3000, 8080}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	var many []string
	for p := 1000; p < 1000+3*previewMaxRequestedPorts; p++ {
		many = append(many, strconv.Itoa(p))
	}
	if got := parseRequestedPorts(strings.Join(many, ",")); len(got) != previewMaxRequestedPorts {
		t.Fatalf("got %d ports, want the cap of %d", len(got), previewMaxRequestedPorts)
	}
}

// TestDropSelf pins that the server's own port never reaches the grid.
func TestDropSelf(t *testing.T) {
	got := dropSelf([]int{3000, 9699, 5173}, 9699)
	if len(got) != 2 || got[0] != 3000 || got[1] != 5173 {
		t.Fatalf("got %v, want [3000 5173]", got)
	}
}

// TestCleanTitle pins the <title> label: entities decoded, whitespace
// collapsed, and a long title clipped with an ellipsis.
func TestCleanTitle(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"  My   App  ", "My App"},
		{"Tom &amp; Jerry", "Tom & Jerry"},
		{"line\n  two", "line two"},
	}
	for _, c := range cases {
		if got := cleanTitle(c.in); got != c.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("x", 200)
	got := cleanTitle(long)
	if r := []rune(got); len(r) > 81 || !strings.HasSuffix(got, "…") {
		t.Errorf("cleanTitle(long) = %q (len %d), want it clipped to ~80 runes + ellipsis", got, len(r))
	}
}

// TestProbePreviewService covers the probe against a real backend: an HTML page
// is up+HTML with its title read, and a plain-text answer is up but not HTML
// (the page can then refuse to embed it in an iframe).
func TestProbePreviewService(t *testing.T) {
	htmlBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><head><title>the player</title></head><body>hi</body></html>")
	}))
	defer htmlBackend.Close()

	t.Run("html page is up and titled", func(t *testing.T) {
		title, up, isHTML := probePreviewService(atoi(t, previewPort(t, htmlBackend.URL)))
		if !up || !isHTML {
			t.Fatalf("up=%v isHTML=%v, want both true", up, isHTML)
		}
		if title != "the player" {
			t.Fatalf("title = %q, want %q", title, "the player")
		}
	})

	t.Run("plain text is up but not html", func(t *testing.T) {
		txt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "Ollama is running")
		}))
		defer txt.Close()
		title, up, isHTML := probePreviewService(atoi(t, previewPort(t, txt.URL)))
		if !up || isHTML {
			t.Fatalf("up=%v isHTML=%v, want up true and isHTML false", up, isHTML)
		}
		if title != "" {
			t.Fatalf("title = %q, want empty for a non-HTML answer", title)
		}
	})

	t.Run("500 is not up", func(t *testing.T) {
		broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer broken.Close()
		if _, up, _ := probePreviewService(atoi(t, previewPort(t, broken.URL))); up {
			t.Fatal("up = true, want false for a 5xx")
		}
	})

	t.Run("a redirect off the service's origin is not followed", func(t *testing.T) {
		// A 30x to another origin is the port's answer, not a destination:
		// chasing its Location would send the probe wherever the service names —
		// off this machine. The browser follows redirects itself once a tile is
		// opened.
		var landed atomic.Bool
		hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			landed.Store(true)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<title>elsewhere</title>")
		}))
		defer hop.Close()
		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", hop.URL+"/landed")
			w.WriteHeader(http.StatusFound)
		}))
		defer redirector.Close()

		title, up, isHTML := probePreviewService(atoi(t, previewPort(t, redirector.URL)))
		if landed.Load() {
			t.Fatal("probe followed the redirect to the Location host")
		}
		if !up || isHTML || title != "" {
			t.Fatalf("title=%q up=%v isHTML=%v, want the 30x itself read as up, not-HTML, untitled", title, up, isHTML)
		}
	})

	t.Run("a same-origin redirect is followed to the page", func(t *testing.T) {
		// An app whose root redirects to /en/ (Astro i18n) or /login is still an
		// app: the tile must embed it, titled from the page it lands on — both
		// for a relative Location and for one naming a loopback host on the
		// same port (built from the Host header the probe sent).
		var port int
		app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/":
				http.Redirect(w, r, "/en/", http.StatusFound)
			case "/en/":
				http.Redirect(w, r, "http://localhost:"+strconv.Itoa(port)+"/en/home", http.StatusFound)
			default:
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = io.WriteString(w, "<title>localized home</title>")
			}
		}))
		defer app.Close()
		port = atoi(t, previewPort(t, app.URL))

		title, up, isHTML := probePreviewService(port)
		if !up || !isHTML || title != "localized home" {
			t.Fatalf("title=%q up=%v isHTML=%v, want the page behind the redirects", title, up, isHTML)
		}
	})

	t.Run("a redirect loop stops and is up but not embeddable", func(t *testing.T) {
		loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/again", http.StatusFound)
		}))
		defer loop.Close()
		title, up, isHTML := probePreviewService(atoi(t, previewPort(t, loop.URL)))
		if !up || isHTML || title != "" {
			t.Fatalf("title=%q up=%v isHTML=%v, want up, not embeddable, untitled", title, up, isHTML)
		}
	})

	t.Run("dead port is not up", func(t *testing.T) {
		dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		port := atoi(t, previewPort(t, dead.URL))
		dead.Close()
		if _, up, _ := probePreviewService(port); up {
			t.Fatal("up = true, want false for a closed port")
		}
	})
}

// seedAnnouncedPort records a port the agent announced in dir — the durable
// half of an announcement, written when the agent handed the user a live-preview
// URL.
func seedAnnouncedPort(t *testing.T, srv *Server, dir string, port string) {
	t.Helper()
	if srv.store == nil {
		srv.store = session.NewStore(srv.db)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("bad fixture port %q: %v", port, err)
	}
	if err := srv.store.RecordAnnouncedPort(dir, n); err != nil {
		t.Fatalf("record port: %v", err)
	}
}

// TestPreviewPortsForProject pins the record as the only source: a port the
// agent announced is returned, another project's record never leaks in, and a
// port that merely appears in the transcript — a tool result, the reasoning
// trace, or prose the agent wrote while debugging — is not an announcement and
// is not shown.
func TestPreviewPortsForProject(t *testing.T) {
	t.Run("nil store and empty dir yield nothing", func(t *testing.T) {
		if got := previewPortsForProject(nil, "/proj"); got != nil {
			t.Fatalf("nil store = %v, want nil", got)
		}
		srv := newTestServer(t)
		srv.store = session.NewStore(srv.db)
		if got := previewPortsForProject(srv.store, ""); got != nil {
			t.Fatalf("empty dir = %v, want nil", got)
		}
	})

	t.Run("a recorded announcement is returned", func(t *testing.T) {
		srv := newTestServer(t)
		srv.store = session.NewStore(srv.db)
		seedAnnouncedPort(t, srv, srv.dir, "4321")
		// A different project's record must not leak in.
		seedAnnouncedPort(t, srv, t.TempDir(), "6000")

		got := previewPortsForProject(srv.store, srv.dir)
		if len(got) != 1 || got[0] != 4321 {
			t.Fatalf("got %v, want [4321] from the announcement record", got)
		}
	})

	t.Run("the transcript is never mined", func(t *testing.T) {
		srv := newTestServer(t)
		srv.store = session.NewStore(srv.db)
		sess := &session.Session{ID: id.NewSessionID(), Directory: srv.dir, ProjectID: srv.dir, SessionType: "build"}
		if err := srv.store.Create(sess); err != nil {
			t.Fatalf("create session: %v", err)
		}
		msg := &session.MessageInfo{ID: id.NewMessageID(), SessionID: sess.ID, Role: session.RoleAssistant}
		if err := srv.store.CreateMessage(msg); err != nil {
			t.Fatalf("create message: %v", err)
		}

		// Assistant prose naming a live-preview URL — the agent discussing this
		// very bug: an announcement, but never recorded, so it is not listed.
		text, _ := json.Marshal(session.TextPartData{
			Text: "the grid showed http://1143." + agent.PreviewDomain() + "/ and 127.0.0.1:3456",
		})
		part := &session.Part{ID: id.NewPartID(), MessageID: msg.ID, SessionID: sess.ID, Type: session.PartText, Data: text}
		if err := srv.store.CreatePart(part); err != nil {
			t.Fatalf("create text part: %v", err)
		}

		// The reasoning trace echoes the prompt's own example URL and every port
		// it probed while working — the noise that filled the grid — so it must
		// never be mined either.
		think, _ := json.Marshal(session.ReasoningPartData{
			Text: "the example is http://3000." + agent.PreviewDomain() + "/ and 65535 is in range",
		})
		thinking := &session.Part{ID: id.NewPartID(), MessageID: msg.ID, SessionID: sess.ID, Type: session.PartReasoning, Data: think}
		if err := srv.store.CreatePart(thinking); err != nil {
			t.Fatalf("create reasoning part: %v", err)
		}

		// A tool result that prints a port, which must never be mined either.
		out := "listening on http://127.0.0.1:4321/"
		data, _ := json.Marshal(session.ToolPartData{
			Tool:   "bash",
			CallID: "call-1",
			State:  session.ToolState{Status: "completed", Input: json.RawMessage(`{"command":"npm run dev"}`), Output: &out},
		})
		toolPart := &session.Part{ID: id.NewPartID(), MessageID: msg.ID, SessionID: sess.ID, Type: session.PartTool, Data: data}
		if err := srv.store.CreatePart(toolPart); err != nil {
			t.Fatalf("create tool part: %v", err)
		}

		if got := previewPortsForProject(srv.store, srv.dir); got != nil {
			t.Fatalf("got %v, want nil: only a recorded announcement counts", got)
		}
	})
}

// TestGatherPreviewServices pins the grid rule: every published port is listed
// whether or not it answers and whatever it answers with (the tile reports
// reachability and embeds only HTML), while an unpublished port the page named
// is listed too — so a deep link shows a tile the user can publish — but is
// never probed: the endpoint must not fingerprint the rest of loopback.
func TestGatherPreviewServices(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<title>dashboard</title>")
	}))
	defer app.Close()
	appPort := atoi(t, previewPort(t, app.URL))

	// A published listener that answers plain text — an Ollama-style port that
	// still deserves a tile (it was published), just not an embedded iframe.
	notAnApp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "not html")
	}))
	defer notAnApp.Close()
	notAnAppPort := atoi(t, previewPort(t, notAnApp.URL))

	// A published port that is down must still be listed.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadPort := atoi(t, previewPort(t, dead.URL))
	dead.Close()

	// An unpublished port the page named, with a live service behind it that
	// must never be dialed.
	var dialed atomic.Bool
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dialed.Store(true)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<title>private</title>")
	}))
	defer private.Close()
	privatePort := atoi(t, previewPort(t, private.URL))

	const origin = "http://127.0.0.1:7799"
	services := gatherPreviewServices([]int{appPort, notAnAppPort, deadPort}, []int{privatePort}, origin)

	byPort := map[int]previewService{}
	for _, s := range services {
		byPort[s.Port] = s
	}

	if got, ok := byPort[appPort]; !ok || !got.Up || !got.HTML || !got.Published || got.Title != "dashboard" {
		t.Fatalf("app port = %+v (present %v), want published/up/html/dashboard", got, ok)
	}
	// Every tile links to the service's own hostname, carrying the origin's
	// scheme and port, so the grid can open and embed it directly.
	if got := byPort[appPort].URL; got != "http://"+strconv.Itoa(appPort)+"."+agent.PreviewDomain()+":7799/" {
		t.Fatalf("app port URL = %q, want the preview hostname", got)
	}
	if got, ok := byPort[notAnAppPort]; !ok || !got.Up || got.HTML || !got.Published {
		t.Fatalf("plain-text published port = %+v (present %v), want published/up/not-html", got, ok)
	}
	if got, ok := byPort[deadPort]; !ok || got.Up || !got.Published {
		t.Fatalf("down published port = %+v (present %v), want published/down", got, ok)
	}
	if got, ok := byPort[privatePort]; !ok || got.Published || got.Up || got.Title != "" {
		t.Fatalf("unpublished port = %+v (present %v), want listed, unpublished, unprobed", got, ok)
	}
	if dialed.Load() {
		t.Fatal("an unpublished port was probed")
	}
	// Sorted by port.
	for i := 1; i < len(services); i++ {
		if services[i-1].Port > services[i].Port {
			t.Fatalf("services not sorted by port: %+v", services)
		}
	}
}

type previewServicesBody struct {
	Services []struct {
		Port      int    `json:"port"`
		Title     string `json:"title"`
		Up        bool   `json:"up"`
		Published bool   `json:"published"`
	} `json:"services"`
}

func getPreviewServices(t *testing.T, h http.Handler, query string) previewServicesBody {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/preview/services"+query, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %q", rec.Code, rec.Body.String())
	}
	var body previewServicesBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body
}

// serviceFor returns one listed port's published flag, title and reachability,
// and whether it was listed at all.
func serviceFor(t *testing.T, body previewServicesBody, port int) (published bool, title string, up bool, ok bool) {
	t.Helper()
	for _, s := range body.Services {
		if s.Port == port {
			return s.Published, s.Title, s.Up, true
		}
	}
	return false, "", false, false
}

// TestServe_PreviewServices covers GET /api/preview/services: the published
// ports come from the named project's record (never a machine-wide scan), a
// port the page names is listed unpublished, the server's own port is never
// listed, and a ?directory= scopes the answer to that project.
func TestServe_PreviewServices(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)

	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<title>my app</title>")
	}))
	defer app.Close()
	appPort := atoi(t, previewPort(t, app.URL))
	seedAnnouncedPort(t, srv, srv.dir, strconv.Itoa(appPort))

	// A port the page asks for that is not published: listed, so the user can
	// publish it, but not probed.
	const requested = 4998

	h := srv.routes()

	body := getPreviewServices(t, h, "?ports="+strconv.Itoa(requested))
	if published, title, up, ok := serviceFor(t, body, appPort); !ok || !published || title != "my app" || !up {
		t.Fatalf("announced app = published:%v title:%q up:%v (present %v), want published/my app/up", published, title, up, ok)
	}
	if published, _, up, ok := serviceFor(t, body, requested); !ok || published || up {
		t.Fatalf("requested port = published:%v up:%v (present %v), want listed unpublished", published, up, ok)
	}

	// Naming a published port in ?ports= does not demote it.
	body = getPreviewServices(t, h, "?ports="+strconv.Itoa(appPort))
	if published, _, _, ok := serviceFor(t, body, appPort); !ok || !published {
		t.Fatalf("published port named by the page = published:%v (present %v), want published", published, ok)
	}

	// The server's own port is never listed, even when asked for by hand: a
	// tile mirroring the ogcode UI inside the grid is a self-reference.
	selfBody := getPreviewServices(t, h, "?ports="+itoa(srv.Port()))
	if _, _, _, ok := serviceFor(t, selfBody, srv.Port()); ok {
		t.Fatalf("server's own port listed: %+v", selfBody.Services)
	}

	// A different project's directory scopes the answer: the announced app is
	// not in its grid, the explicitly requested port stays — and a port that
	// IS published on this server (for the other directory) is listed as such,
	// because the proxy serves it.
	otherDir := t.TempDir()
	other := getPreviewServices(t, h, "?directory="+otherDir+"&ports="+strconv.Itoa(requested))
	if _, _, _, ok := serviceFor(t, other, appPort); ok {
		t.Fatalf("another project's directory still listed the announced app: %+v", other.Services)
	}
	if _, _, _, ok := serviceFor(t, other, requested); !ok {
		t.Fatalf("requested port missing for another directory: %+v", other.Services)
	}
	cross := getPreviewServices(t, h, "?directory="+otherDir+"&ports="+strconv.Itoa(appPort))
	if published, _, _, ok := serviceFor(t, cross, appPort); !ok || !published {
		t.Fatalf("port published elsewhere on this server = published:%v (present %v), want published", published, ok)
	}

	// With nothing published and nothing requested the grid is an empty list:
	// the record is the only source.
	empty := getPreviewServices(t, h, "?directory="+t.TempDir())
	if len(empty.Services) != 0 {
		t.Fatalf("empty project = %+v, want no services", empty.Services)
	}
}

// TestServe_PreviewPortsEndpoints covers publishing and un-publishing a port
// from the Preview page: a publish records the port (the grid lists it
// published), the server's own port and nonsense are refused, a request that is
// not this UI's own — not JSON, or a browser naming another site — is refused
// before anything is recorded, and an un-publish takes the port off again.
func TestServe_PreviewPortsEndpoints(t *testing.T) {
	srv := newTestServer(t)
	srv.store = session.NewStore(srv.db)
	h := srv.routes()

	do := func(method, path, contentType, fetchSite, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if fetchSite != "" {
			req.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	published := func(port int) bool {
		t.Helper()
		ok, err := srv.store.PreviewPortPublished(port)
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		return ok
	}

	// Refused before anything is recorded.
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"form-encoded (a cross-site form can send it)":  do(http.MethodPost, "/api/preview/ports", "application/x-www-form-urlencoded", "", `{"port":4321}`),
		"text/plain (a no-preflight fetch can send it)": do(http.MethodPost, "/api/preview/ports", "text/plain", "", `{"port":4321}`),
		"another site's page":                           do(http.MethodPost, "/api/preview/ports", "application/json", "cross-site", `{"port":4321}`),
		"a sibling subdomain's page":                    do(http.MethodPost, "/api/preview/ports", "application/json", "same-site", `{"port":4321}`),
	} {
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, rec.Code)
		}
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"out of range": do(http.MethodPost, "/api/preview/ports", "application/json", "same-origin", `{"port":70000}`),
		"not JSON":     do(http.MethodPost, "/api/preview/ports", "application/json", "same-origin", `port=4321`),
		"self":         do(http.MethodPost, "/api/preview/ports", "application/json", "same-origin", `{"port":`+itoa(srv.Port())+`}`),
	} {
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
	if published(4321) {
		t.Fatal("a refused request recorded the port")
	}

	// This UI's own request publishes it, into this project's grid.
	rec := do(http.MethodPost, "/api/preview/ports", "application/json; charset=utf-8", "same-origin", `{"port":4321}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish: status %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "4321."+agent.PreviewDomain()) {
		t.Fatalf("publish answer %q, want the preview URL", rec.Body.String())
	}
	if !published(4321) {
		t.Fatal("publish did not record the port")
	}
	if got := getPreviewServices(t, h, ""); len(got.Services) != 1 || got.Services[0].Port != 4321 || !got.Services[0].Published {
		t.Fatalf("grid after publish = %+v, want 4321 published", got.Services)
	}

	// Un-publishing: a bad port is refused, a good one is forgotten.
	if rec := do(http.MethodDelete, "/api/preview/ports/03000", "application/json", "same-origin", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("unpublish bad port: status %d, want 400", rec.Code)
	}
	if rec := do(http.MethodDelete, "/api/preview/ports/4321", "application/json", "cross-site", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site unpublish: status %d, want 403", rec.Code)
	}
	if rec := do(http.MethodDelete, "/api/preview/ports/4321", "application/json", "same-origin", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("unpublish: status %d, want 204", rec.Code)
	}
	if published(4321) {
		t.Fatal("unpublish left the port published")
	}
}
