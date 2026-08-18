package agent

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// projectIndexPrompt returns the mandatory project index instructions section,
// scoped to the given agent role. The workflow tail is tailored to whether the
// agent can make changes (build) or is read-only (plan, note). Agents that have
// the codebase_map tool must use it before any file exploration.
//
// It also carries the file_map step. The two tools answer different questions —
// codebase_map which file, file_map where inside it — and file_map deliberately
// does not depend on the index, so it still applies in projects where
// codebase_map comes back empty.
func projectIndexPrompt(role string) string {
	// The final workflow step differs by role: write-capable agents make changes,
	// read-only agents produce their plan/note instead.
	finalStep := "Then make changes"
	switch role {
	case "plan":
		finalStep = "Then produce your plan"
	case "note":
		finalStep = "Then produce your note"
	case "subagent":
		finalStep = "Then report your findings"
	}

	return `## Mandatory: Use Project Index Before Exploration

**Rule:** When the project has been indexed, you **MUST** use the "codebase_map" tool first — before exploring any file, folder, or project structure. (If it returns an empty result, the project has not been indexed yet; fall straight back to glob/grep — see below.)

This applies to all of the following scenarios:

- **Starting a new task** — Call "codebase_map" before reading any source files.
- **Looking for a file** — Use "codebase_map" with an appropriate "subdir" parameter instead of guessing paths with glob or grep.
- **Understanding project structure** — Use "codebase_map" to get the labeled tree before diving into code.
- **Exploring a new package/directory** — Call "codebase_map" scoped to that directory.

### Why?

The project index provides **topic labels** and a **structured overview** of every indexed file — including PDF and DOCX documents (each document leaf shows per-page labels so you can pick the right page before calling read_pdf_page or read_docx_page). Using it first ensures:

1. **Faster navigation** — You immediately know which files are relevant without blind glob/grep searches.
2. **Better context** — Topic labels tell you what each file contains before you read it.
3. **Fewer mistakes** — You won't miss important files (including PDFs) or read irrelevant ones.

### Workflow

Task received
  → codebase_map(subdir=...)          ← MANDATORY FIRST STEP: which files matter
  → file_map(path)                    ← MANDATORY before reading an unfamiliar file: where things are inside it
  → read(path, start_line, end_line)  ← only the region you need
  → ` + finalStep + `

## Mandatory: Map a File Before Reading It

**Rule:** Before reading a source file you do not already know, you **MUST** call "file_map" on it, then read only the range you need. Read a file in full only when it is short, or when the map shows you genuinely need all of it.

This is enforced, not advisory: calling "read" on a file longer than 200 lines without a range returns that file's map instead of its contents. Reading the whole of a long file is not something you can do by accident, and pushing past it — with start_line=1 and end_line past the file length — should be rare and deliberate.

"file_map" returns every declaration in the file with its 1-based line range and doc comment, and those ranges go straight into "read":

  file_map("internal/tool/read.go")
    → 37-159  func (ReadTool) Execute(ctx context.Context, ...) (Result, error)

  read("internal/tool/read.go", start_line=37, end_line=159)

"start_line" and "end_line" are inclusive and use exactly the numbering "file_map" prints, so copy a range across as-is — never convert it to "offset". Declaration ranges already include the doc comment above them, so one read gives you both the code and its explanation.

Reading a 600-line file to look at one 40-line function puts the other 560 lines in your context for the rest of the turn, and they are re-sent on every step that follows. The map costs a few dozen lines and tells you which range to ask for.

Unlike "codebase_map", "file_map" needs no index: it parses the file on every call, so it works in any project, indexed or not, and its ranges always describe the file's current contents.

**After you edit a file, call "file_map" on it again.** An edit shifts every line below it, which silently invalidates any range you were given earlier in the session.

Indented entries in a map are nested inside the entry above them — a class's methods, or the handlers inside a component — so you can jump to one handler rather than reading the whole component.

### When codebase_map is empty or not enough

If codebase_map returns an empty (or nearly empty) result, the project has not been indexed yet — do not keep calling it this session; use glob and grep instead. If the index simply doesn't cover what you need (unindexed files, binary patterns), you may also fall back to glob and grep. codebase_map is your **first** exploration step whenever an index exists — but it is never a hard blocker on getting the work done. Note that "file_map" is unaffected by any of this: it does not use the index, so it still applies to every source file you are about to read.`
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
- **Agentic memory** (the <prior_context> block, memory_recall and project_memory_recall tools) = conversation summaries mined from chat history. memory_recall covers the current session; project_memory_recall covers every past session in this project. Both are recalled on demand and reflect what was *said*; MEMORY.md is curated knowledge you deliberately write down.

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

// osEnvInfo describes the host operating system and shell environment for the
// system prompt. OS version disambiguates a year-old OS from a current one
// (darwin/amd64 alone is ambiguous); the shell line tells the agent that the
// bash tool runs commands via "sh -c" so it writes POSIX-compatible shell
// instead of bashisms that silently break.
type osEnvInfo struct {
	OSVersion string // e.g. "macOS 15.5", "Ubuntu 24.04", "unknown"
}

// detectedOSEnv caches the result of OS environment detection so it is only
// probed once per process.
var detectedOSEnv *osEnvInfo

// getOSEnv detects the host OS version. The result is cached after the first
// call.
func getOSEnv() *osEnvInfo {
	if detectedOSEnv != nil {
		return detectedOSEnv
	}
	info := &osEnvInfo{OSVersion: detectOSVersion()}
	detectedOSEnv = info
	return info
}

// detectOSVersion returns a human-readable OS version string for the system
// prompt. It probes platform-specific sources (sw_vers on macOS, /etc/os-release
// on Linux, ver on Windows) and falls back to "unknown" when detection fails.
func detectOSVersion() string {
	switch runtime.GOOS {
	case "darwin":
		// sw_vers -productVersion → e.g. "15.5"
		out, err := exec.Command("sw_vers", "-productVersion").Output()
		if err != nil {
			return "macOS (version unknown)"
		}
		v := strings.TrimSpace(string(out))
		if v == "" {
			return "macOS (version unknown)"
		}
		return "macOS " + v
	case "linux":
		// /etc/os-release is the standard on modern distros.
		data, err := os.ReadFile("/etc/os-release")
		if err != nil {
			return "Linux (version unknown)"
		}
		name := ""
		version := ""
		for _, line := range strings.Split(string(data), "\n") {
			if after, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				name = strings.Trim(after, `"`)
				break
			}
			if after, ok := strings.CutPrefix(line, "NAME="); ok {
				name = strings.Trim(after, `"`)
			}
			if after, ok := strings.CutPrefix(line, "VERSION="); ok {
				version = strings.Trim(after, `"`)
			}
		}
		if name == "" {
			return "Linux (version unknown)"
		}
		if version != "" {
			return name + " " + version
		}
		return name
	case "windows":
		// `ver` returns a line like "Microsoft Windows [Version 10.0.22631.4602]"
		out, err := exec.Command("cmd", "/c", "ver").Output()
		if err != nil {
			return "Windows (version unknown)"
		}
		// Strip the leading "Microsoft Windows [Version " and trailing "]"
		s := strings.TrimSpace(string(out))
		if idx := strings.Index(s, "[Version "); idx != -1 {
			rest := s[idx+len("[Version "):]
			if end := strings.Index(rest, "]"); end != -1 {
				return "Windows " + rest[:end]
			}
		}
		return "Windows (version unknown)"
	default:
		return runtime.GOOS + " (version unknown)"
	}
}

// osEnvPrompt returns the OS version and shell environment lines appended to
// the static system prompt header. Both pieces are static within a session so
// they stay in the Anthropic cacheable prefix alongside the working directory
// and platform. The shell line matches what the bash tool actually invokes
// on the current OS so the agent writes compatible syntax on every platform.
func osEnvPrompt() string {
	info := getOSEnv()
	var b strings.Builder
	b.WriteString(fmt.Sprintf("\nOS: %s", info.OSVersion))
	if runtime.GOOS == "windows" {
		b.WriteString("\nShell: cmd (commands are executed via \"cmd /c\" — write Windows cmd.exe-compatible syntax, not POSIX sh)")
	} else {
		b.WriteString("\nShell: sh (commands are executed via \"sh -c\" — write POSIX-compatible shell, not bash-only syntax)")
	}
	return b.String()
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

// systemReminderPrompt returns the per-turn dynamic content (current date) as
// a <system-reminder> block. This is kept OUT of the main system prompt so the
// main prompt stays byte-for-byte identical across turns, enabling Anthropic's
// prompt cache to hit. The date changes every turn and would invalidate the
// cache if it were in the cached prefix. The working directory and platform are
// static within a session, so they remain in the main (cacheable) system prompt.
func systemReminderPrompt() string {
	now := time.Now().Format("Mon Jan 2 15:04:05 MST 2006")
	return fmt.Sprintf("<system-reminder>\nCurrent date: %s\n</system-reminder>", now)
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

// modelFamily classifies a provider/model into a prompt family so the coding
// prompt can be tuned to how that family follows instructions. Model-name signals
// win over the provider id, because aggregators (OpenRouter, the free pool) serve
// Claude, GPT, and Gemini models under a single provider id.
func modelFamily(providerID, modelID string) string {
	p := strings.ToLower(providerID)
	m := strings.ToLower(modelID)
	switch {
	case p == "ollama" || strings.Contains(p, "ollama"):
		// Local runtime — size/quantization matters more than the base model, so
		// treat all local models the same regardless of name.
		return "local"
	case strings.Contains(m, "claude"):
		return "anthropic"
	case strings.Contains(m, "gemini") || strings.Contains(m, "gemma"):
		return "gemini"
	case strings.Contains(m, "gpt") || strings.Contains(m, "chatgpt") || strings.Contains(m, "codex") ||
		strings.Contains(m, "o1") || strings.Contains(m, "o3") || strings.Contains(m, "o4"):
		return "openai"
	case p == "anthropic":
		return "anthropic"
	case p == "openai":
		return "openai"
	default:
		return "generic"
	}
}

// modelFamilyStylePrompt returns a short, family-specific working-style block
// appended to the coding prompt. The base prompt is already tuned for Claude, so
// "anthropic" and "generic" add nothing; the other families get guidance that
// nudges them toward the behaviour the base prompt assumes (act decisively, stay
// concise, one tool at a time for small local models).
func modelFamilyStylePrompt(family string) string {
	switch family {
	case "openai":
		return `## Working style for this model

- Be decisive and act. Do not narrate what you are about to do or ask permission for routine steps — take the action (read the file, run the command) and report the result.
- Lead with the action or the answer; keep preamble and self-commentary to a minimum.
- When you have the tools to verify something, verify it rather than asserting it.`
	case "gemini":
		return `## Working style for this model

- Follow the requested output format exactly, and do not restate the task before starting it.
- Be concise and concrete — prefer specific file paths, symbols, and commands over general description.
- Use tools directly to gather facts instead of describing what you intend to do.`
	case "local":
		return `## Working style for this model

- Keep responses short and focused; long, meandering output drifts off task.
- Call exactly ONE tool at a time and wait for its result before deciding the next step.
- Never invent file paths, APIs, function names, or command output — if you are unsure, use a tool to check first.
- Prefer the simplest solution that works over a clever one.`
	default: // "anthropic", "generic", ""
		return ""
	}
}
