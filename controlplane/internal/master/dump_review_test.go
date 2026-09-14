package master_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	cpv1 "github.com/prasenjeet-symon/ogcode-control-plane/gen/controlplane/v1"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/auth"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/bus"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/master"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/pairing"
	"github.com/prasenjeet-symon/ogcode-control-plane/internal/registry"
)

// TestServeSeededConsole runs the REAL console server, seeded with sample data
// (workers, repositories, and a multi-repo user), on SERVE_ADDR so the new UI
// can be browsed live without a running worker fleet. It is skipped unless
// SERVE_ADDR is set, blocks while serving, and never runs in CI:
//
//	SERVE_ADDR=127.0.0.1:18899 go test ./internal/master/ -run TestServeSeededConsole -timeout 0
func TestServeSeededConsole(t *testing.T) {
	addr := os.Getenv("SERVE_ADDR")
	if addr == "" {
		t.Skip("set SERVE_ADDR to serve the seeded console for review")
	}
	store, err := registry.Open(filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	mk := func(name, email string, admin bool, repos []string) {
		hash, _ := bcrypt.GenerateFromPassword([]byte("demo-"+name), bcrypt.MinCost)
		rec := registry.UserRecord{Hash: string(hash), CreatedAt: time.Now(), Admin: &admin, Email: email}
		if len(repos) > 0 {
			rec.Repos = repos
			rec.Workspaces = []string{master.UserWorktreeSlug(name)}
		}
		if err := store.PutUser(name, rec); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	mk("root", "root@acme.dev", true, nil)
	mk("alice", "alice@acme.dev", false, []string{"https://github.com/acme/webapp", "https://github.com/acme/api-service"})
	mk("bob", "bob@acme.dev", false, []string{"https://github.com/acme/webapp"})

	reg := registry.NewWithStore(store, discardLogger())
	gate, err := auth.NewGate(store, "", time.Hour, "", false)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	srv := master.New(master.Options{
		Registry: reg,
		Auth:     pairing.New("demo-secret", time.Minute),
		Bus:      bus.New(256),
		Logger:   discardLogger(),
		Gate:     gate, // sign in as root / demo-root
	})
	srv.SeedRepoPlacement("https://github.com/acme/webapp", "worker-laptop")
	srv.SeedRepoPlacement("https://github.com/acme/api-service", "worker-laptop")
	srv.SeedRepoPlacement("https://github.com/acme/infra", "worker-ci-01")

	now := time.Now()
	reg.Add("worker-laptop", "laptop", []string{"chrome", "git", "docker"},
		[]registry.Workspace{{Path: "/srv/acme/webapp", Name: "webapp", Branch: "main", Present: true}},
		pairing.Token{Value: "tok1", ExpiresAt: now.Add(time.Hour)}, now)
	_ = reg.Attach("worker-laptop", stubConn{}, now)
	reg.Add("worker-ci-01", "ci-runner-01", []string{"git"}, nil,
		pairing.Token{Value: "tok2", ExpiresAt: now.Add(time.Hour)}, now.Add(-45*time.Minute))

	// A couple of active sessions so the Sessions list has content.
	reg.RouteSession("ses_7f3a9c2e", "worker-laptop")
	srv.RecordSessionUser("ses_7f3a9c2e", "alice")
	reg.RouteSession("ses_b18d4402", "worker-laptop")
	srv.RecordSessionUser("ses_b18d4402", "bob")

	mux := http.NewServeMux()
	path, handler := srv.Handler()
	mux.Handle(path, handler)
	t.Logf("seeded console serving on http://%s/", addr)
	if err := http.ListenAndServe(addr, srv.UIProxyHandler(mux)); err != nil {
		t.Fatalf("serve: %v", err)
	}
}

// readAllString drains a response body to a string for dumping.
func readAllString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// stubConn is a no-op worker connection so a seeded worker reads as Online
// without a live stream (Attach only needs something that satisfies Send).
type stubConn struct{}

func (stubConn) Send(*cpv1.MasterToWorker) error { return nil }

// TestDumpConsoleForReview renders each console page to an HTML file for manual
// visual review. It is skipped unless DUMP_DIR is set, so it never runs in CI:
//
//	DUMP_DIR=/tmp/console go test ./internal/master/ -run TestDumpConsoleForReview
func TestDumpConsoleForReview(t *testing.T) {
	dir := os.Getenv("DUMP_DIR")
	if dir == "" {
		t.Skip("set DUMP_DIR to render the console pages for review")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	srv, hs, store, adminCookie, _, _ := scopedHarness(t)

	// A richer Users page: give alice a contact email and two assigned repos.
	if rec, ok, _ := store.GetUser("alice"); ok {
		rec.Email = "alice@example.com"
		rec.Repo = ""
		rec.Repos = []string{"https://github.com/acme/webapp", "https://github.com/acme/api-service"}
		rec.Workspaces = []string{"alice"}
		_ = store.PutUser("alice", rec)
	}
	if rec, ok, _ := store.GetUser("bob"); ok {
		rec.Email = "bob@example.com"
		rec.Repo = ""
		rec.Repos = []string{"https://github.com/acme/webapp"}
		rec.Workspaces = []string{"bob"}
		_ = store.PutUser("bob", rec)
	}

	// Known repositories for the assign form + the Repositories page.
	srv.SeedRepoPlacement("https://github.com/acme/webapp", "worker-laptop")
	srv.SeedRepoPlacement("https://github.com/acme/api-service", "worker-laptop")
	srv.SeedRepoPlacement("https://github.com/acme/infra", "worker-ci-01")

	// Two workers: one Online (attached via a stub conn), one Offline.
	now := time.Now()
	tok := pairing.Token{Value: "tok", ExpiresAt: now.Add(time.Hour)}
	srv.Registry().Add("worker-laptop", "laptop", []string{"chrome", "git", "docker"},
		[]registry.Workspace{{Path: "/srv/acme/webapp", Name: "webapp", Branch: "main", Present: true}}, tok, now)
	_ = srv.Registry().Attach("worker-laptop", stubConn{}, now)
	srv.Registry().Add("worker-ci-01", "ci-runner-01", []string{"git"}, nil,
		pairing.Token{Value: "tok2", ExpiresAt: now.Add(time.Hour)}, now.Add(-45*time.Minute))

	pages := []struct{ name, path string }{
		{"dashboard", "/"},
		{"users", "/__operator/users"},
		{"repositories", "/__operator/repos"},
		{"sessions", "/__operator/sessions"},
	}
	for _, p := range pages {
		req, _ := http.NewRequest(http.MethodGet, hs.URL+p.path, nil)
		for _, c := range adminCookie {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", p.path, err)
		}
		body := readAllString(t, resp)
		out := filepath.Join(dir, p.name+".html")
		if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", out, err)
		}
		t.Logf("wrote %s (%d bytes)", out, len(body))
	}

	// The login page (unauthenticated GET of the apex).
	resp, err := http.Get(hs.URL + "/")
	if err != nil {
		t.Fatalf("GET login: %v", err)
	}
	login := readAllString(t, resp)
	_ = os.WriteFile(filepath.Join(dir, "login.html"), []byte(login), 0o644)
	t.Logf("wrote %s", filepath.Join(dir, "login.html"))
}
