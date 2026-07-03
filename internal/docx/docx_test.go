package docx

import (
	"archive/zip"
	"os"
	"strings"
	"testing"
)

func createTestDocx(t *testing.T, docXML string) string {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "test*.docx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	w := zip.NewWriter(tmpFile)
	f, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(docXML)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	return tmpFile.Name()
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
