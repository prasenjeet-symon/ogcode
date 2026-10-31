package indexer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// textExtensions is the set of file extensions treated as plain text or source code.
// Delimited files (.csv, .tsv) are NOT here — they are spreadsheets (see
// sheet.go), profiled one page per sheet instead of read as one giant dump.
var textExtensions = map[string]struct{}{
	// Plain text / docs
	".txt": {}, ".md": {}, ".rst": {},
	// Web
	".html": {}, ".htm": {}, ".css": {}, ".scss": {}, ".sass": {},
	".xml": {}, ".vue": {}, ".svelte": {},
	// JS / TS ecosystem
	".js": {}, ".ts": {}, ".jsx": {}, ".tsx": {},
	// Systems / compiled
	".go": {}, ".c": {}, ".cpp": {}, ".cc": {}, ".h": {}, ".hpp": {},
	".rs": {}, ".cs": {}, ".java": {}, ".kt": {}, ".swift": {}, ".scala": {},
	// Scripting
	".py": {}, ".rb": {}, ".php": {}, ".sh": {}, ".bash": {}, ".zsh": {},
	// Config / data
	".yaml": {}, ".yml": {}, ".json": {}, ".toml": {}, ".ini": {}, ".sql": {},
	".tf": {}, ".tfvars": {}, ".hcl": {},
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

// IsTextFile reports whether ext (e.g. ".go") is a supported text/code extension.
func IsTextFile(ext string) bool {
	_, ok := textExtensions[strings.ToLower(ext)]
	return ok
}

// ExtractTextFile reads a text or code file and returns it as a single PageText.
// HTML/XML/template files have tags stripped before keyword extraction.
func ExtractTextFile(filePath string) ([]PageText, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", filePath, err)
	}
	text := string(data)
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".html", ".htm", ".xml", ".vue", ".svelte":
		text = htmlTagRe.ReplaceAllString(text, " ")
	}
	return []PageText{{PageNum: 1, Text: text}}, nil
}
