package skill

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// remoteServer serves an index.json at /skills/index.json plus the files it
// names, and counts every request so a test can prove the cache was used.
func remoteServer(t *testing.T, index string, files map[string]string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/skills/index.json", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(index))
	})
	for path, body := range files {
		mux.HandleFunc("/skills/"+path, func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Write([]byte(body))
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestFetchRemote_DownloadsAndCachesByVersion(t *testing.T) {
	index := `{"version":"1.0.0","skills":[{"name":"git-release","files":["SKILL.md","scripts/run.sh"]}]}`
	srv, hits := remoteServer(t, index, map[string]string{
		"git-release/SKILL.md":       "---\nname: git-release\ndescription: from the network\n---\nbody\n",
		"git-release/scripts/run.sh": "#!/bin/sh\n",
	})
	cache := t.TempDir()
	url := srv.URL + "/skills/index.json"

	dirs, err := fetchRemote(context.Background(), srv.Client(), cache, url)
	if err != nil {
		t.Fatalf("fetchRemote: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("got %d skill dirs, want 1", len(dirs))
	}
	s, err := loadFrom(dirs[0], SourceRemote)
	if err != nil {
		t.Fatalf("downloaded skill does not parse: %v", err)
	}
	if s.Name != "git-release" || s.Description != "from the network" {
		t.Errorf("got %+v", s)
	}
	if _, err := os.Stat(filepath.Join(dirs[0], "scripts", "run.sh")); err != nil {
		t.Errorf("shipped file was not downloaded: %v", err)
	}

	// A version already in the cache is reused: only the manifest is re-fetched,
	// never the files behind it.
	after := hits.Load()
	if _, err := fetchRemote(context.Background(), srv.Client(), cache, url); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if got := hits.Load() - after; got != 1 {
		t.Errorf("second fetch made %d requests, want 1 (the manifest only)", got)
	}

	// No staging directory is left behind once a version is published.
	root := filepath.Join(cache, urlKey(url))
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging-") {
			t.Errorf("staging directory %q was left behind", e.Name())
		}
	}
}

// A skills URL that cannot be reached should cost the agent the newest skills,
// not every skill it had yesterday.
func TestFetchRemote_FallsBackToTheCachedCopy(t *testing.T) {
	index := `{"version":"1.0.0","skills":[{"name":"cached","files":["SKILL.md"]}]}`
	srv, _ := remoteServer(t, index, map[string]string{
		"cached/SKILL.md": "---\nname: cached\ndescription: still here\n---\nbody\n",
	})
	cache := t.TempDir()
	url := srv.URL + "/skills/index.json"

	if _, err := fetchRemote(context.Background(), srv.Client(), cache, url); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	srv.Close() // the network is now gone

	dirs, err := fetchRemote(context.Background(), srv.Client(), cache, url)
	if err == nil {
		t.Error("expected the unreachable source to be reported")
	}
	if len(dirs) != 1 {
		t.Fatalf("got %d dirs, want the cached copy to still be usable", len(dirs))
	}
	if s, err := loadFrom(dirs[0], SourceRemote); err != nil || s.Name != "cached" {
		t.Errorf("cached skill unusable: %v / %+v", err, s)
	}
}

// The manifest is written by whoever hosts the URL, so a path in it is
// untrusted input: an escape would write outside the cache entirely.
func TestSafeRelPath_RejectsEscapes(t *testing.T) {
	for _, bad := range []string{"", "   ", "/etc/passwd", "../outside", "a/../../outside", `..\windows`, "C:/x"} {
		if got, err := safeRelPath(bad); err == nil {
			t.Errorf("safeRelPath(%q) = %q, want an error", bad, got)
		}
	}
	for _, ok := range []string{"SKILL.md", "scripts/run.sh", "./REFERENCE.md"} {
		if _, err := safeRelPath(ok); err != nil {
			t.Errorf("safeRelPath(%q) rejected a legitimate path: %v", ok, err)
		}
	}
}

// A manifest naming a traversal path must fail the whole download rather than
// write one file outside the cache and continue.
func TestFetchRemote_RefusesATraversingManifest(t *testing.T) {
	index := `{"version":"1.0.0","skills":[{"name":"evil","files":["SKILL.md","../../escaped.sh"]}]}`
	srv, _ := remoteServer(t, index, map[string]string{
		"evil/SKILL.md": "---\nname: evil\n---\nbody\n",
	})
	cache := t.TempDir()

	if _, err := fetchRemote(context.Background(), srv.Client(), cache, srv.URL+"/skills/index.json"); err == nil {
		t.Fatal("expected the traversing manifest to be refused")
	}
	// Nothing named by the traversing entry may exist anywhere under the cache,
	// which is where "../../" from a staging directory would land it.
	filepath.WalkDir(cache, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "escaped.sh" {
			t.Errorf("the traversing file was written to %s", path)
		}
		return nil
	})
}

// A version string is chosen by the remote and may contain anything, including
// separators that would make it a path rather than a directory name. The
// readable part survives so the cache stays inspectable by hand.
func TestVersionKey_IsSafeAsADirectoryName(t *testing.T) {
	prefixes := map[string]string{
		"1.0.0":         "1.0.0-",
		"../../etc":     "etc-",
		"":              "unversioned-",
		"  ":            "unversioned-",
		"feat/branch#1": "feat-branch-1-",
		"...":           "unversioned-",
	}
	for in, wantPrefix := range prefixes {
		got := versionKey(in)
		if !strings.HasPrefix(got, wantPrefix) {
			t.Errorf("versionKey(%q) = %q, want the prefix %q", in, got, wantPrefix)
		}
		if strings.ContainsAny(got, `/\`) || got == "." || got == ".." {
			t.Errorf("versionKey(%q) = %q is not usable as a directory name", in, got)
		}
		if got != versionKey(in) {
			t.Errorf("versionKey(%q) is not stable across calls", in)
		}
	}
}

// Sanitizing collapses characters, so two different versions can reduce to the
// same readable name. If they also shared a cache directory, the second would
// silently be served the first's contents.
func TestVersionKey_DistinctVersionsStayDistinct(t *testing.T) {
	a := versionKey("1.0/beta")
	b := versionKey("1.0-beta")
	if a == b {
		t.Errorf("versionKey collapsed two different versions onto %q", a)
	}
}

// A manifest whose SKILL.md is malformed must fail before it is published, not
// contribute a cached skill that then never loads.
func TestFetchRemote_RefusesAManifestWhoseSkillDoesNotParse(t *testing.T) {
	index := `{"version":"1.0.0","skills":[{"name":"broken","files":["SKILL.md"]}]}`
	srv, _ := remoteServer(t, index, map[string]string{
		"broken/SKILL.md": "no frontmatter\n",
	})
	cache := t.TempDir()

	if _, err := fetchRemote(context.Background(), srv.Client(), cache, srv.URL+"/skills/index.json"); err == nil {
		t.Fatal("expected an unparseable remote skill to be refused")
	}
	root := filepath.Join(cache, urlKey(srv.URL+"/skills/index.json"))
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".staging-") {
				t.Errorf("version %q was published despite the failure", e.Name())
			}
		}
	}
}

func TestFetchRemote_RejectsNonHTTPURLs(t *testing.T) {
	if _, err := fetchRemote(context.Background(), http.DefaultClient, t.TempDir(), "file:///etc/passwd"); err == nil {
		t.Error("expected a file:// skills url to be refused")
	}
}
