package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	Image   string `json:"image"`   // base64-encoded JPEG
	Width   int    `json:"width"`   // page width in pixels
	Height  int    `json:"height"`  // page height in pixels
	PageNum int    `json:"pageNum"` // 1-based page number
}

// compileLatex is a shared helper that compiles LaTeX source to PDF.
// Returns the path to the generated PDF, a cleanup function, and any error.
func compileLatex(source string) (pdfPath string, cleanup func(), err error) {
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
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cmd := exec.CommandContext(ctx, "pdflatex",
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
		cancel()

		if runErr != nil {
			fullOutput := stdout.String() + stderr.String()
			if ctx.Err() == context.DeadlineExceeded {
				cleanup()
				return "", nil, &latexCompileError{
					msg:    "pdflatex compilation timed out after 60 seconds",
					output: fullOutput,
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
}

func (e *latexCompileError) Error() string { return e.msg }

// handleLatexCompile compiles LaTeX source to PDF and serves the result.
// POST /api/latex
func (s *Server) handleLatexCompile(w http.ResponseWriter, r *http.Request) {
	var req latexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		writeJSON(w, http.StatusBadRequest, latexResponse{Success: false, Error: "source is required"})
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

	pdfPath, cleanup, err := compileLatex(req.Source)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		switch e := err.(type) {
		case *latexCompileError:
			status := http.StatusUnprocessableEntity
			if e.msg == "pdflatex compilation timed out after 60 seconds" {
				status = http.StatusGatewayTimeout
			}
			writeJSON(w, status, latexResponse{Success: false, Error: e.msg, Output: e.output})
		default:
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

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="output.pdf"`)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfData)))
	w.Write(pdfData)
}

// handleLatexPages compiles LaTeX source to PDF and returns rendered page images.
// POST /api/latex/pages
func (s *Server) handleLatexPages(w http.ResponseWriter, r *http.Request) {
	var req latexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		writeJSON(w, http.StatusBadRequest, latexPagesResponse{Success: false, Error: "source is required"})
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

	pdfPath, cleanup, err := compileLatex(req.Source)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		switch e := err.(type) {
		case *latexCompileError:
			status := http.StatusUnprocessableEntity
			if e.msg == "pdflatex compilation timed out after 60 seconds" {
				status = http.StatusGatewayTimeout
			}
			writeJSON(w, status, latexPagesResponse{Success: false, Error: e.msg, Output: e.output})
		default:
			writeJSON(w, http.StatusInternalServerError, latexPagesResponse{Success: false, Error: err.Error()})
		}
		return
	}

	// Render pages using go-fitz
	doc, fitzErr := fitzOpen(pdfPath)
	if fitzErr != nil {
		// If fitz is not available, fall back to returning just the PDF base64
		pdfData, readErr := os.ReadFile(pdfPath)
		if readErr != nil {
			writeJSON(w, http.StatusInternalServerError, latexPagesResponse{Success: false, Error: fmt.Sprintf("read PDF: %v", readErr)})
			return
		}
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
		img, renderErr := doc.ImageDPI(i, 150)
		if renderErr != nil {
			continue
		}

		var buf bytes.Buffer
		if encErr := jpegEncode(&buf, img, 85); encErr != nil {
			continue
		}

		pages = append(pages, latexPageImage{
			Image:   base64.StdEncoding.EncodeToString(buf.Bytes()),
			Width:   img.Bounds().Dx(),
			Height:  img.Bounds().Dy(),
			PageNum: i + 1,
		})
	}

	// Also include the PDF as base64 for download
	pdfData, readErr := os.ReadFile(pdfPath)
	if readErr != nil {
		writeJSON(w, http.StatusInternalServerError, latexPagesResponse{Success: false, Error: fmt.Sprintf("read PDF: %v", readErr)})
		return
	}

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
