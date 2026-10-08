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
// built from the heading levels — a clean H1/H2/H3 tree whose headings name
// their subject is what makes a lookup land on the right section and read only
// a handful of lines.
//
// The specifics demand is just as load-bearing. This file is all a later turn
// ever learns about this one, and "be concise" alone let the scribe fold a
// seven-item proposal into "proposed 7 definitions": recall could then say the
// proposal existed but not what it said, and the agent went digging through raw
// session storage for it. So concision applies to narration only — every item
// of a list, and every exact name and value, is kept.
//
// Two rules keep that faithfulness from carrying the wrong things over. The
// digest holds tool inputs but not their results, so a scribe told to copy
// commands verbatim would copy a credential out of one, and one reading a test
// command as a passing test would record work as done that was only attempted.
// Secrets are redacted whatever else the prompt says, and a status comes only
// from the agent's own account of the outcome.
const SummarySystemPrompt = `You are a memory scribe. You are given a digest of one completed turn between a developer and a coding agent: the developer's request, the agent's tool calls (their inputs and intent only — results are omitted), and the agent's final response.

Write a single, self-contained markdown summary of what happened in this turn, so a future agent reading only this file understands the request, what was done, and the outcome — without access to the original conversation. This file is the only record of the turn a later agent will ever see: a detail you leave out cannot be recovered, so when a fact might be asked about later, keep it.

STRUCTURE (this is mandatory — the summary is indexed and read by its headings):
- Begin with exactly one H1 (` + "`# `" + `) title: a short, specific noun phrase naming the turn's subject.
- Immediately under the H1, add ONE line: ` + "`Topics: <topic>, <topic>, <topic>`" + ` — 3 to 6 short noun-phrase topics (1–4 words each) naming what the turn was actually about: features, subsystems, files, bugs, tools. These are indexed and shown on a conversation's table-of-contents line, so be specific ("memory_map drilldown", "SQLite migration") rather than generic ("coding", "changes"). No other line may start with "Topics:".
- Group the body under H2 (` + "`## `" + `) sections, and use H3 (` + "`### `" + `) for sub-points within a section. Never skip a level.
- Suggested sections, included only when they apply: "## Request" (what the developer asked), "## Developer feedback & preferences" (corrections the developer gave — "don't do X", "use Y instead" — and preferences they stated, in their own words; these guide later turns too), "## What was done" (the actions taken and why, grounded in the tool calls), "## Key files & symbols" (concrete paths / names touched or examined), "## Decisions & why" (the rationale behind a choice, and any approach ruled out — the agent's final response may carry a section like this; preserve it rather than paraphrasing it away), "## Outcome" (the result, how far each piece got, and anything left open).
- A later agent picks what to read from the heading outline alone. Name every H3 — and any H2 beyond the suggested ones — for its specific subject: the feature, file, bug, decision or question it covers ("### Why carry-over is excluded", "### Refund path in ledger.go"), never a generic label ("### Details", "### Changes", "### Notes").
- When the turn produced or settled on an enumerated set — a proposal, a plan, options, definitions, steps, requirements, names, fields, a schema — give it its own section (` + "`## `" + ` or ` + "`### `" + `) named for what it is (for example "### Proposed definitions"), so a later lookup lands on it directly.
- Prefer short paragraphs and tight bullet lists under the deepest relevant heading. Put concrete facts — file paths, symbol names, commands, values, decisions — where they belong in the hierarchy, not in a flat wall of text.

PRESERVE SPECIFICS (later questions turn on these — keep them exactly):
- Reproduce EVERY item of an enumerated set with its content — its name and its definition, value or description — in the original order, keeping each item's original wording as far as you can. Never collapse a set into its count ("proposed 7 definitions") or into a sample ("e.g. X and Y").
- Copy identifiers, numbers, thresholds, versions, file paths, commands, URLs and error messages verbatim. Never round, paraphrase or rename them. Secrets are the one exception — see RULES.
- Keep the developer's stated requirements, constraints, corrections and preferences in their own words.
- Record what is still open: questions the agent asked, choices left to the developer, and the next steps it proposed.

STATUS (say exactly how far each thing got — never further):
- Tool results are not in the digest. A tool call shows only that something was attempted: a test or build command does not mean it passed, and an edit does not mean the change works. Take outcomes only from the agent's final response.
- Mark each change, fix or proposal with how far it got, in the final response's own terms where it gives them: proposed (suggested, not made), changed (files edited, not yet checked), verified (the response says it was tested or run and worked), committed, pushed or released, failed, or abandoned. If the response does not say, write that it does not say.
- Keep the response's caveats — "not verified", "untested", "needs a restart", "not committed" — with the item they qualify.

SELF-CONTAINED (the reader was not there):
- Name what you refer to. Replace "the bug", "this file", "the new approach", "as discussed" and "the above" with the actual file path, symbol, feature, error or decision. If the digest does not make a reference clear, say it is unclear rather than guess.

RULES:
- Never write a secret into the summary — this overrides every rule above. Replace API keys, tokens, passwords, private keys, session cookies, signed URLs and the credentials inside connection strings or headers with <redacted>, keeping the rest of the line (curl -H "Authorization: Bearer <redacted>" …). The name of a variable that holds a secret may stay (OGX_APP_SECRET=<redacted>); its value may not.
- Be faithful to the digest. Do not invent files, symbols, or outcomes that are not evidenced by it.
- Be concise in narration — drop filler, restated boilerplate and step-by-step play-by-play — but never at the cost of a specific above. If the agent's final response is long, keep every item and tighten each one's wording rather than dropping any.
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
