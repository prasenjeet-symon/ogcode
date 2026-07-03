package agent

import (
	"fmt"
	"os/exec"
	"strings"
)

// projectIndexPrompt returns the mandatory project index instructions section,
// scoped to the given agent role. The workflow tail is tailored to whether the
// agent can make changes (build) or is read-only (plan, note). Agents that have
// the codebase_map tool must use it before any file exploration.
func projectIndexPrompt(role string) string {
	// The final workflow step differs by role: write-capable agents make changes,
	// read-only agents produce their plan/note instead.
	finalStep := "Then make changes"
	switch role {
	case "plan":
		finalStep = "Then produce your plan"
	case "note":
		finalStep = "Then produce your note"
	}

	return `## Mandatory: Use Project Index Before Exploration

**Rule:** Before exploring any file, folder, or project structure, you **MUST** use the "codebase_map" tool first.

This applies to all of the following scenarios:

- **Starting a new task** — Call "codebase_map" before reading any source files.
- **Looking for a file** — Use "codebase_map" with an appropriate "subdir" parameter instead of guessing paths with glob or grep.
- **Understanding project structure** — Use "codebase_map" to get the labeled tree before diving into code.
- **Exploring a new package/directory** — Call "codebase_map" scoped to that directory.

### Why?

The project index provides **topic labels** and a **structured overview** of every indexed file — including PDF documents (each PDF leaf shows per-page labels so you can pick the right page before calling read_pdf_page). Using it first ensures:

1. **Faster navigation** — You immediately know which files are relevant without blind glob/grep searches.
2. **Better context** — Topic labels tell you what each file contains before you read it.
3. **Fewer mistakes** — You won't miss important files (including PDFs) or read irrelevant ones.

### Workflow

Task received
  → codebase_map(subdir=...)   ← MANDATORY FIRST STEP
  → Then read specific files
  → ` + finalStep + `

### When codebase_map is not enough

If codebase_map doesn't cover what you need (e.g., unindexed files, binary patterns), you may fall back to glob and grep. But codebase_map must always be the **first** exploration step.`
}

// memoryMDPrompt returns the MEMORY.md instructions section, adapted for the
// agent's capabilities. Agents without write/edit tools get read-only instructions;
// agents with those tools get instructions to create and maintain MEMORY.md.
func memoryMDPrompt(canWriteFiles bool) string {
	base := `## MEMORY.md — Project Long-Term Memory

`
	if canWriteFiles {
		base += `The content above in the <memory-md> tag is loaded from your project's MEMORY.md file(s). This is the project's persistent, cross-session knowledge base. It survives across conversations — unlike chat history, which resets each session.

`
	} else {
		base += `The content above in the <memory-md> tag is loaded from the project's MEMORY.md file(s). This is the project's persistent, cross-session knowledge base. It survives across conversations — unlike chat history, which resets each session. Treat it as reference material — you can read it but cannot modify it.

`
	}

	base += `### Purpose
MEMORY.md stores hard-won knowledge about this project that you would otherwise forget between sessions. Think of it as a lab notebook: a place to record what you've learned so your future self (and future sessions) don't have to rediscover it.

### What belongs in MEMORY.md
- **Decisions & rationale** — why a particular approach was chosen over alternatives
- **Patterns & conventions** — naming patterns, file organization, coding style, commit message format
- **Architecture notes** — how components connect, data flow, key abstractions
- **Gotchas & pitfalls** — things that broke unexpectedly, non-obvious behaviors, workarounds
- **Project-specific facts** — config values, API quirks, dependency versions, build commands
- **Workflow notes** — how to test, deploy, debug, or reproduce issues in this project

### What does NOT belong in MEMORY.md
- Temporary or per-session state (use chat context or agentic memory recall for that)
- Instructions or rules for how to behave (those go in AGENT.md, not MEMORY.md)
- Verbose logs or full file contents (link or reference them, don't copy them)
- Information that is obvious from reading the code itself

### How it differs from AGENT.md and agentic memory
- **AGENT.md** = behavioral instructions ("follow these rules", "always do X before Y"). It tells you HOW to act.
- **MEMORY.md** = factual knowledge ("we chose PostgreSQL over MongoDB because X", "the auth middleware lives in middleware/auth.go"). It tells you WHAT you know.
- **Agentic memory** (the <prior_context> block and memory_recall tool) = per-session conversation summaries. It provides continuity within a single session. MEMORY.md persists across all sessions.

`
	if canWriteFiles {
		base += `### How to maintain MEMORY.md
- Use the read tool to inspect current contents before making changes
- Use the edit tool for targeted updates (preferred — avoids rewriting the whole file)
- Use the write tool only when restructuring the entire file or creating the file for the first time
- Update MEMORY.md proactively when you learn something important — do not wait to be asked
- Keep it concise and well-organized — future sessions must read and understand it quickly
- Remove or update stale entries when you discover they are no longer accurate`
	} else {
		base += `### How to use MEMORY.md
- Read it at the start of every session to load project context
- Reference it when making decisions — it contains hard-won knowledge from past sessions
- Note any facts you discover that should be recorded — a future session with write access can add them
- Do not attempt to modify MEMORY.md — you are a read-only agent`
	}

	return base
}

// markdownCapabilitiesPrompt returns the markdown output section that agents
// with rendering capabilities should include.
func markdownCapabilitiesPrompt() string {
	return `## Markdown output capabilities

The chat interface natively renders the following — use them when they add genuine clarity:

- **Mermaid diagrams** (triple-backtick mermaid blocks) — flows, architectures, sequences, entity relationships.
- **LaTeX math** — inline with $...$ and display block with $$...$$ — for mathematical formulas and equations.
- **LaTeX documents** (triple-backtick latex blocks) — full LaTeX documents compiled and rendered inline as page images in the chat viewport. Use this for reports, papers, resumes, letters, and any formatted document that needs professional typesetting. The block should contain a complete LaTeX document with \documentclass, \begin{document}...\end{document}, etc. The interface will automatically compile the document and display the rendered pages inline, with a PDF download button and a source code toggle. The latex_to_pdf tool is also available for programmatic PDF generation.
- **Plotly charts** (triple-backtick plotly blocks) — bar, line, scatter, pie, heatmap, and more. The block must contain a valid JSON object with a "data" array and optional "layout" object following the Plotly.js spec.
- **Rough diagrams** (triple-backtick rough blocks) — hand-drawn style 2D diagrams. The block must contain a valid JSON object with an "elements" array and optional "width"/"height"/"options" fields. Each element has a "type" (rectangle, circle, ellipse, line, arrow, path, linearPath, polygon, text) plus type-specific coordinates and optional RoughJS style options (stroke, fill, roughness, bowing, fillStyle, etc.).
- **HTML/CSS/JS** (triple-backtick html blocks) — full interactive content rendered in a sandboxed iframe. Use this for rich visualizations, custom dashboards, interactive widgets, styled tables, animated content, or any presentation that goes beyond static markdown. The block should contain a complete HTML document (or fragment with inline <style> and <script>). CSS is fully supported. JavaScript runs in a sandbox with no access to the parent page. The iframe has a transparent background with no border — it blends seamlessly into the chat. **Do NOT add a background color, gradient, or card-like container to your HTML.** Design your content to feel like a natural part of the conversation. If you need visual sections, use subtle borders or spacing instead of opaque backgrounds. Use the viewport dimensions provided below to make your content responsive — design for the available width and height.`
}

// latexEnv holds information about the detected LaTeX installation.
type latexEnv struct {
	Available    bool
	VersionLine  string // e.g. "pdfTeX 3.141592653-2.6-1.40.29 (TeX Live 2026)"
	Distribution string // e.g. "TeX Live 2026", "MiKTeX 24.1"
	DocClasses   []string
	Packages     []string
}

// detectedLatexEnv caches the result of LaTeX environment detection.
var detectedLatexEnv *latexEnv

// getLatexEnv detects the installed LaTeX environment by running pdflatex
// --version and checking for common document classes and packages. The result
// is cached after the first call.
func getLatexEnv() *latexEnv {
	if detectedLatexEnv != nil {
		return detectedLatexEnv
	}

	env := &latexEnv{}

	// Check pdflatex availability and version
	path, err := exec.LookPath("pdflatex")
	if err != nil || path == "" {
		detectedLatexEnv = env
		return env
	}
	env.Available = true

	out, err := exec.Command("pdflatex", "--version").Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) > 0 {
			env.VersionLine = strings.TrimSpace(lines[0])
		}
		// Extract distribution from the version line
		// e.g. "pdfTeX 3.141592653-2.6-1.40.29 (TeX Live 2026)"
		if idx := strings.Index(env.VersionLine, "("); idx != -1 {
			dist := strings.TrimSpace(strings.TrimSuffix(env.VersionLine[idx+1:], ")"))
			env.Distribution = dist
		}
	}

	// Check common document classes
	for _, cls := range []string{"article", "report", "book", "letter", "beamer", "extarticle", "extreport", "extbook", "memoir", "scrartcl", "scrreprt", "scrbook"} {
		if kpsewhich(cls + ".cls") {
			env.DocClasses = append(env.DocClasses, cls)
		}
	}

	// Check common packages
	for _, pkg := range []string{
		"amsmath", "amssymb", "graphicx", "hyperref", "geometry",
		"tikz", "pgfplots", "listings", "fancyhdr", "xcolor",
		"booktabs", "tabularx", "enumitem", "parskip", "fontenc",
		"inputenc", "babel", "natbib", "biblatex", "caption",
		"subcaption", "multicol", "float", "algorithm2e", "algorithmic",
		"siunitx", "cleveref", "csquotes", "microtype", "fontspec",
		"unicode-math",
	} {
		if kpsewhich(pkg + ".sty") {
			env.Packages = append(env.Packages, pkg)
		}
	}

	detectedLatexEnv = env
	return env
}

// kpsewhich checks whether a TeX file is findable via kpsewhich.
func kpsewhich(file string) bool {
	out, err := exec.Command("kpsewhich", file).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// latexInfoPrompt returns a section describing the available LaTeX environment
// so agents can write compatible LaTeX documents. Returns empty string if
// pdflatex is not available.
func latexInfoPrompt() string {
	env := getLatexEnv()
	if !env.Available {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n## LaTeX environment\n\n")
	b.WriteString("pdflatex is available on this system. When writing LaTeX documents, target this installed version:\n\n")

	if env.VersionLine != "" {
		b.WriteString(fmt.Sprintf("- **Version:** %s\n", env.VersionLine))
	}
	if env.Distribution != "" {
		b.WriteString(fmt.Sprintf("- **Distribution:** %s\n", env.Distribution))
	}

	if len(env.DocClasses) > 0 {
		b.WriteString(fmt.Sprintf("- **Available document classes:** %s\n", strings.Join(env.DocClasses, ", ")))
	}

	if len(env.Packages) > 0 {
		b.WriteString(fmt.Sprintf("- **Available packages:** %s\n", strings.Join(env.Packages, ", ")))
	}

	b.WriteString("\nWrite LaTeX that is compatible with the installed version. Avoid using packages or commands that are not listed above unless you are confident they are available. Prefer standard document classes (article, report, book) for maximum compatibility.\n")

	return b.String()
}

// viewportPrompt returns a section telling the agent about the user's
// rendering viewport so it can make responsive design decisions.
func viewportPrompt(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	return fmt.Sprintf(`

## Rendering viewport

The user's chat viewport is approximately %d×%d pixels (width × height). Design your visual output — HTML content, Plotly charts, Mermaid diagrams, Rough diagrams, and any other rendered content — to fit within these dimensions. Use responsive CSS (flexbox, grid, percentage widths, max-width) when creating HTML content so it adapts gracefully to different screen sizes.`, width, height)
}

// parallelToolCallsPrompt returns the shared section about making parallel
// independent tool calls for efficiency.
func parallelToolCallsPrompt() string {
	return `## Parallel tool calls

When you need to make multiple tool calls and they are independent of each other (i.e., the result of one does not affect the inputs of another), make all the calls in the same response block rather than making them sequentially. This significantly improves efficiency and reduces latency. For example, if you need to read three unrelated files, invoke all three read calls together rather than one after another.`
}

// projectNotesPrompt returns the project notes section, adapted for the agent's
// capabilities. Agents with write/edit tools get an explicit read-only restriction
// for the .ogcode/notes/ directory (notes are managed exclusively by the NoteAgent
// through its backend flow). Read-only agents get the basic notes guidance.
func projectNotesPrompt(canWriteFiles bool) string {
	prompt := `## Project notes

Project notes are saved in .ogcode/notes/ as markdown files. Before starting, check if any existing notes are relevant to the task by globbing .ogcode/notes/*.md and reading the ones that look relevant. Use them as context — don't repeat what is already documented.`

	if canWriteFiles {
		prompt += `

The .ogcode/notes/ directory is managed exclusively by the NoteAgent. Do not create, modify, or delete any files in .ogcode/notes/. You may only read notes from this directory for context.`
	}

	return prompt
}

// noPackageManagerDirsPrompt returns the shared admonition to avoid exploring
// dependency directories.
func noPackageManagerDirsPrompt() string {
	return `- Never explore or read package manager or dependency directories (e.g. node_modules, vendor, .venv, __pycache__, dist) unless a specific issue explicitly requires it. These directories contain third-party code and are not part of the project implementation.`
}
