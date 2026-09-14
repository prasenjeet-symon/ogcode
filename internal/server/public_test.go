package server

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServe_PublicDir pins the core-server feature: every server instance
// exposes the workspace's public/ directory over HTTP at /public, serving file
// contents with a real (non-SPA-fallback) response, returning 404 for a missing
// file, and auto-creating the folder at startup.
func TestServe_PublicDir(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed a file so the fetch below has something to serve.
	if err := os.MkdirAll(filepath.Join(dir, "public"), 0o755); err != nil {
		t.Fatalf("seed public dir: %v", err)
	}
	const wantContent = "hello from public"
	if err := os.WriteFile(filepath.Join(dir, "public", "greet.txt"), []byte(wantContent), 0o644); err != nil {
		t.Fatalf("seed public file: %v", err)
	}

	srv := NewWithOptions(0, dir, ModeBuild, Options{Loopback: true, NoBrowser: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("Serve did not return after context cancel")
		}
	}()
	waitUp(t, srv)

	base := "http://127.0.0.1:" + itoa(srv.Port())

	t.Run("serves file contents", func(t *testing.T) {
		resp, err := http.Get(base + "/public/greet.txt")
		if err != nil {
			t.Fatalf("GET /public/greet.txt: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if string(body) != wantContent {
			t.Fatalf("body = %q, want %q", body, wantContent)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("Content-Type = %q, want text/plain", ct)
		}
	})

	t.Run("missing file returns 404 not SPA fallback", func(t *testing.T) {
		resp, err := http.Get(base + "/public/nope.txt")
		if err != nil {
			t.Fatalf("GET /public/nope.txt: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("auto-creates public dir at startup", func(t *testing.T) {
		// Remove the pre-seeded dir and start a fresh server: ensurePublicDir
		// should recreate it.
		if err := os.RemoveAll(filepath.Join(dir, "public")); err != nil {
			t.Fatalf("remove public dir: %v", err)
		}
		srv2 := NewWithOptions(0, dir, ModeBuild, Options{Loopback: true, NoBrowser: true})
		ctx2, cancel2 := context.WithCancel(context.Background())
		done2 := make(chan error, 1)
		go func() { done2 <- srv2.Serve(ctx2) }()
		waitUp(t, srv2)
		if _, err := os.Stat(filepath.Join(dir, "public")); err != nil {
			t.Fatalf("public dir not auto-created: %v", err)
		}
		cancel2()
		<-done2
	})

	// HEAD is mapped explicitly (chi does not auto-map HEAD onto Get routes);
	// the agent/UI probing a public asset with HEAD must get headers, not 405.
	// (The auto-create subtest above removed the seeded dir, so re-seed.)
	t.Run("HEAD returns headers not 405", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Join(dir, "public"), 0o755); err != nil {
			t.Fatalf("recreate public dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "public", "greet.txt"), []byte(wantContent), 0o644); err != nil {
			t.Fatalf("re-seed public file: %v", err)
		}
		req, err := http.NewRequest("HEAD", base+"/public/greet.txt", nil)
		if err != nil {
			t.Fatalf("build HEAD request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("HEAD /public/greet.txt: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("HEAD status = %d, want 200", resp.StatusCode)
		}
		if resp.ContentLength != int64(len(wantContent)) {
			t.Fatalf("HEAD Content-Length = %d, want %d", resp.ContentLength, len(wantContent))
		}
		body, _ := io.ReadAll(resp.Body)
		if len(body) != 0 {
			t.Fatalf("HEAD body = %d bytes, want 0", len(body))
		}
	})
}
