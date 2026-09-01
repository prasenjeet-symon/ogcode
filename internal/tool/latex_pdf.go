package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LatexToPdfTool compiles LaTeX source to PDF using pdflatex.
// It returns the path to the generated PDF file and, for vision-capable models,
// renders the first page as a JPEG image.
type LatexToPdfTool struct{}

func (LatexToPdfTool) ID() string { return "latex_to_pdf" }

func (LatexToPdfTool) Description() string {
	return "Compile LaTeX source code to PDF. Returns the path to the generated PDF file. Requires a TeX distribution (pdflatex) installed on the system. Use this to render full LaTeX documents (reports, papers, resumes, etc.) to PDF. The LaTeX source should be a complete document with \\documentclass and \\begin{document}...\\end{document}."
}

func (LatexToPdfTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["source"],
		"properties": {
			"source": {
				"type": "string",
				"description": "Complete LaTeX source code (must include \\documentclass and \\begin{document}...\\end{document})"
			},
			"filename": {
				"type": "string",
				"description": "Output filename for the PDF (default: output.pdf). Must end with .pdf"
			}
		}
	}`)
}

// pdflatexAvailability caches whether pdflatex was found on the system PATH.
var pdflatexAvailability struct {
	checked bool
	found   bool
}

// pdflatexAvailable checks whether pdflatex is available on the system PATH.
func pdflatexAvailable() bool {
	if pdflatexAvailability.checked {
		return pdflatexAvailability.found
	}
	_, err := exec.LookPath("pdflatex")
	pdflatexAvailability.checked = true
	pdflatexAvailability.found = err == nil
	return pdflatexAvailability.found
}

func (LatexToPdfTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Source   string `json:"source"`
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{}, fmt.Errorf("parse latex_to_pdf args: %w", err)
	}
	if params.Source == "" {
		return Result{Title: "LaTeX to PDF", Output: "source is required"}, nil
	}

	if !pdflatexAvailable() {
		return Result{
			Title:  "LaTeX to PDF — pdflatex not found",
			Output: "pdflatex is not installed on this system. Install a TeX distribution to use this tool:\n\n• macOS: brew install --cask mactex  (or: brew install basictex)\n• Linux: sudo apt install texlive  (or: sudo apt install texlive-latex-base)\n• Windows: Install MiKTeX or TeX Live\n\nAlternatively, write the LaTeX source to a .tex file and compile it externally.",
		}, nil
	}

	// Default filename
	filename := params.Filename
	if filename == "" {
		filename = "output.pdf"
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".pdf") {
		filename += ".pdf"
	}

	// Create a temp directory for compilation
	tmpDir, err := os.MkdirTemp("", "ogcode-latex-*")
	if err != nil {
		return Result{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write the LaTeX source to a .tex file
	texPath := filepath.Join(tmpDir, "input.tex")
	if err := os.WriteFile(texPath, []byte(params.Source), 0o644); err != nil {
		return Result{}, fmt.Errorf("write tex file: %w", err)
	}

	// Run pdflatex (two passes for references, TOC, etc.)
	var compileOutput string
	for pass := 1; pass <= 2; pass++ {
		compileCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		cmd := exec.CommandContext(compileCtx, "pdflatex",
			"-interaction=nonstopmode",
			"-halt-on-error",
			"-output-directory="+tmpDir,
			texPath,
		)
		cmd.Dir = tmpDir
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		cancel() // release context resources immediately

		if err != nil {
			fullOutput := stdout.String() + stderr.String()
			// Check if it was a timeout
			if compileCtx.Err() == context.DeadlineExceeded {
				return Result{
					Title:  "LaTeX to PDF — compilation timed out",
					Output: "pdflatex compilation timed out after 60 seconds. Your document may be too complex or contain an infinite loop.",
				}, nil
			}
			// Extract the error lines from pdflatex output
			errLines := extractLatexErrors(fullOutput)
			if errLines == "" {
				errLines = err.Error()
			}
			return Result{
				Title:  "LaTeX to PDF — compilation failed",
				Output: fmt.Sprintf("pdflatex compilation failed (pass %d):\n\n%s\n\nFull output:\n%s", pass, errLines, truncateString(fullOutput, 3000)),
			}, nil
		}
		compileOutput = stdout.String()
	}

	// Check the PDF was generated
	pdfPath := filepath.Join(tmpDir, "input.pdf")
	pdfInfo, err := os.Stat(pdfPath)
	if err != nil {
		return Result{
			Title:  "LaTeX to PDF — no PDF generated",
			Output: fmt.Sprintf("pdflatex completed but no PDF was generated.\n\nOutput:\n%s", truncateString(compileOutput, 3000)),
		}, nil
	}

	// Copy the PDF to the session directory
	outputDir := tctx.SessionDir
	outputPath := filepath.Join(outputDir, filename)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("create output dir: %w", err)
	}

	pdfData, err := os.ReadFile(pdfPath)
	if err != nil {
		return Result{}, fmt.Errorf("read generated PDF: %w", err)
	}
	if err := os.WriteFile(outputPath, pdfData, 0o644); err != nil {
		return Result{}, fmt.Errorf("write output PDF: %w", err)
	}

	result := Result{
		Title: fmt.Sprintf("LaTeX to PDF — %s (%s)", filename, formatFileSize(pdfInfo.Size())),
		Output: fmt.Sprintf("PDF generated successfully: %s (%s)\n\nYou can open this file to view the rendered LaTeX document.",
			outputPath, formatFileSize(pdfInfo.Size())),
	}

	// For vision-capable models, try to render the first page as an image
	if tctx.ModelSupportsImages {
		if data, mediaType, rerr := renderPDFPageImage(pdfPath, 1); rerr == nil {
			result.Image = &ResultImage{
				MediaType: mediaType,
				Data:      base64.StdEncoding.EncodeToString(data),
			}
			result.Output += "\n\nFirst page preview is attached."
		}
	}

	return result, nil
}

// extractLatexErrors pulls out error lines from pdflatex output.
func extractLatexErrors(output string) string {
	var errors []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "!") || strings.HasPrefix(line, "LaTeX Error") {
			errors = append(errors, line)
		}
	}
	if len(errors) == 0 {
		return ""
	}
	return strings.Join(errors, "\n")
}

// truncateString truncates a string to maxLen characters.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// formatFileSize formats a file size in human-readable form.
func formatFileSize(size int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
	)
	switch {
	case size >= MB:
		return fmt.Sprintf("%.1f MB", float64(size)/float64(MB))
	case size >= KB:
		return fmt.Sprintf("%.1f KB", float64(size)/float64(KB))
	default:
		return fmt.Sprintf("%d B", size)
	}
}
