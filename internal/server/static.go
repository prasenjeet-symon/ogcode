package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/prasenjeet-symon/ogcode/web"
)

func (s *Server) serveStatic(r chiRouter) {
	sub, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>ogcode</title></head>
<body>
<h1>ogcode</h1>
<p>Server is running. Web UI not built.</p>
</body>
</html>`))
		})
		return
	}

	// Read index.html once for SPA fallback
	indexHTML, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		indexHTML = []byte("<!DOCTYPE html><html><body><h1>ogcode</h1></body></html>")
	}

	// A validator for the shell, so revalidating it costs a 304 instead of a
	// fresh copy. The bytes are fixed at build time, so hashing once is enough.
	// Files in an embed.FS report a zero mtime, so Last-Modified is never sent
	// and an ETag is the only validator available to us.
	sum := sha256.Sum256(indexHTML)
	indexETag := `"` + hex.EncodeToString(sum[:16]) + `"`

	fileServer := http.FileServer(http.FS(sub))

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve static assets first
		if r.URL.Path != "/" {
			// Check if a static file exists
			filePath := r.URL.Path[1:] // strip leading /
			if f, err := sub.Open(filePath); err == nil {
				f.Close()
				// Vite content-hashes everything under /assets/: the filename
				// changes whenever the bytes do, which is the one situation
				// where caching forever is exactly right. The rest of the
				// embedded tree (favicon, the two web fonts) keeps stable
				// names across releases, so it gets a modest lifetime rather
				// than being pinned for a year on a name that can be reused.
				if strings.HasPrefix(filePath, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "public, max-age=86400")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback: serve index.html for all other routes.
		//
		// The shell names which hashed bundle to load, so it must never be
		// reused without asking the server first: a stale copy kept across an
		// upgrade requests a bundle the new binary no longer embeds, and the
		// app comes up blank. "no-cache" still permits storing it — it forces
		// revalidation — and ServeContent answers those with a 304 using the
		// ETag above.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", indexETag)
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(indexHTML))
	})
}

type chiRouter interface {
	Get(pattern string, handler http.HandlerFunc)
	NotFound(handler http.HandlerFunc)
}