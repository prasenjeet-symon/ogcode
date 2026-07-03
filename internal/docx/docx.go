package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// WordsPerPage is the target word count per pseudo-page when a DOCX has no
// explicit page breaks. Paragraphs are accumulated until this threshold is
// reached, then a new pseudo-page starts. This keeps each pseudo-page's keyword
// corpus comparable to a PDF page.
const WordsPerPage = 500

// PageText holds the raw extracted text for a single DOCX pseudo-page.
type PageText struct {
	PageNum int
	Text    string
}

// ExtractPages opens the DOCX (ZIP) archive at docxPath, parses
// word/document.xml, extracts paragraph text, and groups paragraphs into
// pseudo-pages. Explicit page breaks (<w:br w:type="page"/>) always start a new
// pseudo-page; otherwise paragraphs are chunked by word count (WordsPerPage).
func ExtractPages(docxPath string) ([]PageText, error) {
	zr, err := zip.OpenReader(docxPath)
	if err != nil {
		return nil, fmt.Errorf("open docx %s: %w", docxPath, err)
	}
	defer zr.Close()

	var docFile *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return nil, fmt.Errorf("docx %s: word/document.xml not found", docxPath)
	}

	rc, err := docFile.Open()
	if err != nil {
		return nil, fmt.Errorf("open word/document.xml: %w", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read word/document.xml: %w", err)
	}

	paragraphs, err := parseBody(data)
	if err != nil {
		return nil, fmt.Errorf("parse document.xml: %w", err)
	}

	return groupPages(paragraphs), nil
}

// paragraph holds the text of a single <w:p> element and whether it forces a
// page break (contains <w:br w:type="page"/>).
type paragraph struct {
	text      string
	pageBreak bool
}

// parseBody decodes the OOXML body and returns a flat slice of paragraphs.
func parseBody(data []byte) ([]paragraph, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var paragraphs []paragraph

	var cur paragraph
	var inRun bool
	var inText bool

	// flushCur appends the current paragraph and resets it.
	flushCur := func() {
		paragraphs = append(paragraphs, cur)
		cur = paragraph{}
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			local := t.Name.Local
			switch local {
			case "p":
				cur = paragraph{}
			case "r":
				inRun = true
			case "t":
				inText = true
			case "br":
				// Check for w:type="page" attribute.
				isPageBreak := false
				for _, attr := range t.Attr {
					if attr.Name.Local == "type" && attr.Value == "page" {
						isPageBreak = true
					}
				}
				if isPageBreak {
					// Text accumulated so far belongs to the current page.
					// Mark this paragraph as a page-break terminator, then start
					// a fresh paragraph so any subsequent <w:t> content in the
					// same <w:p> goes to the next page.
					cur.pageBreak = true
					flushCur()
					// Preserve inRun state — we are still inside the same <w:r>.
				}
			case "tab":
				if inRun {
					cur.text += "\t"
				}
			}

		case xml.EndElement:
			local := t.Name.Local
			switch local {
			case "p":
				flushCur()
			case "r":
				inRun = false
			case "t":
				inText = false
			}

		case xml.CharData:
			if inText {
				cur.text += string(t)
			}
		}
	}

	return paragraphs, nil
}

// groupPages converts the flat paragraph list into pseudo-pages. An explicit
// page break starts a new page immediately. Otherwise, paragraphs accumulate
// until the word count reaches WordsPerPage, at which point a new page begins.
// A page break paragraph itself is included in the page it terminates (i.e. the
// break ends the current page; the paragraph belongs to the current page, and
// the next paragraph starts a new one).
func groupPages(paragraphs []paragraph) []PageText {
	if len(paragraphs) == 0 {
		return nil
	}

	var pages []PageText
	var sb strings.Builder
	wordCount := 0
	pageNum := 1

	flush := func() {
		text := strings.TrimSpace(sb.String())
		if text == "" && len(pages) == 0 {
			// Don't emit a leading empty page.
			return
		}
		pages = append(pages, PageText{PageNum: pageNum, Text: text})
		pageNum++
		sb.Reset()
		wordCount = 0
	}

	for _, p := range paragraphs {
		sb.WriteString(p.text)
		sb.WriteString("\n")
		wordCount += len(strings.Fields(p.text))

		if p.pageBreak {
			flush()
			continue
		}
		if wordCount >= WordsPerPage {
			flush()
		}
	}

	// Flush any remaining content.
	if strings.TrimSpace(sb.String()) != "" {
		flush()
	}

	if len(pages) == 0 {
		// Edge case: all paragraphs were empty but we still had content.
		pages = append(pages, PageText{PageNum: 1, Text: ""})
	}

	return pages
}