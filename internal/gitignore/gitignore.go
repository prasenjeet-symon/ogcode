// Package gitignore matches paths against .gitignore rules.
//
// It exists so the indexer can skip what the repository already declares as
// noise. A .gitignore is the one place a project has already written down which
// files are generated, vendored or private, and re-deriving that list as
// indexer excludes means maintaining the same knowledge twice — badly, since
// only one of the two copies is under review.
//
// The implementation follows gitignore(5) rather than approximating it, because
// the approximations fail in the direction that matters: a pattern silently not
// matching indexes a build directory, and the person who wrote the pattern has
// no reason to suspect it. What is deliberately not implemented is everything
// outside the working tree — the global core.excludesFile, .git/info/exclude,
// and the index itself. A file tracked by git before it was ignored is still
// ignored here, because the file on disk is what this package can see and what
// the user pointed at.
package gitignore

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// gitDir is the repository's own metadata directory.
//
// It is excluded as part of implementing git's semantics, not as a policy of
// this package's own. Git does not treat .git as part of the working tree at
// all — it is never tracked, never reported by check-ignore, and never a
// candidate for a rule to apply to — so a matcher that let callers walk into it
// would be modelling git wrongly rather than modelling it strictly.
const gitDir = ".git"

// Matcher answers whether a path is ignored, consulting every .gitignore
// between the root and the path itself.
//
// Files are read lazily and cached, so a walk pays for each .gitignore once
// however many paths it covers.
type Matcher struct {
	root string

	mu    sync.Mutex
	cache map[string][]*rule // directory (relative to root, "" for root) → its rules
}

// New returns a Matcher rooted at dir. No files are read until the first match.
func New(root string) *Matcher {
	return &Matcher{root: filepath.Clean(root), cache: map[string][]*rule{}}
}

// Match reports whether path is ignored. path may be absolute or relative to
// the root; isDir must say whether it names a directory, because a pattern
// ending in "/" matches only directories.
//
// The rules of precedence are git's: within one file the last matching pattern
// decides, and a .gitignore deeper in the tree overrides a shallower one. So the
// search runs outermost-first and keeps the last verdict rather than stopping
// at the first — which is what lets a nested file re-include something its
// parent excluded.
func (m *Matcher) Match(path string, isDir bool) bool {
	rel, ok := m.relative(path)
	if !ok || rel == "." {
		return false
	}

	// The directories from the root down to the one holding this path. Each may
	// carry a .gitignore, and each sees the path relative to itself.
	segments := strings.Split(rel, "/")

	// The repository's own metadata is outside the working tree entirely, so no
	// rule decides it and none can re-include it.
	for _, seg := range segments {
		if seg == gitDir {
			return true
		}
	}

	ignored := false
	for i := 0; i < len(segments); i++ {
		dir := strings.Join(segments[:i], "/")
		sub := strings.Join(segments[i:], "/")
		for _, r := range m.rulesFor(dir) {
			if r.match(sub, isDir) {
				ignored = !r.negate
			}
		}
	}
	return ignored
}

// relative converts a path to a slash-separated path relative to the root, and
// reports whether it lies under the root at all.
func (m *Matcher) relative(path string) (string, bool) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.root, path)
	}
	rel, err := filepath.Rel(m.root, filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// rulesFor returns the compiled rules of the .gitignore in dir, reading it on
// first use. A directory without one caches an empty result, so a walk through
// a deep tree does not stat the same missing file repeatedly.
func (m *Matcher) rulesFor(dir string) []*rule {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rules, ok := m.cache[dir]; ok {
		return rules
	}
	rules := parseFile(filepath.Join(m.root, filepath.FromSlash(dir), ".gitignore"))
	m.cache[dir] = rules
	return rules
}

// rule is one compiled pattern line.
type rule struct {
	negate  bool
	dirOnly bool
	// re matches the pattern itself and anything beneath it.
	re *regexp.Regexp
	// under matches only what lies strictly beneath the pattern, and is set for
	// a directory-only rule. Such a rule does not match a file of the same name
	// — "build/" leaves a file called "build" alone — but it does match every
	// file inside the directory, and those arrive with isDir false.
	under *regexp.Regexp
}

// match reports whether rel — a path relative to the directory holding this
// rule's .gitignore — is covered by it.
func (r *rule) match(rel string, isDir bool) bool {
	if r.dirOnly && !isDir {
		return r.under != nil && r.under.MatchString(rel)
	}
	return r.re.MatchString(rel)
}

// parseFile reads a .gitignore and compiles every usable line. A missing file
// is not an error: most directories have none.
func parseFile(path string) []*rule {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var rules []*rule
	scanner := bufio.NewScanner(f)
	// Some generated .gitignore files carry very long lines; the default 64 KiB
	// token limit would end the scan early and silently drop the rest of the file.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if r := compile(scanner.Text()); r != nil {
			rules = append(rules, r)
		}
	}
	return rules
}

// compile turns one .gitignore line into a rule, or nil where the line declares
// nothing — a blank, or a comment.
func compile(line string) *rule {
	// A line's trailing whitespace is not part of the pattern unless escaped,
	// which is how a pattern for a filename that really ends in a space is
	// written. Leading whitespace is significant and stays.
	line = trimUnescapedTrailingSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}

	r := &rule{}
	if strings.HasPrefix(line, "!") {
		r.negate = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\!`) || strings.HasPrefix(line, `\#`) {
		// A leading "!" or "#" that is meant literally is escaped.
		line = line[1:]
	}
	if line == "" {
		return nil
	}

	if strings.HasSuffix(line, "/") {
		r.dirOnly = true
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			return nil
		}
	}

	// A slash anywhere but the end anchors the pattern to the directory holding
	// the .gitignore. Without one it floats: "build" matches a build directory
	// at any depth below that file, which is the behaviour most patterns rely on.
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")

	expr := translate(line)
	if !anchored {
		expr = `(?:^|.*/)` + expr
	} else {
		expr = `^` + expr
	}
	re, err := regexp.Compile(expr + `(?:/.*)?$`)
	if err != nil {
		// A pattern this package cannot express is better dropped than applied
		// wrongly: over-matching would hide files the user never excluded.
		return nil
	}
	r.re = re
	if r.dirOnly {
		under, err := regexp.Compile(expr + `/.*$`)
		if err != nil {
			return nil
		}
		r.under = under
	}
	return r
}

// translate converts gitignore wildcard syntax into a regular expression over a
// slash-separated path.
//
// The distinction that carries the format is between "*" and "**": a single
// star stops at a separator and a double star crosses them, which is what makes
// "a/*/c" one level deep and "a/**/c" any number.
func translate(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		switch c := p[i]; c {
		case '*':
			if i+1 < len(p) && p[i+1] == '*' {
				i++
				switch {
				case i+1 < len(p) && p[i+1] == '/':
					// "**/" — zero or more leading directories.
					i++
					b.WriteString(`(?:.*/)?`)
				case i == len(p)-1:
					// A trailing "**" matches everything below.
					b.WriteString(`.*`)
				default:
					b.WriteString(`.*`)
				}
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '[':
			if class, next, ok := bracket(p, i); ok {
				b.WriteString(class)
				i = next
			} else {
				b.WriteString(regexp.QuoteMeta("["))
			}
		case '\\':
			// An escape takes the next character literally.
			if i+1 < len(p) {
				i++
				b.WriteString(regexp.QuoteMeta(string(p[i])))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}

// bracket copies a [...] character class through to the regexp, returning the
// index of its closing bracket. Only the negation marker differs between the
// two syntaxes: gitignore writes "[!abc]" where a regexp writes "[^abc]".
func bracket(p string, start int) (string, int, bool) {
	i := start + 1
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		i++
	}
	// A "]" immediately after the opening bracket is a literal.
	if i < len(p) && p[i] == ']' {
		i++
	}
	for ; i < len(p); i++ {
		if p[i] == ']' {
			body := p[start+1 : i]
			if strings.HasPrefix(body, "!") {
				body = "^" + body[1:]
			}
			return "[" + body + "]", i, true
		}
	}
	return "", start, false // unterminated: treat the "[" as literal
}

// AddPattern appends pattern to the .gitignore in dir if one exists and the
// pattern is not already present. It returns true when the file was changed.
//
// If dir has no .gitignore, nothing happens and false is returned: ogcode does
// not create a .gitignore on its own — it only extends one the project already
// keeps. A project with no .gitignore is left that way.
func AddPattern(dir, pattern string) bool {
	path := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil {
		// No file (or unreadable): never create one ourselves.
		return false
	}
	if patternAlreadyPresent(data, pattern) {
		return false
	}
	// Ensure the file ends in a newline before appending, so the new line
	// does not glom onto whatever the last line was.
	body := string(data)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		body += "\n"
	}
	body += pattern + "\n"
	return os.WriteFile(path, []byte(body), 0o644) == nil
}

// patternAlreadyPresent reports whether pattern already appears as its own
// line in data, so AddPattern is idempotent. It scans line by line, trimming a
// trailing newline and a trailing carriage return (for CRLF .gitignores),
// and compares the trimmed line to pattern.
func patternAlreadyPresent(data []byte, pattern string) bool {
	for len(data) > 0 {
		nl := indexByte(data, '\n')
		var line []byte
		if nl < 0 {
			line, data = data, nil
		} else {
			line, data = data[:nl], data[nl+1:]
		}
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if string(line) == pattern {
			return true
		}
	}
	return false
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// trimUnescapedTrailingSpace removes trailing spaces that are not escaped.
func trimUnescapedTrailingSpace(s string) string {
	end := len(s)
	for end > 0 && (s[end-1] == ' ' || s[end-1] == '\t') {
		// A space preceded by an odd number of backslashes is escaped, so the
		// pattern really does end in one and the trim stops here.
		slashes := 0
		for j := end - 2; j >= 0 && s[j] == '\\'; j-- {
			slashes++
		}
		if slashes%2 == 1 {
			break
		}
		end--
	}
	return s[:end]
}
