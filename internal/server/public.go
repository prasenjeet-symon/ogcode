package server

import (
	"net/http"
	"os"
	"path/filepath"
)

// publicDir returns the absolute path of the workspace's public folder, the one
// directory whose contents the running server exposes to the browser over HTTP.
func (s *Server) publicDir() string {
	return filepath.Join(s.dir, "public")
}

// ensurePublicDir creates the workspace's public/ folder at startup. It is the
// agreed place for the agent to leave assets (a built site, generated files)
// for the UI to fetch and display or the user to download. Creating it eagerly
// keeps the served route stable even while the folder is still empty.
func (s *Server) ensurePublicDir() error {
	return os.MkdirAll(s.publicDir(), 0o755)
}

// servePublic wires the workspace public/ directory onto /public. It is
// registered before the SPA fallback so an exact path like /public/foo.pdf hits
// this handler rather than the embedded index.html. http.FileServer (built on
// http.ServeContent) gives proper Last-Modified validators and Range support,
// so large assets stream and resume rather than loading wholesale. HEAD is
// mapped explicitly because chi does not auto-map HEAD onto Get routes.
func (s *Server) servePublic(r chiRouter) {
	// http.FileServer routes on the URL path, so strip the /public prefix before
	// it hits http.Dir — otherwise it looks for <dir>/public/<public-path>.
	fileServer := http.StripPrefix("/public", http.FileServer(http.Dir(s.publicDir())))
	r.Get("/public", fileServer.ServeHTTP)
	r.Get("/public/*", fileServer.ServeHTTP)
	r.Head("/public", fileServer.ServeHTTP)
	r.Head("/public/*", fileServer.ServeHTTP)
}
