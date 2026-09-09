package memfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SummarySystemPrompt instructs the synthesis LLM to turn a turn's digest into a
// tightly-structured markdown summary. The hierarchy demand is not cosmetic: the
// recall agent navigates these files through the file-map outline, which is
// built from the heading levels — a clean H1/H2/H3 tree is what makes a lookup
// land on the right section and read only a handful of lines.
const SummarySystemPrompt = `You are a memory scribe. You are given a digest of one completed turn between a developer and a coding agent: the developer's request, the agent's tool calls (their inputs and intent only — results are omitted), and the agent's final response.

Write a single, self-contained markdown summary of what happened in this turn, so a future agent reading only this file understands the request, what was done, and the outcome — without access to the original conversation.

STRUCTURE (this is mandatory — the summary is indexed and read by its headings):
- Begin with exactly one H1 (` + "`# `" + `) title: a short, specific noun phrase naming the turn's subject.
- Group the body under H2 (` + "`## `" + `) sections, and use H3 (` + "`### `" + `) for sub-points within a section. Never skip a level.
- Suggested sections, included only when they apply: "## Request" (what the developer asked), "## What was done" (the actions taken and why, grounded in the tool calls), "## Key files & symbols" (concrete paths / names touched or examined), "## Outcome" (the result, decisions made, and anything left open).
- Prefer short paragraphs and tight bullet lists under the deepest relevant heading. Put concrete facts — file paths, symbol names, commands, values, decisions — where they belong in the hierarchy, not in a flat wall of text.

RULES:
- Be faithful to the digest. Do not invent files, symbols, or outcomes that are not evidenced by it.
- Be concise. Capture what matters for later recall; omit filler and restated boilerplate.
- Output ONLY the markdown summary, starting with the ` + "`#`" + ` title. No preamble, no code fence around the whole thing, no trailing commentary.`

// Write persists a turn summary as a dated markdown file under the project's
// .ogcode/memory/ folder and returns its absolute path. body is the synthesized
// markdown (starting with its own H1); Write prepends YAML frontmatter carrying
// the Meta so recall can scope/attribute the file, and picks a collision-free
// filename.
func Write(projectDir string, meta Meta, body string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("memfile: empty summary body")
	}
	dir := MemoryDir(projectDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("memfile: create memory dir: %w", err)
	}

	created := meta.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	path := uniquePath(dir, Filename(created, meta.SessionID, meta.Title))

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlString(meta.Title))
	fmt.Fprintf(&b, "session_id: %s\n", yamlString(meta.SessionID))
	fmt.Fprintf(&b, "session_type: %s\n", yamlString(meta.SessionType))
	fmt.Fprintf(&b, "project_id: %s\n", yamlString(meta.ProjectID))
	fmt.Fprintf(&b, "created_at: %s\n", created.UTC().Format(time.RFC3339))
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n")

	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("memfile: write summary: %w", err)
	}
	return path, nil
}

// uniquePath returns base under dir, or base with a numeric suffix if a file of
// that name already exists — two turns can complete within the same second.
func uniquePath(dir, base string) string {
	path := filepath.Join(dir, base)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; i < 1000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path // give up de-duping; overwrite rather than loop forever
}

// yamlString renders a value as a safe double-quoted YAML scalar.
func yamlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", " ")
	return `"` + s + `"`
}
