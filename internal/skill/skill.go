// Package skill implements ogcode's lazy-loaded instruction system.
//
// A skill is a directory holding a SKILL.md file: YAML frontmatter that names
// and describes it, and a markdown body carrying the instructions themselves.
// The agent never sees a body unless it asks for one — its system prompt lists
// only names and descriptions, and the "skill" tool pulls one body into context
// on demand. Listing bodies instead would cost the full token weight of every
// skill the agent never uses, re-sent on every step of every turn.
package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// Filename is the file that marks a directory as a skill.
	Filename = "SKILL.md"

	// MaxNameLen bounds the frontmatter name, in characters. The name is a
	// lookup key the model types back verbatim, so it stays short and
	// typo-resistant.
	MaxNameLen = 64
	// MaxDescriptionLen bounds the frontmatter description, in characters.
	// Descriptions are carried in the system prompt for every skill on every
	// step, so a runaway one is clamped rather than paid for.
	MaxDescriptionLen = 1024
	// maxFileSize caps a single SKILL.md. Anything larger is a document, not a
	// set of instructions, and would dominate the context it is loaded into.
	maxFileSize = 256 * 1024
)

// namePattern is the frontmatter name grammar: lowercase alphanumeric segments
// joined by single hyphens. It is deliberately narrow — the name doubles as the
// directory name and as the argument the model passes to the skill tool, and
// case or separator drift between the three is a lookup miss with no error.
var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Source labels where a skill was found, for diagnostics and for the tool's
// output. It has no effect on lookup.
type Source string

const (
	SourceProject  Source = "project"
	SourceGlobal   Source = "global"
	SourceConfig   Source = "config"
	SourceRemote   Source = "remote"
	SourceEmbedded Source = "built-in"
)

// Skill is one parsed SKILL.md.
type Skill struct {
	Name        string
	Description string
	// Dir is the absolute path to the skill's directory. Relative paths inside
	// the body (scripts/, references/) resolve against it, which is why the tool
	// hands it to the model along with the body.
	Dir string
	// Path is the absolute path to the SKILL.md itself. Empty for embedded
	// skills, which have no file.
	Path string
	// Content is the markdown body — everything after the frontmatter.
	Content string
	Source  Source
}

// Parse reads a SKILL.md's bytes and returns the skill it defines. dir is the
// directory holding the file; the frontmatter name must match its base name, so
// that the name in the prompt, the name the model passes to the tool, and the
// directory on disk are always the same string.
func Parse(dir, path string, data []byte) (Skill, error) {
	s, err := parseContent(data)
	if err != nil {
		return Skill{}, err
	}
	if base := filepath.Base(dir); base != s.Name {
		return Skill{}, fmt.Errorf("name %q does not match its directory %q — rename one so they agree", s.Name, base)
	}
	s.Dir = dir
	s.Path = path
	return s, nil
}

// parseContent validates everything a SKILL.md's own bytes can decide: the
// frontmatter shape, the name grammar, the description length. The directory
// match is Parse's job, because a built-in skill has no directory to match
// against and is otherwise held to exactly these rules.
func parseContent(data []byte) (Skill, error) {
	if len(data) > maxFileSize {
		return Skill{}, fmt.Errorf("%s is %d bytes, over the %d byte limit", Filename, len(data), maxFileSize)
	}

	front, body, err := splitFrontmatter(string(data))
	if err != nil {
		return Skill{}, err
	}
	fields := parseFrontmatter(front)

	name := strings.TrimSpace(fields["name"])
	if name == "" {
		return Skill{}, errors.New("frontmatter has no name")
	}
	if n := utf8.RuneCountInString(name); n > MaxNameLen {
		return Skill{}, fmt.Errorf("name %q is %d chars, over the %d limit", name, n, MaxNameLen)
	}
	if !namePattern.MatchString(name) {
		return Skill{}, fmt.Errorf("name %q must be lowercase alphanumeric with single hyphens (e.g. git-release)", name)
	}

	// Over-long descriptions are clamped, not rejected. The description only
	// feeds the prompt listing; dropping an otherwise valid skill over its
	// length would cost the user the skill to fix a formatting problem.
	//
	// Cut on a rune boundary. A byte slice through a multi-byte character would
	// put invalid UTF-8 straight into the system prompt, which is a worse
	// outcome than the long description it was trying to avoid.
	desc := collapseSpace(fields["description"])
	if runes := []rune(desc); len(runes) > MaxDescriptionLen {
		desc = strings.TrimSpace(string(runes[:MaxDescriptionLen])) + "…"
	}

	return Skill{
		Name:        name,
		Description: desc,
		Content:     strings.TrimSpace(body),
	}, nil
}

// Load reads and parses the SKILL.md at path.
func Load(path string) (Skill, error) {
	// Checked before the read, not only in parseContent: a file that is
	// pathologically large — a log or a dataset that ended up named SKILL.md —
	// would otherwise be pulled into memory in full just to be rejected.
	if info, err := os.Stat(path); err == nil && info.Size() > maxFileSize {
		return Skill{}, fmt.Errorf("%s is %d bytes, over the %d byte limit", Filename, info.Size(), maxFileSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	return Parse(filepath.Dir(path), path, data)
}

// splitFrontmatter separates the leading YAML frontmatter block from the
// markdown body. The file must open with a "---" fence; a SKILL.md without one
// has no name, and a skill with no name cannot be listed or looked up.
func splitFrontmatter(text string) (front, body string, err error) {
	// Strip a UTF-8 BOM: editors add it invisibly and it would otherwise make
	// the first line start with U+FEFF, which matches no fence.
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")

	lines := strings.Split(text, "\n")
	// Trailing whitespace on the fence is invisible in an editor and would
	// otherwise cost the user the whole skill. The closing fence below has
	// always tolerated it; the opening one now matches.
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return "", "", errors.New("missing YAML frontmatter — the file must start with a --- line")
	}
	for i := 1; i < len(lines); i++ {
		if t := strings.TrimRight(lines[i], " \t"); t == "---" || t == "..." {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), nil
		}
	}
	return "", "", errors.New("unterminated YAML frontmatter — no closing --- line")
}

// parseFrontmatter reads the top-level scalar fields of a YAML frontmatter
// block. It is a deliberate subset, not a YAML implementation: ogcode has no
// YAML dependency, and the only fields that carry meaning here are two strings.
//
// Handled: "key: value" (optionally quoted), and block scalars "key: |" and
// "key: >". Nested maps (metadata:) and sequences are skipped rather than
// modeled — they parse to an empty value and are simply not returned.
// Inline "# comment" suffixes are NOT stripped, because a description is prose
// and a bare # in it is far more likely than a trailing comment.
func parseFrontmatter(front string) map[string]string {
	fields := map[string]string{}
	lines := strings.Split(front, "\n")

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Indented lines and sequence entries belong to a structure the caller
		// does not model; the block-scalar branch below consumes the ones that
		// are actually a value.
		if line[0] == ' ' || line[0] == '\t' || line[0] == '-' {
			continue
		}
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		rest = strings.TrimSpace(rest)

		if rest != "" && !isBlockIndicator(rest) {
			fields[key] = unquote(rest)
			continue
		}

		block, next := collectIndented(lines, i+1)
		i = next - 1
		switch {
		case strings.HasPrefix(rest, "|"): // literal: newlines preserved
			fields[key] = strings.Join(block, "\n")
		case strings.HasPrefix(rest, ">"): // folded: newlines become spaces
			fields[key] = strings.Join(block, " ")
		default:
			// A bare "key:" followed by indented lines is a nested map, which
			// this parser does not model. Recorded as empty so a later "key:"
			// cannot be confused with it.
			fields[key] = ""
		}
	}
	return fields
}

// isBlockIndicator reports whether a YAML value position holds a block scalar
// header (|, >, and their chomping variants) rather than an inline value.
func isBlockIndicator(s string) bool {
	if s == "" {
		return false
	}
	if s[0] != '|' && s[0] != '>' {
		return false
	}
	return strings.Trim(s[1:], "+-0123456789") == ""
}

// collectIndented returns the indented lines starting at index start, stripped
// of their common leading whitespace, plus the index of the first line that
// ends the block.
func collectIndented(lines []string, start int) ([]string, int) {
	var block []string
	i := start
	for ; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			// A blank line inside a block belongs to it; a trailing one does not
			// survive the TrimSpace on the assembled value either way.
			block = append(block, "")
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			break
		}
		block = append(block, strings.TrimSpace(line))
	}
	// Drop trailing blanks so a block followed by a blank line does not end in
	// stray separators once joined.
	for len(block) > 0 && block[len(block)-1] == "" {
		block = block[:len(block)-1]
	}
	return block, i
}

// unquote strips matching surrounding quotes from a scalar and unescapes the
// handful of sequences a double-quoted YAML scalar can carry.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		r := strings.NewReplacer(`\"`, `"`, `\n`, "\n", `\t`, "\t", `\\`, `\`)
		return r.Replace(s[1 : len(s)-1])
	}
	return s
}

// collapseSpace flattens a description to a single line. Descriptions are
// rendered inside an XML element in the system prompt, where an embedded
// newline breaks the one-line-per-field shape the block otherwise holds.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
