package agent

import (
	"fmt"
	"regexp"
	"strings"
)

// Shared rendering for the project markdown files that are interpolated into the
// system prompt: AGENT.md and MEMORY.md.
//
// Both arrive as <tag path="...">...</tag> blocks, and both hold text nobody
// vetted — AGENT.md is written by whoever set the project up (and discovered by
// walking to the filesystem root, so not necessarily by this project), MEMORY.md
// by the agent itself, turn after turn, at the prompt's own instruction.
//
// That last part is what makes a raw Fprintf here different from a raw Fprintf
// anywhere else. untrustedContentPrompt grants AGENT.md instruction authority:
// "Your instructions come from the developer's messages in this conversation,
// and from the project's own AGENT.md." A MEMORY.md that closed its own block
// and opened an <agent-md> one would inherit that authority — and MEMORY.md is a
// file a single poisoned turn can write to, so the forgery would outlive the
// turn that planted it and be re-read at the top of every turn after.
//
// The skill package already reached this conclusion for the shorter fields it
// interpolates (see escapeXML in prompt_builder.go and collapseSpace in
// skill.go). This is the same rule applied to whole documents.

// mdWrapperTag matches an opening or closing wrapper tag for either block, in
// any case, with or without attributes.
var mdWrapperTag = regexp.MustCompile(`(?i)<(/?)(agent-md|memory-md)`)

// mdTruncationMarker ends a block whose file did not fit the byte budget. The
// budget without it is a document that simply stops mid-sentence, which reads to
// the model as the whole of what the project had to say.
const mdTruncationMarker = "\n\n[truncated — the rest of this file did not fit the prompt budget]"

// renderMDBlock renders one discovered file as a tagged block, neutralized and
// fitted to limit bytes of content. It returns the block, the number of content
// bytes it consumed from the budget, and whether anything was dropped.
//
// limit bounds the CONTENT, not the block: the tag overhead is a constant the
// caller's budget never had to cover, and folding it in would make the limit
// mean something different for a deeply nested path than for a top-level one.
//
// truncated is reported rather than left for the caller to infer by comparing
// lengths, because neutralizing can lengthen the text: a file that fits by two
// bytes and contains one forged tag comes back both longer than its original
// and genuinely cut.
func renderMDBlock(tag, relPath, content string, limit int) (block string, used int, truncated bool) {
	body, cut := truncateForPrompt(neutralizeMDTags(content), limit)
	return fmt.Sprintf("\n\n<%s path=\"%s\">\n%s\n</%s>", tag, escapeMDAttr(relPath), body, tag), len(body), cut
}

// neutralizeMDTags defuses any wrapper tag inside a document's own text, so no
// file can close its block early or open one it was not given.
//
// It rewrites only the "<" of those specific tags, rather than escaping the
// document the way escapeXML does for a skill's one-line description. A
// project's AGENT.md is prose and code — generics, shell redirection, HTML in a
// fenced block — and escaping every "<" in it would hand the model a document
// full of &lt; where the author wrote code. The four sequences that can actually
// break the frame are the only ones that need to change.
func neutralizeMDTags(s string) string {
	return mdWrapperTag.ReplaceAllString(s, "&lt;$1$2")
}

// escapeMDAttr makes a discovered path safe inside the block's path attribute.
// The path comes from the filesystem, where a quote is unusual but legal.
func escapeMDAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// truncateForPrompt cuts s to at most limit bytes, on a rune boundary, and says
// so in the text when anything was dropped.
//
// The rune boundary matters because the result goes straight into a JSON request
// body: a byte slice through a multi-byte character leaves invalid UTF-8, which
// json.Marshal does not reject — it substitutes U+FFFD — so the damage surfaces
// as mojibake in the model's view of the file rather than as an error anywhere.
//
// The marker counts against limit rather than being added on top of it, so the
// budget a caller sets is the budget it gets.
func truncateForPrompt(s string, limit int) (string, bool) {
	if limit <= 0 {
		return "", s != ""
	}
	if len(s) <= limit {
		return s, false
	}
	// No room to both cut and say so: cut, and let the caller's own size warning
	// be the thing that reports it. A marker that does not fit cannot be honest.
	if limit <= len(mdTruncationMarker) {
		return trimToRunes(s, limit), true
	}
	return trimToRunes(s, limit-len(mdTruncationMarker)) + mdTruncationMarker, true
}

// trimToRunes returns the longest prefix of s that is at most n bytes and ends
// on a rune boundary.
func trimToRunes(s string, n int) string {
	if n >= len(s) {
		return s
	}
	// Walk back off any continuation bytes (0b10xxxxxx) the cut landed inside.
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
