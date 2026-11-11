package docx

import (
	"archive/zip"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

// wrapBody wraps body XML in a minimal word/document.xml.
func wrapBody(body string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>` + body + `</w:body>
</w:document>`
}

// createTestDocxFiles writes a DOCX archive holding every part in files, keyed
// by the part's path inside the archive (e.g. "word/footnotes.xml").
func createTestDocxFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "test*.docx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	w := zip.NewWriter(tmpFile)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	return tmpFile.Name()
}

func createTestDocx(t *testing.T, docXML string) string {
	t.Helper()
	return createTestDocxFiles(t, map[string]string{"word/document.xml": docXML})
}

func TestExtractPages_PageBreak(t *testing.T) {
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>First page paragraph about golang concurrency.</w:t></w:r></w:p>
<w:p><w:r><w:br w:type="page"/><w:t>Second page paragraph about database optimization.</w:t></w:r></w:p>
<w:p><w:r><w:t>More second page content.</w:t></w:r></w:p>
</w:body>
</w:document>`

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "golang concurrency") {
		t.Errorf("page 1 should contain first paragraph, got: %s", pages[0].Text)
	}
	if !strings.Contains(pages[1].Text, "database optimization") {
		t.Errorf("page 2 should contain second paragraph, got: %s", pages[1].Text)
	}
	if strings.Contains(pages[0].Text, "database optimization") {
		t.Error("page 1 should not contain second page content")
	}
}

func TestExtractPages_WordCountChunking(t *testing.T) {
	// Generate enough text to span multiple pages without explicit page breaks.
	// Each paragraph has ~40 words; 20 paragraphs = ~800 words → 2 pages.
	var paragraphs []string
	for i := 0; i < 20; i++ {
		paragraphs = append(paragraphs,
			`<w:p><w:r><w:t>this is a paragraph with more than enough words to ensure that the pseudo page word count threshold is exceeded during extraction testing so that multiple pages are created from the content</w:t></w:r></w:p>`)
	}
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>` + strings.Join(paragraphs, "") + `</w:body></w:document>`

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 2 {
		t.Fatalf("expected at least 2 pages from word-count chunking, got %d", len(pages))
	}
	// Verify page numbers are sequential.
	for i, p := range pages {
		if p.PageNum != i+1 {
			t.Errorf("page %d has PageNum %d", i+1, p.PageNum)
		}
	}
}

func TestExtractPages_UnspacedTextPaginates(t *testing.T) {
	// Chinese/Japanese/Thai prose has no spaces, so counting only
	// space-delimited fields would fit 12,000 runes into one pseudo-page and
	// leave the tail unreachable behind read_docx_page's head-only truncation.
	const perPara = 60
	para := textPara(strings.Repeat("漢", perPara))
	var body []string
	for i := 0; i < 200; i++ {
		body = append(body, para)
	}

	pages, err := ExtractPages(createTestDocx(t, wrapBody(strings.Join(body, ""))))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 3 {
		t.Fatalf("expected at least 3 pages for 12,000 unspaced runes, got %d", len(pages))
	}
	if pages[0].PageNum != 1 || pages[0].Text == "" {
		t.Errorf("page 1 should be numbered 1 and carry text, got %+v", pages[0])
	}
	runes := 0
	for _, p := range pages {
		runes += strings.Count(p.Text, "漢")
	}
	if runes != 200*perPara {
		t.Errorf("every input rune should survive pagination, got %d of %d", runes, 200*perPara)
	}
}

func TestExtractPages_TabCharacter(t *testing.T) {
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>Before tab</w:t><w:tab/><w:t>After tab</w:t></w:r></w:p>
</w:body>
</w:document>`

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "Before tab") || !strings.Contains(pages[0].Text, "After tab") {
		t.Errorf("page should contain both text segments, got: %s", pages[0].Text)
	}
}

func TestExtractPages_EmptyDocument(t *testing.T) {
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body></w:body></w:document>`

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if pages != nil {
		t.Errorf("expected nil pages for empty document, got %d", len(pages))
	}
}

func TestExtractPages_ParagraphProperties(t *testing.T) {
	// Real DOCX files always have <w:pPr> elements for paragraph styles.
	// The parser must ignore them and still extract text from <w:t> elements.
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/><w:rPr><w:b/><w:sz w:val="28"/></w:rPr></w:pPr><w:r><w:rPr><w:b/></w:rPr><w:t>Heading One</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Normal"/><w:rPr><w:sz w:val="22"/></w:rPr></w:pPr><w:r><w:t>Normal paragraph text about database optimization and query planning.</w:t></w:r></w:p>
<w:p><w:pPr><w:rPr><w:i/></w:rPr></w:pPr><w:r><w:rPr><w:i/></w:rPr><w:t>Italic text paragraph about concurrency patterns.</w:t></w:r></w:p>
</w:body>
</w:document>`

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "Heading One") {
		t.Errorf("page should contain 'Heading One', got: %s", pages[0].Text)
	}
	if !strings.Contains(pages[0].Text, "database optimization") {
		t.Errorf("page should contain 'database optimization', got: %s", pages[0].Text)
	}
	if !strings.Contains(pages[0].Text, "concurrency patterns") {
		t.Errorf("page should contain 'concurrency patterns', got: %s", pages[0].Text)
	}
}

func TestExtractPages_TableCellText(t *testing.T) {
	// Text inside table cells must be captured — tables have <w:tbl><w:tr><w:tc><w:p>...
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Cell A1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Cell B1</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p><w:r><w:t>After table paragraph.</w:t></w:r></w:p>
</w:body>
</w:document>`

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "Cell A1") {
		t.Errorf("page should contain 'Cell A1', got: %s", pages[0].Text)
	}
	if !strings.Contains(pages[0].Text, "Cell B1") {
		t.Errorf("page should contain 'Cell B1', got: %s", pages[0].Text)
	}
	if !strings.Contains(pages[0].Text, "Cell A1\tCell B1") {
		t.Errorf("row cells should be joined by a tab, got: %q", pages[0].Text)
	}
	if !strings.Contains(pages[0].Text, "After table paragraph") {
		t.Errorf("page should contain 'After table paragraph', got: %s", pages[0].Text)
	}
}

func TestExtractPages_HyperlinkText(t *testing.T) {
	// Hyperlinks contain <w:r><w:t> inside <w:hyperlink> — text must be extracted.
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<w:body>
<w:p><w:r><w:t>Before link. </w:t></w:r><w:hyperlink r:id="rId1"><w:r><w:t>Link Text</w:t></w:r></w:hyperlink><w:r><w:t> After link.</w:t></w:r></w:p>
</w:body>
</w:document>`

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "Before link") {
		t.Errorf("page should contain 'Before link', got: %s", pages[0].Text)
	}
	if !strings.Contains(pages[0].Text, "Link Text") {
		t.Errorf("page should contain 'Link Text', got: %s", pages[0].Text)
	}
	if !strings.Contains(pages[0].Text, "After link") {
		t.Errorf("page should contain 'After link', got: %s", pages[0].Text)
	}
}

func TestExtractPages_NestedParagraphProperties(t *testing.T) {
	// A real-world DOCX with <w:rPr> inside <w:pPr> and inside <w:r>.
	// This is the most common DOCX structure — the parser must handle it.
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml"
            xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"
            mc:Ignorable="w14">
<w:body>
<w:p><w:pPr><w:pStyle w:val="Title"/><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/><w:sz w:val="56"/></w:rPr></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/></w:rPr><w:t>Annual Report 2025</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Heading1"/><w:rPr><w:sz w:val="32"/></w:rPr></w:pPr><w:r><w:t>Executive Summary</w:t></w:r></w:p>
<w:p><w:r><w:t>This report covers the financial performance of Q1 through Q4. Revenue increased by fifteen percent compared to the previous year, driven by growth in the cloud services division.</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Heading1"/><w:rPr><w:sz w:val="32"/></w:rPr></w:pPr><w:r><w:t>Market Analysis</w:t></w:r></w:p>
<w:p><w:r><w:t>The technology sector experienced significant volatility throughout the year. Our portfolio diversified into emerging markets, reducing exposure to domestic market fluctuations.</w:t></w:r></w:p>
</w:body>
</w:document>`

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatal("expected at least 1 page, got 0")
	}
	// Check that key text content was extracted
	allText := ""
	for _, p := range pages {
		allText += p.Text
	}
	if !strings.Contains(allText, "Annual Report 2025") {
		t.Errorf("expected 'Annual Report 2025' in extracted text, got: %s", allText)
	}
	if !strings.Contains(allText, "Executive Summary") {
		t.Errorf("expected 'Executive Summary' in extracted text, got: %s", allText)
	}
	if !strings.Contains(allText, "cloud services division") {
		t.Errorf("expected 'cloud services division' in extracted text, got: %s", allText)
	}
	if !strings.Contains(allText, "Market Analysis") {
		t.Errorf("expected 'Market Analysis' in extracted text, got: %s", allText)
	}
}

func TestExtractPages_StructuredDocumentTag(t *testing.T) {
	// Content controls (<w:sdt>) wrap content in <w:sdtContent> elements.
	// The parser must extract text from <w:t> elements inside these.
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:sdt><w:sdtContent><w:p><w:r><w:t>SDT Title Content</w:t></w:r></w:p></w:sdtContent></w:sdt>
<w:p><w:r><w:t>Regular paragraph after SDT.</w:t></w:r></w:p>
</w:body>
</w:document>`

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatal("expected at least 1 page, got 0")
	}
	allText := ""
	for _, p := range pages {
		allText += p.Text
	}
	if !strings.Contains(allText, "SDT Title Content") {
		t.Errorf("expected 'SDT Title Content' in extracted text, got: %s", allText)
	}
	if !strings.Contains(allText, "Regular paragraph after SDT") {
		t.Errorf("expected 'Regular paragraph after SDT' in extracted text, got: %s", allText)
	}
}

func TestExtractPages_NestedTextBoxParagraph(t *testing.T) {
	// A text box's body is a <w:p> nested inside the outer <w:p>. Its text
	// belongs to the outer paragraph in document order: the nested start must
	// not reset the outer paragraph, nor its end flush it.
	docXML := wrapBody(
		`<w:p><w:r><w:t>before</w:t></w:r>` +
			`<w:r><w:drawing><w:txbxContent><w:p><w:r><w:t>inside</w:t></w:r></w:p></w:txbxContent></w:drawing></w:r>` +
			`<w:r><w:t>after</w:t></w:r></w:p>`)

	pages, err := ExtractPages(createTestDocx(t, docXML))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if pages[0].Text != "before inside after" {
		t.Errorf("nested paragraph text should join the outer paragraph in order, got: %q", pages[0].Text)
	}
}

func TestExtractPages_MissingDocumentXML(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test*.docx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	w := zip.NewWriter(tmpFile)
	if _, err := w.Create("not/document.xml"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	tmpFile.Close()

	_, err = ExtractPages(tmpFile.Name())
	if err == nil {
		t.Error("expected error for missing word/document.xml")
	}
}

func TestExtractPages_NotAZipArchive(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test*.docx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })
	if _, err := tmpFile.WriteString("this is not a zip archive"); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	if _, err := ExtractPages(tmpFile.Name()); err == nil {
		t.Error("expected an error for a non-ZIP input")
	}
}

// utf16LE encodes s as UTF-16LE with a byte-order mark, the form Word writes
// into a part whose XML declaration says encoding="UTF-16".
func utf16LE(s string) string {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, 0, 2+2*len(units))
	buf = append(buf, 0xFF, 0xFE)
	for _, u := range units {
		buf = append(buf, byte(u), byte(u>>8))
	}
	return string(buf)
}

func TestExtractPages_UTF16Part(t *testing.T) {
	docXML := `<?xml version="1.0" encoding="UTF-16" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>UTF sixteen body text.</w:t></w:r></w:p>
</w:body>
</w:document>`

	pages, err := ExtractPages(createTestDocx(t, utf16LE(docXML)))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "UTF sixteen body text.") {
		t.Errorf("page should carry the UTF-16 text, got: %q", pages[0].Text)
	}
}

func TestExtractPages_OversizedPart(t *testing.T) {
	old := maxPartBytes
	maxPartBytes = 1 << 20
	t.Cleanup(func() { maxPartBytes = old })

	t.Run("document_part_is_fatal", func(t *testing.T) {
		body := `<w:p><w:r><w:t>` + strings.Repeat("a", int(maxPartBytes)+1) + `</w:t></w:r></w:p>`
		_, err := ExtractPages(createTestDocx(t, wrapBody(body)))
		if err == nil {
			t.Fatal("expected an error for a part past the cap")
		}
		for _, want := range []string{"word/document.xml", "exceeds 1 MiB"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error should mention %q, got: %v", want, err)
			}
		}
	})

	t.Run("auxiliary_part_is_tolerated", func(t *testing.T) {
		bigStyles := `<w:styles>` + strings.Repeat(" ", int(maxPartBytes)+1) + `</w:styles>`
		path := createTestDocxFiles(t, map[string]string{
			"word/document.xml": wrapBody(textPara("body text")),
			"word/styles.xml":   bigStyles,
		})
		pages, err := ExtractPages(path)
		if err != nil {
			t.Fatalf("an oversized styles part must not fail extraction: %v", err)
		}
		if len(pages) != 1 || !strings.Contains(pages[0].Text, "body text") {
			t.Errorf("page should still carry the body text, got: %+v", pages)
		}
	})
}

// textPara renders a paragraph of plain text.
func textPara(text string) string {
	return `<w:p><w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

// headingPara renders a paragraph in the given style, e.g. "Heading1".
func headingPara(style, text string) string {
	return `<w:p><w:pPr><w:pStyle w:val="` + style + `"/></w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

// wordsPara renders exactly n space-separated words whose first word is id, so
// tests can count words and locate paragraphs without depending on real prose.
func wordsPara(id string, n int) string {
	words := make([]string, n)
	words[0] = id
	for i := 1; i < n; i++ {
		words[i] = "filler"
	}
	return strings.Join(words, " ")
}

func TestExtractPages_SoftLineBreaks(t *testing.T) {
	// <w:br/> without type="page", <w:cr/> and <w:lastRenderedPageBreak/> all
	// separate text without starting a new pseudo-page.
	cases := []struct {
		name string
		body string
	}{
		{
			name: "br",
			body: `<w:p><w:r><w:t>Line one</w:t></w:r><w:r><w:br/><w:t>Line two</w:t></w:r></w:p>`,
		},
		{
			name: "cr",
			body: `<w:p><w:r><w:t>Line one</w:t><w:cr/><w:t>Line two</w:t></w:r></w:p>`,
		},
		{
			name: "lastRenderedPageBreak",
			body: `<w:p><w:r><w:t>Line one</w:t><w:lastRenderedPageBreak/><w:t>Line two</w:t></w:r></w:p>`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := createTestDocx(t, wrapBody(tc.body))
			pages, err := ExtractPages(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(pages) != 1 {
				t.Fatalf("expected 1 page (a soft break is not a page boundary), got %d", len(pages))
			}
			if pages[0].PageNum != 1 {
				t.Errorf("expected PageNum 1, got %d", pages[0].PageNum)
			}
			if !strings.Contains(pages[0].Text, "Line one\nLine two") {
				t.Errorf("soft break should keep a newline between the lines, got: %q", pages[0].Text)
			}
		})
	}
}

func TestExtractPages_LeadingSoftBreak(t *testing.T) {
	// A run that begins with a soft break keeps its leading newline instead of
	// gluing its text onto the previous paragraph.
	path := createTestDocx(t, wrapBody(
		`<w:p><w:r><w:t>First line</w:t></w:r></w:p>`+
			`<w:p><w:r><w:br/><w:t>Second line</w:t></w:r></w:p>`))
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "First line\n\nSecond line") {
		t.Errorf("leading <w:br/> should leave a blank line, got: %q", pages[0].Text)
	}
	if strings.Contains(pages[0].Text, "First lineSecond line") {
		t.Errorf("leading <w:br/> was dropped, got: %q", pages[0].Text)
	}
}

func TestExtractPages_TableRows(t *testing.T) {
	// One paragraph per row, cells joined by tabs, rows kept on separate lines,
	// a cell's own paragraphs joined by a single space, empty rows dropped.
	docXML := wrapBody(`<w:tbl>
<w:tr><w:tc><w:p><w:r><w:t>Cell A1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Cell B1</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>Cell A2</w:t></w:r></w:p><w:p><w:r><w:t>continued</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Cell B2</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p/></w:tc><w:tc><w:p/></w:tc></w:tr>
</w:tbl>`)

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	text := pages[0].Text
	if !strings.Contains(text, "Cell A1\tCell B1") {
		t.Errorf("row 1 cells should be joined by a tab, got: %q", text)
	}
	if !strings.Contains(text, "Cell A1\tCell B1\nCell A2 continued\tCell B2") {
		t.Errorf("rows should be separate lines and a cell's paragraphs joined by a space, got: %q", text)
	}
	if strings.Count(text, "\n") != 1 {
		t.Errorf("expected exactly 2 rows (1 newline), got: %q", text)
	}
}

func TestExtractPages_NestedTable(t *testing.T) {
	// A nested table contributes its text to the enclosing cell rather than
	// flushing its rows to the top-level paragraph list.
	docXML := wrapBody(`<w:tbl><w:tr>
<w:tc><w:p><w:r><w:t>Outer</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Inner</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
</w:tc>
<w:tc><w:p><w:r><w:t>Second</w:t></w:r></w:p></w:tc>
</w:tr></w:tbl>`)

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "Outer Inner\tSecond") {
		t.Errorf("nested table text should join the enclosing cell, got: %q", pages[0].Text)
	}
}

const testFootnotesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:footnote w:type="separator" w:id="-1"><w:p><w:r><w:separator/></w:r></w:p></w:footnote>
<w:footnote w:type="continuationSeparator" w:id="0"><w:p><w:r><w:continuationSeparator/></w:r></w:p></w:footnote>
<w:footnote w:id="1"><w:p><w:r><w:t>First footnote text.</w:t></w:r></w:p></w:footnote>
<w:footnote w:id="2"><w:p><w:r><w:t>Second footnote text.</w:t></w:r></w:p></w:footnote>
</w:footnotes>`

func TestExtractPages_Footnotes(t *testing.T) {
	docXML := wrapBody(
		`<w:p><w:r><w:t>First page with a note</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r></w:p>` +
			`<w:p><w:r><w:t>Same note again</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r></w:p>` +
			`<w:p><w:r><w:br w:type="page"/></w:r></w:p>` +
			`<w:p><w:r><w:t>Second page note</w:t></w:r><w:r><w:footnoteReference w:id="2"/></w:r></w:p>`)

	path := createTestDocxFiles(t, map[string]string{
		"word/document.xml":  docXML,
		"word/footnotes.xml": testFootnotesXML,
	})
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}

	if !strings.Contains(pages[0].Text, "note[^1]") {
		t.Errorf("page 1 should carry the [^1] marker, got: %q", pages[0].Text)
	}
	if !strings.Contains(pages[0].Text, "[^1]: First footnote text.") {
		t.Errorf("page 1 should carry the footnote body, got: %q", pages[0].Text)
	}
	if n := strings.Count(pages[0].Text, "[^1]:"); n != 1 {
		t.Errorf("page 1 should spell the footnote out once, got %d\n%s", n, pages[0].Text)
	}
	if strings.Contains(pages[0].Text, "[^2]") {
		t.Errorf("page 1 should not carry page 2's footnote, got: %q", pages[0].Text)
	}

	if !strings.Contains(pages[1].Text, "[^2]: Second footnote text.") {
		t.Errorf("page 2 should carry its own footnote body, got: %q", pages[1].Text)
	}
	if strings.Contains(pages[1].Text, "[^1]") {
		t.Errorf("page 2 should not carry page 1's footnote, got: %q", pages[1].Text)
	}
	// The separator footnotes are layout furniture and must not surface.
	if strings.Contains(pages[0].Text+pages[1].Text, "separator") {
		t.Errorf("separator footnotes should be skipped, got: %q", pages[0].Text+pages[1].Text)
	}
}

func TestExtractPages_FootnoteMultipleParagraphs(t *testing.T) {
	// One <w:footnote> may hold several <w:p>; the whole body is the note.
	footnotes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:footnote w:id="1"><w:p><w:r><w:t>First part of the note.</w:t></w:r></w:p><w:p><w:r><w:t>Second part of the note.</w:t></w:r></w:p></w:footnote>
</w:footnotes>`
	docXML := wrapBody(
		`<w:p><w:r><w:t>claim needing a note</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r></w:p>`)

	pages, err := ExtractPages(createTestDocxFiles(t, map[string]string{
		"word/document.xml":  docXML,
		"word/footnotes.xml": footnotes,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "[^1]: First part of the note. Second part of the note.") {
		t.Errorf("a multi-paragraph note should join into one body, got: %q", pages[0].Text)
	}
}

func TestExtractPages_HeadingPagination_DefersToHeading(t *testing.T) {
	// 16 body paragraphs of 40 words each (640 words) followed by a heading:
	// the page crosses WordsPerPage at paragraph 13, but the heading still fits
	// the 750-word budget, so the page runs on and the heading opens the next.
	cases := []struct {
		name    string
		heading string
	}{
		{"heading1_style", headingPara("Heading1", "Section Two")},
		{"heading_style_with_space", headingPara("Heading 2", "Section Two")},
		{"heading_style_lowercase", headingPara("heading3", "Section Two")},
		{"title_style", headingPara("Title", "Section Two")},
		{"outline_level", `<w:p><w:pPr><w:outlineLvl w:val="1"/></w:pPr><w:r><w:t>Section Two</w:t></w:r></w:p>`},
		// Word may pad the attribute value; styles.go trims it, so the body
		// has to as well or the same value is a heading in a style only.
		{"padded_outline_level", `<w:p><w:pPr><w:outlineLvl w:val=" 0 "/></w:pPr><w:r><w:t>Section Two</w:t></w:r></w:p>`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body []string
			for i := 0; i < 16; i++ {
				body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
			}
			body = append(body, tc.heading)
			for i := 16; i < 19; i++ {
				body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
			}

			path := createTestDocx(t, wrapBody(strings.Join(body, "")))
			pages, err := ExtractPages(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(pages) != 2 {
				t.Fatalf("expected 2 pages, got %d", len(pages))
			}
			if !strings.Contains(pages[0].Text, "para15") {
				t.Errorf("page 1 should absorb the paragraphs before the heading, got: %q", pages[0].Text)
			}
			if strings.Contains(pages[0].Text, "Section Two") {
				t.Errorf("the heading should start page 2, got page 1: %q", pages[0].Text)
			}
			if !strings.HasPrefix(pages[1].Text, "Section Two") {
				t.Errorf("page 2 should start with the heading, got: %q", pages[1].Text)
			}
			if !strings.Contains(pages[1].Text, "para16") {
				t.Errorf("page 2 should carry the text after the heading, got: %q", pages[1].Text)
			}
		})
	}
}

func TestExtractPages_HeadingPagination_IgnoresBodyStyles(t *testing.T) {
	// A body-styled paragraph is not a heading: the page must still flush on
	// word count exactly as it did before heading awareness.
	var body []string
	for i := 0; i < 16; i++ {
		body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
	}
	body = append(body, `<w:p><w:pPr><w:pStyle w:val="Normal"/></w:pPr><w:r><w:t>Not a heading</w:t></w:r></w:p>`)
	for i := 16; i < 19; i++ {
		body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
	}

	path := createTestDocx(t, wrapBody(strings.Join(body, "")))
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	if strings.Contains(pages[0].Text, "para13") {
		t.Errorf("page 1 should stop at the word-count threshold, got: %q", pages[0].Text)
	}
	if !strings.HasPrefix(pages[1].Text, "para13") {
		t.Errorf("page 2 should start at para13, got: %q", pages[1].Text)
	}
	if !strings.Contains(pages[1].Text, "Not a heading") {
		t.Errorf("page 2 should carry the body-styled paragraph, got: %q", pages[1].Text)
	}
}

func TestExtractPages_HeadingPagination_BudgetCeiling(t *testing.T) {
	// The heading sits past 750 words: the page must not grow without bound, so
	// it flushes on word count and the heading stays on the next page.
	var body []string
	for i := 0; i < 13; i++ {
		body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
	}
	for i := 13; i < 21; i++ {
		body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
	}
	body = append(body, headingPara("Heading1", "Section Three"))
	body = append(body, textPara(wordsPara("para21", 40)))

	path := createTestDocx(t, wrapBody(strings.Join(body, "")))
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	words := len(strings.Fields(pages[0].Text))
	if words != 13*40 {
		t.Errorf("page 1 should flush on word count (13 paragraphs), got %d: %q", words, pages[0].Text)
	}
	if !strings.HasPrefix(pages[1].Text, "para13") {
		t.Errorf("page 2 should start at para13, got: %q", pages[1].Text)
	}
	if !strings.Contains(pages[1].Text, "Section Three") {
		t.Errorf("page 2 should carry the heading, got: %q", pages[1].Text)
	}
}

func TestExtractPages_HeadingPagination_ShortDocumentStaysOnePage(t *testing.T) {
	// A short, multi-heading document never reaches WordsPerPage, so the
	// headings must not manufacture extra pages.
	docXML := wrapBody(
		headingPara("Title", "Annual Report 2025") +
			headingPara("Heading1", "Executive Summary") +
			textPara("Revenue increased by fifteen percent, driven by cloud services.") +
			headingPara("Heading2", "Market Analysis") +
			textPara("The technology sector experienced significant volatility this year.") +
			headingPara("Heading3", "Outlook") +
			textPara("We expect steady growth next year across all divisions."))

	path := createTestDocx(t, docXML)
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "Market Analysis") {
		t.Errorf("heading text should stay in the page text, got: %q", pages[0].Text)
	}
	if strings.Contains(pages[0].Text, "Heading1") || strings.Contains(pages[0].Text, "Heading2") {
		t.Errorf("style names must not leak into the page text, got: %q", pages[0].Text)
	}
}

// pageBreakBeforePara renders a paragraph whose pPr opens a page. An empty val
// writes the bare flag; otherwise the val attribute is the given override
// ("0" / "false" switch it off).
func pageBreakBeforePara(text, val string) string {
	flag := "<w:pageBreakBefore/>"
	if val != "" {
		flag = `<w:pageBreakBefore w:val="` + val + `"/>`
	}
	return `<w:p><w:pPr>` + flag + `</w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

// assertHeadingStartsSecondPage builds the same document shape as
// TestExtractPages_HeadingPagination_DefersToHeading — 16 paragraphs of 40
// words, the heading, then 3 more — and asserts the heading opens page 2.
// extraFiles are additional archive parts (a styles part, say).
func assertHeadingStartsSecondPage(t *testing.T, heading string, extraFiles map[string]string) {
	t.Helper()
	var body []string
	for i := 0; i < 16; i++ {
		body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
	}
	body = append(body, heading)
	for i := 16; i < 19; i++ {
		body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
	}

	parts := map[string]string{"word/document.xml": wrapBody(strings.Join(body, ""))}
	for name, content := range extraFiles {
		parts[name] = content
	}
	pages, err := ExtractPages(createTestDocxFiles(t, parts))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "para15") {
		t.Errorf("page 1 should absorb the paragraphs before the heading, got: %q", pages[0].Text)
	}
	if strings.Contains(pages[0].Text, "Section Two") {
		t.Errorf("the heading should start page 2, got page 1: %q", pages[0].Text)
	}
	if !strings.HasPrefix(pages[1].Text, "Section Two") {
		t.Errorf("page 2 should start with the heading, got: %q", pages[1].Text)
	}
}

// assertStyleIsNotAHeading builds the same document shape with a paragraph in
// the given style: the page must flush on word count exactly as it does for
// body text, so para13 opens page 2.
func assertStyleIsNotAHeading(t *testing.T, style string, extraFiles map[string]string) {
	t.Helper()
	var body []string
	for i := 0; i < 16; i++ {
		body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
	}
	body = append(body, headingPara(style, "Not a heading"))
	for i := 16; i < 19; i++ {
		body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
	}

	parts := map[string]string{"word/document.xml": wrapBody(strings.Join(body, ""))}
	for name, content := range extraFiles {
		parts[name] = content
	}
	pages, err := ExtractPages(createTestDocxFiles(t, parts))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	if strings.Contains(pages[0].Text, "para13") {
		t.Errorf("page 1 should stop at the word-count threshold, got: %q", pages[0].Text)
	}
	if !strings.HasPrefix(pages[1].Text, "para13") {
		t.Errorf("page 2 should start at para13, got: %q", pages[1].Text)
	}
	if !strings.Contains(pages[1].Text, "Not a heading") {
		t.Errorf("page 2 should carry the styled paragraph, got: %q", pages[1].Text)
	}
}

const testEndnotesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:endnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:endnote w:type="separator" w:id="-1"><w:p><w:r><w:separator/></w:r></w:p></w:endnote>
<w:endnote w:type="continuationNotice" w:id="0"><w:p><w:r><w:t>notice furniture</w:t></w:r></w:p></w:endnote>
<w:endnote w:id="2"><w:p><w:r><w:t>Endnote body text.</w:t></w:r></w:p></w:endnote>
</w:endnotes>`

func TestExtractPages_Endnotes(t *testing.T) {
	// The endnote's w:id is 2, and it is referenced after a footnote: endnotes
	// number independently of footnotes and by reference order, not by id.
	docXML := wrapBody(
		`<w:p><w:r><w:t>Footnote first</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r></w:p>` +
			`<w:p><w:r><w:t>Then an endnote</w:t></w:r><w:r><w:endnoteReference w:id="2"/></w:r></w:p>` +
			`<w:p><w:r><w:t>Same endnote again</w:t></w:r><w:r><w:endnoteReference w:id="2"/></w:r></w:p>`)

	t.Run("body_appended_to_referencing_page", func(t *testing.T) {
		path := createTestDocxFiles(t, map[string]string{
			"word/document.xml":  docXML,
			"word/footnotes.xml": testFootnotesXML,
			"word/endnotes.xml":  testEndnotesXML,
		})
		pages, err := ExtractPages(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 1 {
			t.Fatalf("expected 1 page, got %d", len(pages))
		}
		text := pages[0].Text

		if !strings.Contains(text, "Then an endnote[^e1]") {
			t.Errorf("the endnote reference should leave a [^e1] marker, got: %q", text)
		}
		if !strings.Contains(text, "[^e1]: Endnote body text.") {
			t.Errorf("the endnote body should be appended to the page, got: %q", text)
		}
		if n := strings.Count(text, "[^e1]:"); n != 1 {
			t.Errorf("a note referenced twice should be spelled out once, got %d\n%s", n, text)
		}
		if strings.Contains(text, "[^2]") {
			t.Errorf("the endnote id must not leak as a footnote marker, got: %q", text)
		}
		// Footnote numbering is its own sequence.
		if !strings.Contains(text, "Footnote first[^1]") || !strings.Contains(text, "[^1]: First footnote text.") {
			t.Errorf("footnote markers and bodies must keep their own numbering, got: %q", text)
		}
		if strings.Contains(text, "notice furniture") || strings.Contains(text, "separator") {
			t.Errorf("separator/continuationNotice endnotes should be skipped, got: %q", text)
		}
	})

	t.Run("marker_without_endnotes_part", func(t *testing.T) {
		path := createTestDocx(t, wrapBody(
			`<w:p><w:r><w:t>Bare endnote</w:t></w:r><w:r><w:endnoteReference w:id="2"/></w:r></w:p>`))
		pages, err := ExtractPages(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 1 {
			t.Fatalf("expected 1 page, got %d", len(pages))
		}
		if !strings.Contains(pages[0].Text, "Bare endnote[^e1]") {
			t.Errorf("the marker must stay without an endnotes part, got: %q", pages[0].Text)
		}
		if strings.Contains(pages[0].Text, "[^e1]:") {
			t.Errorf("there is no body to append without an endnotes part, got: %q", pages[0].Text)
		}
	})
}

func TestExtractPages_PageBreakBefore(t *testing.T) {
	t.Run("opens_a_new_page", func(t *testing.T) {
		docXML := wrapBody(
			textPara("first paragraph") +
				pageBreakBeforePara("second paragraph", "") +
				textPara("third paragraph"))
		pages, err := ExtractPages(createTestDocx(t, docXML))
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 2 {
			t.Fatalf("expected 2 pages, got %d", len(pages))
		}
		if strings.Contains(pages[0].Text, "second paragraph") {
			t.Errorf("the flagged paragraph should open page 2, got page 1: %q", pages[0].Text)
		}
		if !strings.HasPrefix(pages[1].Text, "second paragraph") {
			t.Errorf("page 2 should start with the flagged paragraph, got: %q", pages[1].Text)
		}
		if !strings.Contains(pages[1].Text, "third paragraph") {
			t.Errorf("page 2 should carry the following paragraphs, got: %q", pages[1].Text)
		}
	})

	for _, off := range []string{"0", "false"} {
		t.Run("val_"+off+"_is_off", func(t *testing.T) {
			docXML := wrapBody(
				textPara("first paragraph") +
					pageBreakBeforePara("second paragraph", off))
			pages, err := ExtractPages(createTestDocx(t, docXML))
			if err != nil {
				t.Fatal(err)
			}
			if len(pages) != 1 {
				t.Fatalf("expected 1 page with w:val=%q, got %d", off, len(pages))
			}
			if !strings.Contains(pages[0].Text, "second paragraph") {
				t.Errorf("paragraph should stay on page 1, got: %q", pages[0].Text)
			}
		})
	}

	t.Run("leading_flag_does_not_create_empty_page", func(t *testing.T) {
		docXML := wrapBody(
			pageBreakBeforePara("only paragraph", "") +
				textPara("second paragraph"))
		pages, err := ExtractPages(createTestDocx(t, docXML))
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 1 {
			t.Fatalf("expected 1 page, got %d", len(pages))
		}
		if !strings.HasPrefix(pages[0].Text, "only paragraph") {
			t.Errorf("page 1 should start with the first paragraph, got: %q", pages[0].Text)
		}
	})

	t.Run("table_row_forces_break", func(t *testing.T) {
		row := `<w:tbl><w:tr>` +
			`<w:tc><w:p><w:pPr><w:pageBreakBefore/></w:pPr><w:r><w:t>cellA</w:t></w:r></w:p></w:tc>` +
			`<w:tc><w:p><w:r><w:t>cellB</w:t></w:r></w:p></w:tc>` +
			`</w:tr></w:tbl>`
		pages, err := ExtractPages(createTestDocx(t, wrapBody(textPara("before the table")+row)))
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 2 {
			t.Fatalf("expected 2 pages, got %d", len(pages))
		}
		if !strings.HasPrefix(pages[1].Text, "cellA\tcellB") {
			t.Errorf("the row should open page 2 with its cells tab-joined, got: %q", pages[1].Text)
		}
	})
}

const testSkippedFootnoteIDsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:footnote w:type="separator" w:id="-1"><w:p><w:r><w:separator/></w:r></w:p></w:footnote>
<w:footnote w:type="continuationSeparator" w:id="0"><w:p><w:r><w:continuationSeparator/></w:r></w:p></w:footnote>
<w:footnote w:id="1"><w:p><w:r><w:t>Body for the first note.</w:t></w:r></w:p></w:footnote>
<w:footnote w:id="4"><w:p><w:r><w:t>Body for the second note.</w:t></w:r></w:p></w:footnote>
</w:footnotes>`

func TestExtractPages_FootnoteNumberingByReferenceOrder(t *testing.T) {
	// Word's w:id skips after deletions, so ids 1 and 4 are the document's
	// first and second note: they must display as [^1] and [^2].
	docXML := wrapBody(
		`<w:p><w:r><w:t>first claim</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r></w:p>` +
			`<w:p><w:r><w:t>second claim</w:t></w:r><w:r><w:footnoteReference w:id="4"/></w:r></w:p>` +
			`<w:p><w:r><w:t>first claim again</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r></w:p>`)

	path := createTestDocxFiles(t, map[string]string{
		"word/document.xml":  docXML,
		"word/footnotes.xml": testSkippedFootnoteIDsXML,
	})
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	text := pages[0].Text

	for _, want := range []string{
		"first claim[^1]",
		"second claim[^2]",
		"first claim again[^1]",
		"[^1]: Body for the first note.",
		"[^2]: Body for the second note.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q, got: %q", want, text)
		}
	}
	if n := strings.Count(text, "[^1]:"); n != 1 {
		t.Errorf("the repeated note should be spelled out once, got %d\n%s", n, text)
	}
	if n := strings.Count(text, "[^2]:"); n != 1 {
		t.Errorf("the second note should be spelled out once, got %d\n%s", n, text)
	}
	if strings.Contains(text, "[^4]") {
		t.Errorf("markers must use display numbers, not raw w:id, got: %q", text)
	}
}

const testStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:style w:type="paragraph" w:styleId="Ttulo1"><w:name w:val="heading 1"/></w:style>
<w:style w:type="paragraph" w:styleId="Ttulo2"><w:name w:val="Titulo personalizado"/><w:basedOn w:val="Ttulo1"/></w:style>
<w:style w:type="paragraph" w:styleId="Contorno2"><w:name w:val="Contorno"/><w:pPr><w:outlineLvl w:val="1"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Cuerpo"><w:name w:val="Cuerpo de texto"/></w:style>
<w:style w:type="character" w:styleId="Fuerte"><w:name w:val="heading 3"/></w:style>
<w:style w:type="paragraph" w:styleId="CicloA"><w:name w:val="Ciclo A"/><w:basedOn w:val="CicloB"/></w:style>
<w:style w:type="paragraph" w:styleId="CicloB"><w:name w:val="Ciclo B"/><w:basedOn w:val="CicloA"/></w:style>
</w:styles>`

func TestExtractPages_HeadingStyleFromStylesPart(t *testing.T) {
	stylesFiles := map[string]string{"word/styles.xml": testStylesXML}

	t.Run("localised_style_name_is_a_heading", func(t *testing.T) {
		assertHeadingStartsSecondPage(t, headingPara("Ttulo1", "Section Two"), stylesFiles)
	})

	t.Run("based_on_heading_style_is_a_heading", func(t *testing.T) {
		assertHeadingStartsSecondPage(t, headingPara("Ttulo2", "Section Two"), stylesFiles)
	})

	t.Run("outline_level_in_style_is_a_heading", func(t *testing.T) {
		assertHeadingStartsSecondPage(t, headingPara("Contorno2", "Section Two"), stylesFiles)
	})

	t.Run("body_and_character_styles_are_not_headings", func(t *testing.T) {
		assertStyleIsNotAHeading(t, "Cuerpo", stylesFiles)
		assertStyleIsNotAHeading(t, "Fuerte", stylesFiles)
	})

	t.Run("based_on_cycle_is_not_a_heading", func(t *testing.T) {
		assertStyleIsNotAHeading(t, "CicloA", stylesFiles)
	})

	t.Run("missing_styles_part_keeps_old_behaviour", func(t *testing.T) {
		assertStyleIsNotAHeading(t, "Ttulo1", nil)
	})

	t.Run("unparsable_styles_part_does_not_fail", func(t *testing.T) {
		assertStyleIsNotAHeading(t, "Ttulo1", map[string]string{
			"word/styles.xml": `<w:styles><w:style w:styleId=`,
		})
	})
}

func TestExtractPages_RealisticLongDocument(t *testing.T) {
	// Roughly 1,600 filler words across three chapters, with a soft break, a
	// table and a footnote: pagination must be deterministic in CI.
	var body []string
	body = append(body, headingPara("Heading1", "Chapter 1 Introduction"))
	body = append(body, `<w:p><w:r><w:t>softbreak-left</w:t><w:br/><w:t>softbreak-right</w:t></w:r></w:p>`)
	body = append(body, `<w:tbl>`+
		`<w:tr><w:tc><w:p><w:r><w:t>cellA1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>cellB1</w:t></w:r></w:p></w:tc></w:tr>`+
		`<w:tr><w:tc><w:p><w:r><w:t>cellA2</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>cellB2</w:t></w:r></w:p></w:tc></w:tr>`+
		`</w:tbl>`)
	body = append(body, `<w:p><w:r><w:t>claim needing support</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r></w:p>`)
	for _, id := range []string{"alpha", "beta", "gamma"} {
		body = append(body, textPara(wordsPara(id, 200)))
	}
	body = append(body, headingPara("Heading2", "Chapter 2 Methods"))
	for _, id := range []string{"delta", "epsilon", "zeta"} {
		body = append(body, textPara(wordsPara(id, 200)))
	}
	body = append(body, headingPara("Heading2", "Chapter 3 Results"))
	for _, id := range []string{"eta", "theta"} {
		body = append(body, textPara(wordsPara(id, 200)))
	}

	path := createTestDocxFiles(t, map[string]string{
		"word/document.xml":  wrapBody(strings.Join(body, "")),
		"word/footnotes.xml": testFootnotesXML,
	})
	pages, err := ExtractPages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 3 {
		t.Fatalf("expected 3 pages, got %d", len(pages))
	}

	if !strings.HasPrefix(pages[0].Text, "Chapter 1 Introduction") {
		t.Errorf("page 1 should open with chapter 1, got: %q", firstLine(pages[0].Text))
	}
	for _, want := range []string{
		"softbreak-left\nsoftbreak-right",
		"cellA1\tcellB1",
		"claim needing support[^1]",
		"[^1]: First footnote text.",
		"gamma",
	} {
		if !strings.Contains(pages[0].Text, want) {
			t.Errorf("page 1 should contain %q", want)
		}
	}
	for _, unwanted := range []string{"delta", "Chapter 2 Methods"} {
		if strings.Contains(pages[0].Text, unwanted) {
			t.Errorf("page 1 should not contain %q, got: %q", unwanted, pages[0].Text)
		}
	}

	if !strings.HasPrefix(pages[1].Text, "Chapter 2 Methods") {
		t.Errorf("page 2 should open with chapter 2, got: %q", firstLine(pages[1].Text))
	}
	if !strings.Contains(pages[1].Text, "zeta") {
		t.Errorf("page 2 should carry chapter 2's last paragraph")
	}
	// "\neta " keeps "beta"/"zeta" from matching chapter 3's first word.
	for _, unwanted := range []string{"\neta ", "Chapter 3 Results"} {
		if strings.Contains(pages[1].Text, unwanted) {
			t.Errorf("page 2 should not contain %q, got: %q", unwanted, pages[1].Text)
		}
	}

	if !strings.HasPrefix(pages[2].Text, "Chapter 3 Results") {
		t.Errorf("page 3 should open with chapter 3, got: %q", firstLine(pages[2].Text))
	}
	if !strings.Contains(pages[2].Text, "theta") {
		t.Errorf("page 3 should carry chapter 3's last paragraph")
	}
}

// firstLine returns the opening line of a page, for failure messages.
func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

// revisionChangePara is a paragraph whose tracked change reverts *away* from
// heading formatting and an opening page break. ECMA-376 §17.13.5.32: the
// <w:pPr> inside <w:pPrChange> carries the properties the revision reverts to,
// not the ones in force, so none of it may flag the paragraph.
const revisionChangePara = `<w:p><w:pPr><w:pPrChange w:id="1" w:author="Reviewer" w:date="2026-01-01T00:00:00Z"><w:pPr><w:pStyle w:val="Heading1"/><w:outlineLvl w:val="0"/><w:pageBreakBefore/></w:pPr></w:pPrChange></w:pPr><w:r><w:t>TARGET</w:t></w:r></w:p>`

// revisionChangeRunPara carries an <w:rPrChange>; if its end did not balance
// the revision depth, every later property would be read as a reverted one.
const revisionChangeRunPara = `<w:p><w:r><w:rPr><w:rPrChange w:id="2" w:author="Reviewer"><w:rPr><w:b/></w:rPr></w:rPrChange></w:rPr><w:t>REVISED-RUN</w:t></w:r></w:p>`

// headingDeferralBody renders the 19-paragraph shape the heading pagination
// tests use (16 paragraphs of 40 words, the given heading, 3 more) around lead.
func headingDeferralBody(lead, heading string) string {
	var body []string
	if lead != "" {
		body = append(body, lead)
	}
	for i := 0; i < 16; i++ {
		body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
	}
	body = append(body, heading)
	for i := 16; i < 19; i++ {
		body = append(body, textPara(wordsPara(fmt.Sprintf("para%d", i), 40)))
	}
	return strings.Join(body, "")
}

func TestExtractPages_RevisionChangeIsNotFormatting(t *testing.T) {
	t.Run("reverted_properties_page_nothing", func(t *testing.T) {
		pages, err := ExtractPages(createTestDocx(t, wrapBody(
			textPara("PAGEONE")+revisionChangePara+textPara("PAGETWO"))))
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 1 {
			t.Fatalf("expected 1 page, got %d (a reverted page break must not split the text)", len(pages))
		}
		for _, want := range []string{"PAGEONE", "TARGET", "PAGETWO"} {
			if !strings.Contains(pages[0].Text, want) {
				t.Errorf("page 1 should carry %q, got: %q", want, pages[0].Text)
			}
		}
	})

	t.Run("reverted_heading_is_not_a_heading_target", func(t *testing.T) {
		// The revision paragraph sits just before the real heading. Treated as
		// formatting it would open a page itself (or be picked as the heading
		// to defer to), and page 2 would not start at "Section Two".
		doc := wrapBody(headingDeferralBody("", revisionChangePara+headingPara("Heading1", "Section Two")))
		pages, err := ExtractPages(createTestDocx(t, doc))
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 2 {
			t.Fatalf("expected 2 pages, got %d", len(pages))
		}
		if !strings.Contains(pages[0].Text, "TARGET") {
			t.Errorf("page 1 should carry the revised paragraph, got: %q", pages[0].Text)
		}
		if !strings.Contains(pages[0].Text, "para15") {
			t.Errorf("page 1 should run on to the heading, got: %q", pages[0].Text)
		}
		if !strings.HasPrefix(pages[1].Text, "Section Two") {
			t.Errorf("page 2 should start at the heading, got: %q", pages[1].Text)
		}
	})

	t.Run("revision_depth_does_not_leak_into_later_paragraphs", func(t *testing.T) {
		// A leaked revDepth would swallow the later heading's pStyle, so the
		// page would flush at the word-count threshold instead of deferring
		// to the heading.
		doc := wrapBody(headingDeferralBody(revisionChangeRunPara, headingPara("Heading1", "Section Two")))
		pages, err := ExtractPages(createTestDocx(t, doc))
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 2 {
			t.Fatalf("expected 2 pages, got %d", len(pages))
		}
		if !strings.Contains(pages[0].Text, "REVISED-RUN") {
			t.Errorf("page 1 should carry the revised run, got: %q", pages[0].Text)
		}
		if !strings.HasPrefix(pages[1].Text, "Section Two") {
			t.Errorf("page 2 should start at the heading, got: %q", pages[1].Text)
		}
	})
}

func TestExtractPages_TableCellPageBreak(t *testing.T) {
	t.Run("break_between_text_in_a_cell_separates_the_text", func(t *testing.T) {
		doc := wrapBody(textPara("PAGEONE") +
			`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>before</w:t><w:br w:type="page"/><w:t>after</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
			textPara("PAGETWO"))
		pages, err := ExtractPages(createTestDocx(t, doc))
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 2 {
			t.Fatalf("expected 2 pages, got %d", len(pages))
		}
		if !strings.Contains(pages[0].Text, "before\nafter") {
			t.Errorf("the text on either side of the break must not glue together, got: %q", pages[0].Text)
		}
		if !strings.HasPrefix(pages[1].Text, "PAGETWO") {
			t.Errorf("page 2 should start after the break, got: %q", pages[1].Text)
		}
	})

	t.Run("break_only_row_still_pages", func(t *testing.T) {
		doc := wrapBody(textPara("PAGEONE") +
			`<w:tbl><w:tr><w:tc><w:p><w:r><w:br w:type="page"/></w:r></w:p></w:tc></w:tr></w:tbl>` +
			textPara("PAGETWO"))
		pages, err := ExtractPages(createTestDocx(t, doc))
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 2 {
			t.Fatalf("a row that exists only for its page break must not be dropped; got %d pages", len(pages))
		}
		if !strings.HasPrefix(pages[1].Text, "PAGETWO") {
			t.Errorf("page 2 should start after the break, got: %q", pages[1].Text)
		}
	})

	t.Run("empty_row_without_a_break_pages_nothing", func(t *testing.T) {
		doc := wrapBody(textPara("PAGEONE") +
			`<w:tbl><w:tr><w:tc><w:p/></w:tc></w:tr></w:tbl>` +
			textPara("PAGETWO"))
		pages, err := ExtractPages(createTestDocx(t, doc))
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 1 {
			t.Fatalf("an empty row must not manufacture a page; got %d pages", len(pages))
		}
		if !strings.Contains(pages[0].Text, "PAGEONE") || !strings.Contains(pages[0].Text, "PAGETWO") {
			t.Errorf("both texts belong to page 1, got: %q", pages[0].Text)
		}
	})
}

func TestExtractPages_NotesPartCharset(t *testing.T) {
	// Word writes the notes part in the document's code page. Without a
	// CharsetReader the decoder rejects it and every note body is lost with no
	// diagnostic.
	footnotes := `<?xml version="1.0" encoding="windows-1252" standalone="yes"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:footnote w:type="separator" w:id="-1"><w:p><w:r><w:separator/></w:r></w:p></w:footnote>
<w:footnote w:id="1"><w:p><w:r><w:t>Caf` + "\xe9" + ` cr` + "\xe8" + `me</w:t></w:r></w:p></w:footnote>
</w:footnotes>`
	docXML := wrapBody(`<w:p><w:r><w:t>Claim</w:t></w:r><w:r><w:footnoteReference w:id="1"/></w:r></w:p>`)
	pages, err := ExtractPages(createTestDocxFiles(t, map[string]string{
		"word/document.xml":  docXML,
		"word/footnotes.xml": footnotes,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if want := "[^1]: Café crème"; !strings.Contains(pages[0].Text, want) {
		t.Errorf("page 1 should carry the windows-1252 note body %q, got: %q", want, pages[0].Text)
	}
}

func TestExtractPages_UnreadableDocumentPart(t *testing.T) {
	extract := func(t *testing.T, docXML string) ([]PageText, error) {
		t.Helper()
		return ExtractPages(createTestDocxFiles(t, map[string]string{"word/document.xml": docXML}))
	}

	t.Run("empty_part_is_an_error", func(t *testing.T) {
		_, err := extract(t, "")
		if err == nil {
			t.Fatal("an empty word/document.xml must not read as a document with no text")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("error should say the part is empty, got: %v", err)
		}
	})

	t.Run("non_xml_part_is_an_error", func(t *testing.T) {
		_, err := extract(t, "not xml at all")
		if err == nil {
			t.Fatal("a part with no XML element must not read as an empty document")
		}
		if !strings.Contains(err.Error(), "not WordprocessingML") {
			t.Errorf("error should say the part is not WordprocessingML, got: %v", err)
		}
	})

	t.Run("malformed_part_is_an_error", func(t *testing.T) {
		_, err := extract(t, `<w:document><w:body><w:p>`)
		if err == nil {
			t.Fatal("a malformed part must be reported, not silently empty")
		}
		if !strings.Contains(err.Error(), "parse document.xml") {
			t.Errorf("error should name the part that failed to parse, got: %v", err)
		}
	})

	t.Run("valid_document_with_no_paragraphs_has_no_pages", func(t *testing.T) {
		pages, err := extract(t, wrapBody(""))
		if err != nil {
			t.Fatalf("an empty body is a valid document, got error: %v", err)
		}
		if len(pages) != 0 {
			t.Errorf("expected no pages, got %d: %+v", len(pages), pages)
		}
	})
}

const testStylesPageBreakXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:style w:type="paragraph" w:styleId="BreakBody"><w:name w:val="Section Body"/><w:pPr><w:pageBreakBefore/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="BreakChild"><w:name w:val="Section Child"/><w:basedOn w:val="BreakBody"/></w:style>
<w:style w:type="paragraph" w:styleId="BreakOff"><w:name w:val="Section Off"/><w:pPr><w:pageBreakBefore w:val="0"/></w:pPr></w:style>
<w:style w:type="character" w:styleId="BreakChar"><w:name w:val="Section Character"/><w:pPr><w:pageBreakBefore/></w:pPr></w:style>
</w:styles>`

func TestExtractPages_StylePageBreakBefore(t *testing.T) {
	extract := func(t *testing.T, docXML string) []PageText {
		t.Helper()
		pages, err := ExtractPages(createTestDocxFiles(t, map[string]string{
			"word/document.xml": docXML,
			"word/styles.xml":   testStylesPageBreakXML,
		}))
		if err != nil {
			t.Fatal(err)
		}
		return pages
	}

	t.Run("style_flag_opens_a_page", func(t *testing.T) {
		// BreakBody carries the flag; BreakChild inherits it through basedOn.
		for _, style := range []string{"BreakBody", "BreakChild"} {
			pages := extract(t, wrapBody(textPara("PAGEONE")+headingPara(style, "TARGET")))
			if len(pages) != 2 {
				t.Fatalf("%s: expected 2 pages, got %d", style, len(pages))
			}
			if !strings.HasPrefix(pages[1].Text, "TARGET") {
				t.Errorf("%s: page 2 should start at the styled paragraph, got: %q", style, pages[1].Text)
			}
		}
	})

	t.Run("style_switching_the_flag_off_opens_no_page", func(t *testing.T) {
		pages := extract(t, wrapBody(textPara("PAGEONE")+headingPara("BreakOff", "TARGET")))
		if len(pages) != 1 {
			t.Fatalf("expected 1 page, got %d", len(pages))
		}
	})

	t.Run("character_style_opens_no_page", func(t *testing.T) {
		pages := extract(t, wrapBody(textPara("PAGEONE")+headingPara("BreakChar", "TARGET")))
		if len(pages) != 1 {
			t.Fatalf("expected 1 page, got %d", len(pages))
		}
	})

	t.Run("unknown_style_opens_no_page", func(t *testing.T) {
		pages := extract(t, wrapBody(textPara("PAGEONE")+headingPara("Unlisted", "TARGET")))
		if len(pages) != 1 {
			t.Fatalf("expected 1 page, got %d", len(pages))
		}
	})

	t.Run("paragraph_property_overrides_the_style", func(t *testing.T) {
		// CT_PPr orders pStyle before pageBreakBefore, so the paragraph's own
		// w:val="0" is read after the style contributed its break.
		target := `<w:p><w:pPr><w:pStyle w:val="BreakBody"/><w:pageBreakBefore w:val="0"/></w:pPr><w:r><w:t>TARGET</w:t></w:r></w:p>`
		pages := extract(t, wrapBody(textPara("PAGEONE")+target))
		if len(pages) != 1 {
			t.Fatalf("expected 1 page, got %d", len(pages))
		}
	})
}

const testFurnitureFootnotesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:footnote w:type="separator" w:id="-1"><w:p><w:r><w:t>SEPARATOR-FURNITURE</w:t></w:r></w:p></w:footnote>
<w:footnote w:type="continuationSeparator" w:id="0"><w:p><w:r><w:t>CONTINUATION-FURNITURE</w:t></w:r></w:p></w:footnote>
<w:footnote w:type="continuationNotice" w:id="1"><w:p><w:r><w:t>NOTICE-FURNITURE</w:t></w:r></w:p></w:footnote>
<w:footnote w:id="2"><w:p><w:r><w:t>REAL-NOTE-BODY</w:t></w:r></w:p></w:footnote>
</w:footnotes>`

func TestExtractPages_NoteSeparatorFurniture(t *testing.T) {
	// The furniture notes carry unique text *and* are referenced by the body,
	// so a missing type filter would surface them as note bodies.
	body := `<w:p><w:r><w:t>Claim</w:t></w:r>` +
		`<w:r><w:footnoteReference w:id="-1"/></w:r>` +
		`<w:r><w:footnoteReference w:id="0"/></w:r>` +
		`<w:r><w:footnoteReference w:id="1"/></w:r>` +
		`<w:r><w:footnoteReference w:id="2"/></w:r></w:p>`
	pages, err := ExtractPages(createTestDocxFiles(t, map[string]string{
		"word/document.xml":  wrapBody(body),
		"word/footnotes.xml": testFurnitureFootnotesXML,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if !strings.Contains(pages[0].Text, "REAL-NOTE-BODY") {
		t.Errorf("page 1 should spell out the real note, got: %q", pages[0].Text)
	}
	for _, unwanted := range []string{"SEPARATOR-FURNITURE", "CONTINUATION-FURNITURE", "NOTICE-FURNITURE"} {
		if strings.Contains(pages[0].Text, unwanted) {
			t.Errorf("separator furniture %q should be skipped, got: %q", unwanted, pages[0].Text)
		}
	}
}

func TestExtractPages_SectionBreak(t *testing.T) {
	cases := []struct {
		name      string
		val       string
		wantPages int
	}{
		{"next_page", "nextPage", 2},
		{"continuous", "continuous", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			section := `<w:p><w:pPr><w:sectPr><w:type w:val="` + tc.val + `"/><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:pPr><w:r><w:t>PAGEONE</w:t></w:r></w:p>`
			pages, err := ExtractPages(createTestDocx(t, wrapBody(section+textPara("PAGETWO"))))
			if err != nil {
				t.Fatal(err)
			}
			if len(pages) != tc.wantPages {
				t.Fatalf("expected %d pages, got %d", tc.wantPages, len(pages))
			}
			if tc.wantPages == 2 {
				if !strings.HasPrefix(pages[1].Text, "PAGETWO") {
					t.Errorf("a nextPage section break should open page 2, got: %q", pages[1].Text)
				}
			} else if !strings.Contains(pages[0].Text, "PAGETWO") {
				t.Errorf("a continuous section break should keep the text together, got: %q", pages[0].Text)
			}
		})
	}
}
