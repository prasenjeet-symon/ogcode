package server

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// latexRequest is the JSON body for POST /api/latex and POST /api/latex/pages
type latexRequest struct {
	Source string `json:"source"`
}

// latexResponse is the JSON response for the LaTeX compilation endpoint.
type latexResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Output  string `json:"output,omitempty"` // pdflatex output (for debugging)
}

// latexPagesResponse is the JSON response for the LaTeX pages rendering endpoint.
type latexPagesResponse struct {
	Success   bool             `json:"success"`
	Error     string           `json:"error,omitempty"`
	Output    string           `json:"output,omitempty"`
	DocClass  string           `json:"docClass,omitempty"`
	Title     string           `json:"title,omitempty"`
	Pages     []latexPageImage `json:"pages,omitempty"`
	PdfBase64 string           `json:"pdfBase64,omitempty"`
	PdfSize   int64            `json:"pdfSize,omitempty"`
}

// latexPageImage represents a single rendered page of a LaTeX document.
type latexPageImage struct {
	Image   string `json:"image,omitempty"` // base64-encoded JPEG
	Width   int    `json:"width"`           // page width in pixels
	Height  int    `json:"height"`          // page height in pixels
	PageNum int    `json:"pageNum"`         // 1-based page number
	Error   string `json:"error,omitempty"` // per-page render/encode failure
}

// Bounds and caches for the LaTeX endpoints.
const (
	// maxLatexBodyBytes caps /api/latex* JSON bodies (source text). Oversize
	// bodies are rejected with 413 before any compile starts.
	maxLatexBodyBytes = 256 << 10
	// maxLatexLogBytes caps captured pdflatex log output echoed in JSON (~8KB).
	maxLatexLogBytes = 8 << 10
	// latexRenderDPI is the fixed render resolution for page previews.
	latexRenderDPI = 150
	// latexCompileTimeout bounds each pdflatex pass.
	latexCompileTimeout = 60 * time.Second
	// latexCacheMaxEntries and latexCacheTTL bound the compile cache: ~20
	// recent sources held for ~10 minutes so repeated identical-source POSTs
	// (streaming re-sends, double submits) skip pdflatex entirely.
	latexCacheMaxEntries = 20
	latexCacheTTL        = 10 * time.Minute
)

// latexCacheEntry is one compiled PDF keyed by the sha256 of its source.
type latexCacheEntry struct {
	key     string
	pdf     []byte
	expires time.Time
}

// latexCompileCache is a small in-memory LRU of recent compilations.
var latexCompileCache = struct {
	sync.Mutex
	items map[string]*list.Element
	order *list.List // front = most recently used; values are *latexCacheEntry
}{items: map[string]*list.Element{}, order: list.New()}

// latexSourceKey hashes LaTeX source for cache lookup and ETags.
func latexSourceKey(source string) string {
	sum := sha256.Sum256([]byte(source))
	return fmt.Sprintf("%x", sum)
}

// latexCacheGet returns the cached PDF for key, moving it to the front.
// Expired entries are dropped on read.
func latexCacheGet(key string) ([]byte, bool) {
	latexCompileCache.Lock()
	defer latexCompileCache.Unlock()
	el, ok := latexCompileCache.items[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*latexCacheEntry)
	if time.Now().After(e.expires) {
		latexCompileCache.order.Remove(el)
		delete(latexCompileCache.items, key)
		return nil, false
	}
	latexCompileCache.order.MoveToFront(el)
	return e.pdf, true
}

// latexCachePut stores pdf under key, evicting least-recently-used entries
// past the cap.
func latexCachePut(key string, pdf []byte) {
	latexCompileCache.Lock()
	defer latexCompileCache.Unlock()
	if el, ok := latexCompileCache.items[key]; ok {
		latexCompileCache.order.MoveToFront(el)
		el.Value.(*latexCacheEntry).pdf = pdf
		el.Value.(*latexCacheEntry).expires = time.Now().Add(latexCacheTTL)
		return
	}
	el := latexCompileCache.order.PushFront(&latexCacheEntry{key: key, pdf: pdf, expires: time.Now().Add(latexCacheTTL)})
	latexCompileCache.items[key] = el
	for latexCompileCache.order.Len() > latexCacheMaxEntries {
		back := latexCompileCache.order.Back()
		if back == nil {
			break
		}
		latexCompileCache.order.Remove(back)
		delete(latexCompileCache.items, back.Value.(*latexCacheEntry).key)
	}
}

// decodeLatexRequest bounds and decodes a LaTeX JSON body. Oversize bodies
// get 413; anything else undecodable gets 400.
func decodeLatexRequest(w http.ResponseWriter, r *http.Request) (latexRequest, bool) {
	var req latexRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxLatexBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			http.Error(w, "request body too large (max 256KB)", http.StatusRequestEntityTooLarge)
			return req, false
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return req, false
	}
	return req, true
}

// serveLatexPDF serves compiled PDF bytes with an ETag so re-posts can
// revalidate.
func serveLatexPDF(w http.ResponseWriter, pdfData []byte) {
	sum := sha256.Sum256(pdfData)
	w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sum))
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="output.pdf"`)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Write(pdfData)
}

// matchIfNoneMatch reports whether the request's If-None-Match preconditions
// already hold for etag (quoted, as served).
func matchIfNoneMatch(r *http.Request, etag string) bool {
	for _, part := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if part = strings.TrimSpace(part); part == "*" || part == etag {
			return true
		}
	}
	return false
}

// truncateLatexLog caps pdflatex log output echoed back in JSON responses.
func truncateLatexLog(s string) string {
	if len(s) > maxLatexLogBytes {
		return s[:maxLatexLogBytes] + "\n...[truncated]"
	}
	return s
}

// materializeCachedPDF writes cached PDF bytes to a temp file fitz can open.
// The caller removes it via the returned cleanup.
func materializeCachedPDF(pdfData []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "ogcode-latex-cache-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	pdfPath := filepath.Join(dir, "input.pdf")
	if err := os.WriteFile(pdfPath, pdfData, 0o644); err != nil {
		cleanup()
		return "", nil, err
	}
	return pdfPath, cleanup, nil
}

// compileLatex is a shared helper that compiles LaTeX source to PDF.
// Returns the path to the generated PDF, a cleanup function, and any error.
func compileLatex(ctx context.Context, source string) (pdfPath string, cleanup func(), err error) {
	// Create a temp directory for compilation
	tmpDir, mkdirErr := os.MkdirTemp("", "ogcode-latex-api-*")
	if mkdirErr != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", mkdirErr)
	}

	cleanup = func() { os.RemoveAll(tmpDir) }

	// Write the LaTeX source
	texPath := filepath.Join(tmpDir, "input.tex")
	if writeErr := os.WriteFile(texPath, []byte(source), 0o644); writeErr != nil {
		cleanup()
		return "", nil, fmt.Errorf("write tex file: %w", writeErr)
	}

	// Run pdflatex (two passes for references, TOC, etc.)
	var compileOutput string
	for pass := 1; pass <= 2; pass++ {
		// Derived from the request context, so a client abort or navigation
		// cancels the pass: CommandContext kills the pdflatex process.
		// -no-shell-escape keeps \write18 off: compilation must never run
		// shell commands from the document source.
		passCtx, cancel := context.WithTimeout(ctx, latexCompileTimeout)
		cmd := exec.CommandContext(passCtx, "pdflatex",
			"-no-shell-escape",
			"-interaction=nonstopmode",
			"-halt-on-error",
			"-output-directory="+tmpDir,
			texPath,
		)
		cmd.Dir = tmpDir
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		runErr := cmd.Run()
		// errors.Is on the pass context, not a message compare: a client
		// abort cancels it too, and CommandContext already killed pdflatex.
		timedOut := errors.Is(passCtx.Err(), context.DeadlineExceeded)
		cancel()

		if runErr != nil {
			fullOutput := stdout.String() + stderr.String()
			if timedOut {
				cleanup()
				return "", nil, &latexCompileError{
					msg:      "pdflatex compilation timed out after 60 seconds",
					output:   fullOutput,
					timedOut: true,
				}
			}
			cleanup()
			return "", nil, &latexCompileError{
				msg:    fmt.Sprintf("pdflatex compilation failed (pass %d)", pass),
				output: fullOutput,
			}
		}
		compileOutput = stdout.String()
	}

	// Check the PDF was generated
	pdfPath = filepath.Join(tmpDir, "input.pdf")
	if _, statErr := os.Stat(pdfPath); statErr != nil {
		cleanup()
		return "", nil, &latexCompileError{
			msg:    "pdflatex completed but no PDF was generated",
			output: compileOutput,
		}
	}

	return pdfPath, cleanup, nil
}

// latexCompileError is a structured error for LaTeX compilation failures.
type latexCompileError struct {
	msg    string
	output string
	// timedOut marks a pass that exceeded latexCompileTimeout, so handlers
	// map it to 504 without comparing message strings.
	timedOut bool
}

func (e *latexCompileError) Error() string { return e.msg }

// handleLatexCompile compiles LaTeX source to PDF and serves the result.
// POST /api/latex
func (s *Server) handleLatexCompile(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeLatexRequest(w, r)
	if !ok {
		return
	}
	if req.Source == "" {
		writeJSON(w, http.StatusBadRequest, latexResponse{Success: false, Error: "source is required"})
		return
	}

	// Repeat posts of the same source skip pdflatex entirely.
	key := latexSourceKey(req.Source)
	if pdfData, hit := latexCacheGet(key); hit {
		serveLatexPDF(w, pdfData)
		return
	}

	// Check if pdflatex is available
	if _, err := exec.LookPath("pdflatex"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, latexResponse{
			Success: false,
			Error:   "pdflatex is not installed. Install a TeX distribution (e.g. brew install --cask mactex on macOS, or sudo apt install texlive on Linux).",
		})
		return
	}

	pdfPath, cleanup, err := compileLatex(r.Context(), req.Source)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		var cerr *latexCompileError
		if errors.As(err, &cerr) {
			status := http.StatusUnprocessableEntity
			if cerr.timedOut {
				status = http.StatusGatewayTimeout
			}
			writeJSON(w, status, latexResponse{Success: false, Error: cerr.msg, Output: truncateLatexLog(cerr.output)})
		} else {
			writeJSON(w, http.StatusInternalServerError, latexResponse{Success: false, Error: err.Error()})
		}
		return
	}

	// Serve the PDF directly
	pdfData, readErr := os.ReadFile(pdfPath)
	if readErr != nil {
		writeJSON(w, http.StatusInternalServerError, latexResponse{Success: false, Error: fmt.Sprintf("read PDF: %v", readErr)})
		return
	}
	latexCachePut(key, pdfData)
	serveLatexPDF(w, pdfData)
}

// handleLatexPages compiles LaTeX source to PDF and returns rendered page images.
// POST /api/latex/pages
func (s *Server) handleLatexPages(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeLatexRequest(w, r)
	if !ok {
		return
	}
	if req.Source == "" {
		writeJSON(w, http.StatusBadRequest, latexPagesResponse{Success: false, Error: "source is required"})
		return
	}

	// The rendered output is a pure function of the source, so the source
	// hash is the ETag: identical re-posts revalidate with 304.
	key := latexSourceKey(req.Source)
	etag := `"` + key + `"`
	if matchIfNoneMatch(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// Check if pdflatex is available
	if _, err := exec.LookPath("pdflatex"); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, latexPagesResponse{
			Success: false,
			Error:   "pdflatex is not installed. Install a TeX distribution (e.g. brew install --cask mactex on macOS, or sudo apt install texlive on Linux).",
		})
		return
	}

	// Extract metadata from source
	docClassMatch := regexpLatexDocClass.FindStringSubmatch(req.Source)
	titleMatch := regexpLatexTitle.FindStringSubmatch(req.Source)
	docClass := "article"
	if len(docClassMatch) > 1 {
		docClass = docClassMatch[1]
	}
	title := ""
	if len(titleMatch) > 1 {
		title = titleMatch[1]
	}

	// Cache hits skip the compile and go straight to the bytes below;
	// misses fall through to a fresh two-pass run.
	pdfData, hit := latexCacheGet(key)
	var pdfPath string
	var cleanup func()
	if hit {
		var matErr error
		pdfPath, cleanup, matErr = materializeCachedPDF(pdfData)
		if matErr != nil {
			writeJSON(w, http.StatusInternalServerError, latexPagesResponse{Success: false, Error: matErr.Error()})
			return
		}
		defer cleanup()
	} else {
		var compErr error
		pdfPath, cleanup, compErr = compileLatex(r.Context(), req.Source)
		if cleanup != nil {
			defer cleanup()
		}
		if compErr != nil {
			var cerr *latexCompileError
			if errors.As(compErr, &cerr) {
				status := http.StatusUnprocessableEntity
				if cerr.timedOut {
					status = http.StatusGatewayTimeout
				}
				writeJSON(w, status, latexPagesResponse{Success: false, Error: cerr.msg, Output: truncateLatexLog(cerr.output)})
			} else {
				writeJSON(w, http.StatusInternalServerError, latexPagesResponse{Success: false, Error: compErr.Error()})
			}
			return
		}
		var readErr error
		pdfData, readErr = os.ReadFile(pdfPath)
		if readErr != nil {
			writeJSON(w, http.StatusInternalServerError, latexPagesResponse{Success: false, Error: fmt.Sprintf("read PDF: %v", readErr)})
			return
		}
		latexCachePut(key, pdfData)
	}

	// Render pages using go-fitz
	doc, fitzErr := fitzOpen(pdfPath)
	if fitzErr != nil {
		// If fitz is not available, fall back to returning just the PDF base64.
		w.Header().Set("ETag", etag)
		writeJSON(w, http.StatusOK, latexPagesResponse{
			Success:   true,
			DocClass:  docClass,
			Title:     title,
			PdfBase64: base64.StdEncoding.EncodeToString(pdfData),
			PdfSize:   int64(len(pdfData)),
		})
		return
	}
	defer doc.Close()

	pages := make([]latexPageImage, 0, doc.NumPage())
	for i := 0; i < doc.NumPage(); i++ {
		img, renderErr := doc.ImageDPI(i, latexRenderDPI)
		if renderErr != nil {
			// Keep the page slot with its number so callers never see a
			// silently renumbered sequence.
			pages = append(pages, latexPageImage{PageNum: i + 1, Error: renderErr.Error()})
			continue
		}

		var buf bytes.Buffer
		if encErr := jpegEncode(&buf, img, 85); encErr != nil {
			pages = append(pages, latexPageImage{PageNum: i + 1, Error: encErr.Error()})
			continue
		}

		pages = append(pages, latexPageImage{
			Image:   base64.StdEncoding.EncodeToString(buf.Bytes()),
			Width:   img.Bounds().Dx(),
			Height:  img.Bounds().Dy(),
			PageNum: i + 1,
		})
	}

	// Also include the PDF as base64 for download. The bytes are already in
	// hand from the compile/cache step above — no re-read.
	w.Header().Set("ETag", etag)
	writeJSON(w, http.StatusOK, latexPagesResponse{
		Success:   true,
		DocClass:  docClass,
		Title:     title,
		Pages:     pages,
		PdfBase64: base64.StdEncoding.EncodeToString(pdfData),
		PdfSize:   int64(len(pdfData)),
	})
}

// handleLatexStatus returns whether pdflatex is available on the system.
// GET /api/latex/status
func (s *Server) handleLatexStatus(w http.ResponseWriter, r *http.Request) {
	path, err := exec.LookPath("pdflatex")
	available := err == nil

	result := map[string]any{
		"available": available,
	}

	if available {
		out, verErr := exec.Command("pdflatex", "--version").Output()
		if verErr == nil {
			lines := strings.Split(string(out), "\n")
			if len(lines) > 0 {
				result["version"] = strings.TrimSpace(lines[0])
			}
			// Extract distribution from version line, e.g. "(TeX Live 2026)"
			if len(lines) > 0 {
				if idx := strings.Index(lines[0], "("); idx != -1 {
					dist := strings.TrimSpace(strings.TrimSuffix(lines[0][idx+1:], ")"))
					result["distribution"] = dist
				}
			}
		}
		result["path"] = path
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) registerLatexRoutes(r chi.Router) {
	r.Post("/latex", s.handleLatexCompile)
	r.Post("/latex/pages", s.handleLatexPages)
	r.Get("/latex/status", s.handleLatexStatus)
}
