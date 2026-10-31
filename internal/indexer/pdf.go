package indexer

import (
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/ledongthuc/pdf"
)

// PageText holds the raw extracted text for a single PDF page.
type PageText struct {
	PageNum int
	Text    string
	// Sheet marks pages extracted from a spreadsheet profile (ExtractSheetPages).
	// Besides the usual word keywords, the profile's distinctive VALUES — row
	// counts, min/max extremes, ISO dates, decimal amounts — join the keyword
	// corpus, so two sheets with identical generic column names still differ
	// by their values when the label agent names them.
	Sheet bool
}

// PageCorpus holds the deduplicated keyword corpus for a single PDF page.
type PageCorpus struct {
	PageNum  int
	Keywords []string
}

// ExtractPages reads all pages from the PDF at pdfPath in parallel and returns
// a slice of PageText sorted by page number.
func ExtractPages(pdfPath string) ([]PageText, error) {
	f, reader, err := pdf.Open(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("open pdf %s: %w", pdfPath, err)
	}
	defer f.Close()

	numPages := reader.NumPage()
	if numPages == 0 {
		return nil, nil
	}

	results := make([]PageText, numPages)
	errs := make([]error, numPages)

	var wg sync.WaitGroup
	for i := 1; i <= numPages; i++ {
		wg.Add(1)
		go func(pageNum int) {
			defer wg.Done()
			p := reader.Page(pageNum)
			if p.V.IsNull() {
				results[pageNum-1] = PageText{PageNum: pageNum, Text: ""}
				return
			}
			// Build a font cache for this page to avoid repeated charmap parsing.
			fonts := make(map[string]*pdf.Font)
			for _, name := range p.Fonts() {
				f := p.Font(name)
				fonts[name] = &f
			}
			text, err := p.GetPlainText(fonts)
			if err != nil {
				errs[pageNum-1] = fmt.Errorf("page %d: %w", pageNum, err)
				return
			}
			results[pageNum-1] = PageText{PageNum: pageNum, Text: text}
		}(i)
	}
	wg.Wait()

	// Collect any errors (report first one).
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}

	return results, nil
}

// BuildCorpora converts page texts to keyword corpora in parallel.
// Each corpus is deduplicated, lowercased, and stripped of stop words and
// non-alphabetic tokens; sheet pages additionally carry their profile's
// numeric/date values (see PageText.Sheet and extractSheetValueKeywords).
func BuildCorpora(pages []PageText) []PageCorpus {
	corpora := make([]PageCorpus, len(pages))

	var wg sync.WaitGroup
	for i, pt := range pages {
		wg.Add(1)
		go func(idx int, p PageText) {
			defer wg.Done()
			keywords := extractKeywords(p.Text)
			if p.Sheet {
				keywords = append(keywords, extractSheetValueKeywords(p.Text)...)
			}
			corpora[idx] = PageCorpus{
				PageNum:  p.PageNum,
				Keywords: keywords,
			}
		}(i, pt)
	}
	wg.Wait()

	return corpora
}

// extractKeywords splits text into tokens, expands camelCase identifiers,
// removes stop words and short tokens, and deduplicates the result.
func extractKeywords(text string) []string {
	seen := make(map[string]struct{})
	var keywords []string

	for _, token := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r)
	}) {
		for _, word := range splitCamelCase(token) {
			if len(word) < 2 {
				continue
			}
			if isStopWord(word) {
				continue
			}
			if _, dup := seen[word]; dup {
				continue
			}
			seen[word] = struct{}{}
			keywords = append(keywords, word)
		}
	}

	return keywords
}

// splitCamelCase splits a camelCase or PascalCase token into lowercase parts.
// Non-camelCase tokens are returned as a single lowercase element.
// e.g. "getUserName" → ["get", "user", "name"], "simple" → ["simple"]
func splitCamelCase(word string) []string {
	runes := []rune(word)
	if len(runes) == 0 {
		return nil
	}
	var parts []string
	start := 0
	for i := 1; i < len(runes); i++ {
		if unicode.IsUpper(runes[i]) {
			parts = append(parts, strings.ToLower(string(runes[start:i])))
			start = i
		}
	}
	parts = append(parts, strings.ToLower(string(runes[start:])))
	return parts
}
