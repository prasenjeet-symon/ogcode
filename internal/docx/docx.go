package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/net/html/charset"
)

// WordsPerPage is the target word count per pseudo-page when a DOCX has no
// explicit page breaks. Paragraphs are accumulated until this threshold is
// reached, then a new pseudo-page starts. This keeps each pseudo-page's keyword
// corpus comparable to a PDF page.
const WordsPerPage = 500

// pageLookaheadWords is the hard ceiling a pseudo-page may grow to while it
// waits for the next heading (WordsPerPage * 3 / 2). The half-page of headroom
// is what lets a page absorb a short section instead of splitting it mid-way;
// the fixed ceiling is what stops a run of dense prose from growing one
// pseudo-page without bound. Once the accumulated words pass it, the page is
// flushed on word count exactly as it was before heading awareness existed.
const pageLookaheadWords = WordsPerPage * 3 / 2

// maxPartBytes is the ceiling on the uncompressed size of any single OOXML
// part the reader loads. A .docx is a ZIP archive, so a few KB of deflated XML
// can inflate to gigabytes (a ZIP bomb): without a cap one hostile document
// takes the whole process down. It is a variable, not a constant, only so tests
// can lower it.
var maxPartBytes int64 = 64 << 20

// PageText holds the raw extracted text for a single DOCX pseudo-page.
type PageText struct {
	PageNum int
	Text    string
}

// ExtractPages opens the DOCX (ZIP) archive at docxPath, parses
// word/document.xml, extracts paragraph text, and groups paragraphs into
// pseudo-pages. An explicit page break (<w:br w:type="page"/>) and an active
// <w:pPr><w:pageBreakBefore/></w:pPr> always start a new pseudo-page; otherwise
// paragraphs are chunked by word count (WordsPerPage), preferring a heading at
// which to start the next page. Heading styles are resolved through
// word/styles.xml when present, so a custom or localised heading style
// paginates like a built-in one. Footnote and endnote bodies are read from
// word/footnotes.xml and word/endnotes.xml when present and appended to the
// page that references them.
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

	data, err := readPart(docFile)
	if err != nil {
		return nil, fmt.Errorf("read word/document.xml: %w", err)
	}
	// An empty part is corruption, not a document without text: returning no
	// pages for it would surface later as an unexplained "no extractable
	// pages" from the indexer, with nothing naming the real cause.
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("docx %s: word/document.xml is empty", docxPath)
	}

	// Styles, footnotes and endnotes are auxiliary: a missing or unparsable
	// part must not fail the whole extraction. Without styles a custom heading
	// just is not flagged; without a notes part only the bodies are lost (the
	// [^N] / [^eN] markers stay in the page text).
	styles := readStyles(zr.File)

	paragraphs, err := parseBody(data, styles)
	if err != nil {
		return nil, fmt.Errorf("parse document.xml: %w", err)
	}

	notes := noteBodies{
		footnotes: readNotes(zr.File, "word/footnotes.xml"),
		endnotes:  readNotes(zr.File, "word/endnotes.xml"),
	}

	return groupPages(paragraphs, notes), nil
}

// readPart reads one archive part, refusing to inflate past maxPartBytes. The
// header's UncompressedSize64 is checked first, but a crafted archive can lie
// about it, so the read itself is also capped one byte past the limit.
func readPart(f *zip.File) ([]byte, error) {
	oversized := fmt.Errorf("part %s exceeds %d MiB uncompressed", f.Name, maxPartBytes>>20)
	if f.UncompressedSize64 > uint64(maxPartBytes) {
		return nil, oversized
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxPartBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxPartBytes {
		return nil, oversized
	}
	return transcodeUTF16(data), nil
}

// transcodeUTF16 converts a part that starts with a UTF-16 byte-order mark to
// UTF-8. encoding/xml can only read an XML declaration whose bytes are
// ASCII-compatible, so a UTF-16 part is transcoded before it reaches a decoder;
// its declaration is then dropped, because it still names UTF-16 and the
// decoder would use CharsetReader to convert the now-UTF-8 bytes a second time.
// A part without a BOM is returned untouched.
func transcodeUTF16(data []byte) []byte {
	label := ""
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		label = "utf-16le"
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		label = "utf-16be"
	default:
		return data
	}
	r, err := charset.NewReaderLabel(label, bytes.NewReader(data[2:]))
	if err != nil {
		return data
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return data
	}
	if bytes.HasPrefix(out, []byte("<?xml")) {
		if end := bytes.Index(out, []byte("?>")); end >= 0 {
			out = out[end+2:]
		}
	}
	return out
}

// noteBodies holds the note texts of both notes parts, keyed by the w:id the
// references in document.xml use. A missing part leaves its map empty.
type noteBodies struct {
	footnotes map[string]string
	endnotes  map[string]string
}

// body returns the text of a note, or "" when its part or id is missing.
func (b noteBodies) body(ref noteRef) string {
	if ref.endnote {
		return b.endnotes[ref.id]
	}
	return b.footnotes[ref.id]
}

// readNotes returns the bodies of one notes part (word/footnotes.xml or
// word/endnotes.xml) keyed by w:id, or an empty map when the archive has no
// usable part.
func readNotes(files []*zip.File, part string) map[string]string {
	for _, f := range files {
		if f.Name != part {
			continue
		}
		data, err := readPart(f)
		if err != nil {
			return map[string]string{}
		}
		notes, err := parseNotes(data)
		if err != nil {
			return map[string]string{}
		}
		return notes
	}
	return map[string]string{}
}

// noteRef identifies a footnote or endnote by its w:id inside its own part.
// The two parts number independently, so the kind is part of the identity.
type noteRef struct {
	endnote bool
	id      string
}

// paragraphNote is a note reference as it sits in a paragraph: the identity
// plus the display number assigned by first-reference order in the document.
type paragraphNote struct {
	ref noteRef
	num int
}

// marker renders the inline marker for a note: [^N] for a footnote, [^eN] for
// an endnote, so one flat text stream can carry both sequences unambiguously.
func (n paragraphNote) marker() string {
	if n.ref.endnote {
		return "[^e" + strconv.Itoa(n.num) + "]"
	}
	return "[^" + strconv.Itoa(n.num) + "]"
}

// paragraph holds the text of a single <w:p> element (or of one table row,
// whose cells are joined by tabs), whether it forces a page break (contains
// <w:br w:type="page"/>), whether it opens a page
// (<w:pPr><w:pageBreakBefore/></w:pPr>), whether it is a heading, and the notes
// it references.
type paragraph struct {
	text            string
	pageBreak       bool
	pageBreakBefore bool
	heading         bool
	notes           []paragraphNote
}

// cellFrame accumulates the paragraphs of one table cell. Nested tables push
// their own frames; a nested row's text joins the enclosing cell instead of
// starting a top-level paragraph.
type cellFrame struct {
	paras           []string
	notes           []paragraphNote
	heading         bool
	pageBreak       bool
	pageBreakBefore bool
}

// rowFrame accumulates the cells of one table row.
type rowFrame struct {
	cells           []string
	notes           []paragraphNote
	heading         bool
	pageBreak       bool
	pageBreakBefore bool
}

// parseBody decodes the OOXML body and returns a flat slice of paragraphs.
// Table rows are flattened into one paragraph per row ("Cell A\tCell B"), soft
// line breaks become "\n" inside the paragraph, headings are flagged by style
// name, resolved style table or outline level, and note references leave a
// [^N] / [^eN] marker numbered by first-reference order. styles is the resolved
// word/styles.xml; its zero value treats every style as ordinary body text
// that opens no page.
func parseBody(data []byte, styles styleSheet) ([]paragraph, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = charset.NewReaderLabel
	var paragraphs []paragraph
	// sawElement records that the part held at least one XML element. A part
	// that decodes to nothing but character data is not a document; without
	// this it would "parse" to no paragraphs, and the cause would surface only
	// much later as an empty extraction.
	sawElement := false

	var cur paragraph
	// text accumulates the current paragraph's text. A nested <w:p> (the body
	// of a text box inside a drawing) appends to the enclosing paragraph, so
	// the accumulator has to outlive a single <w:p> element; a Builder also
	// avoids recopying everything accumulated so far on every run. It is
	// materialised into cur.text when the paragraph is routed, and reset
	// wherever cur is.
	var text strings.Builder
	var inRun bool
	var inText bool
	// pDepth counts open <w:p> elements: a text box nests paragraphs, and only
	// the outermost one is a paragraph of its own.
	var pDepth int
	var pPrDepth int
	// revDepth counts open tracked-change property blocks (<w:pPrChange>,
	// <w:rPrChange>, ECMA-376 §17.13.5.32). The <w:pPr> / <w:rPr> they wrap
	// carry the formatting the revision reverts *to*, not the formatting in
	// force, so no property consumer below may read one while revDepth > 0.
	// encoding/xml synthesises the EndElement of a self-closing tag, so the
	// count stays balanced for the empty form (<w:pPrChange/>) too.
	var revDepth int
	// inSectPr is set inside <w:sectPr>, whose <w:type> names the kind of
	// section break.
	var inSectPr bool
	tableDepth := 0
	var cellStack []*cellFrame
	var rowStack []*rowFrame

	// Note numbers follow first-reference order, not w:id: ids skip after a
	// deletion, so [^3] would otherwise be the document's second note. The two
	// parts number independently.
	noteNums := map[noteRef]int{}
	footnoteNum, endnoteNum := 0, 0
	noteRefFor := func(endnote bool, id string) paragraphNote {
		ref := noteRef{endnote: endnote, id: id}
		num, ok := noteNums[ref]
		if !ok {
			if endnote {
				endnoteNum++
				num = endnoteNum
			} else {
				footnoteNum++
				num = footnoteNum
			}
			noteNums[ref] = num
		}
		return paragraphNote{ref: ref, num: num}
	}

	// flushCur appends the current paragraph to the top-level list and resets
	// it.
	flushCur := func() {
		cur.text = text.String()
		text.Reset()
		paragraphs = append(paragraphs, cur)
		cur = paragraph{}
	}

	// endParagraph routes the current paragraph to the top-level list, or into
	// the enclosing table cell when we are inside a table.
	endParagraph := func() {
		p := cur
		p.text = text.String()
		text.Reset()
		cur = paragraph{}
		// pPrDepth must not survive a closed paragraph: a malformed document
		// that leaves a <w:pPr> open would otherwise flag every later
		// paragraph as a heading or as opening a page.
		pPrDepth = 0
		if tableDepth == 0 {
			paragraphs = append(paragraphs, p)
			return
		}
		text := strings.TrimSpace(p.text)
		if len(cellStack) > 0 {
			cf := cellStack[len(cellStack)-1]
			if text != "" {
				cf.paras = append(cf.paras, text)
			}
			cf.heading = cf.heading || p.heading
			cf.pageBreak = cf.pageBreak || p.pageBreak
			cf.pageBreakBefore = cf.pageBreakBefore || p.pageBreakBefore
			cf.notes = append(cf.notes, p.notes...)
			return
		}
		// A paragraph directly inside a table but outside any cell (a drawing's
		// text box, say): keep its text as a standalone cell of the open row.
		if text != "" && len(rowStack) > 0 {
			rowStack[len(rowStack)-1].cells = append(rowStack[len(rowStack)-1].cells, text)
		}
	}

	// endCell closes the innermost table cell: its paragraphs are joined by a
	// single space and the result becomes the next cell of the open row.
	endCell := func() {
		if len(cellStack) == 0 {
			return
		}
		cf := cellStack[len(cellStack)-1]
		cellStack = cellStack[:len(cellStack)-1]

		text := strings.TrimSpace(strings.Join(cf.paras, " "))
		if len(rowStack) == 0 {
			return
		}
		rf := rowStack[len(rowStack)-1]
		// Keep empty cells as empty fields so the tabs still line the row up.
		rf.cells = append(rf.cells, text)
		rf.heading = rf.heading || cf.heading
		rf.pageBreak = rf.pageBreak || cf.pageBreak
		rf.pageBreakBefore = rf.pageBreakBefore || cf.pageBreakBefore
		rf.notes = append(rf.notes, cf.notes...)
	}

	// endRow closes the innermost table row and emits one paragraph for it: the
	// cells joined by tabs. Inside a nested table the row joins the enclosing
	// cell rather than starting a top-level paragraph.
	endRow := func() {
		if len(rowStack) == 0 {
			return
		}
		rf := rowStack[len(rowStack)-1]
		rowStack = rowStack[:len(rowStack)-1]

		text := strings.TrimSpace(strings.Join(rf.cells, "\t"))
		// An empty row is dropped — unless it carries the break it exists for:
		// a row holding nothing but <w:br w:type="page"/> is a deliberate page
		// break, and dropping it would lose the page boundary. A row with no
		// text and no break still emits nothing, so it cannot manufacture a
		// blank page.
		if text == "" && !rf.pageBreak && !rf.pageBreakBefore && len(rf.notes) == 0 {
			return
		}
		if tableDepth >= 2 && len(cellStack) > 0 {
			cf := cellStack[len(cellStack)-1]
			if text != "" {
				cf.paras = append(cf.paras, text)
			}
			cf.heading = cf.heading || rf.heading
			cf.pageBreak = cf.pageBreak || rf.pageBreak
			cf.pageBreakBefore = cf.pageBreakBefore || rf.pageBreakBefore
			cf.notes = append(cf.notes, rf.notes...)
			return
		}
		paragraphs = append(paragraphs, paragraph{
			text:            text,
			pageBreak:       rf.pageBreak,
			pageBreakBefore: rf.pageBreakBefore,
			heading:         rf.heading,
			notes:           rf.notes,
		})
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
			sawElement = true
			local := t.Name.Local
			switch local {
			case "p":
				// A <w:p> nested in another (a text box's body) contributes its
				// text to the enclosing paragraph, in document order; only the
				// outermost <w:p> starts a fresh paragraph.
				pDepth++
				if pDepth == 1 {
					cur = paragraph{}
					text.Reset()
				} else {
					// Keep the nested text from gluing onto the runs around it.
					text.WriteString(" ")
				}
			case "pPr":
				pPrDepth++
			case "pStyle":
				// w:pStyle only names a paragraph style; the style text itself
				// is never extracted, only used to flag the paragraph. A style
				// declared in word/styles.xml may be a heading under a name the
				// built-in check does not recognise (a localised "Ttulo1" or a
				// custom style based on Heading1, say) and it may carry the
				// "page break before" idiom that opens a page.
				if pPrDepth > 0 && revDepth == 0 {
					style := strings.TrimSpace(attrValue(t, "val"))
					if isHeadingStyle(style) || styles.headings[style] {
						cur.heading = true
					}
					// The paragraph's own properties stay authoritative: CT_PPr
					// orders pStyle before pageBreakBefore, so an explicit
					// property overrides what the style contributed.
					if styles.pageBreakBefore[style] {
						cur.pageBreakBefore = true
					}
				}
			case "outlineLvl":
				// Word writes <w:outlineLvl w:val="0"/> (…"8") inside pPr for
				// outline levels; 9 (body text) is not a heading.
				if pPrDepth > 0 && revDepth == 0 {
					// Word may pad the value (" 0 "); styles.go trims it, so the
					// body must too or the same value is a heading in a style and
					// body text here.
					if lvl, err := strconv.Atoi(strings.TrimSpace(attrValue(t, "val"))); err == nil && lvl >= 0 && lvl <= 8 {
						cur.heading = true
					}
				}
			case "pageBreakBefore":
				// An active pageBreakBefore opens a page: the paragraph itself
				// belongs to the new page. w:val="0"/"false"/"off" switches it
				// off again — including a page break the paragraph's style
				// contributed.
				if pPrDepth > 0 && revDepth == 0 {
					cur.pageBreakBefore = onOff(t)
				}
			case "pPrChange", "rPrChange":
				// The properties the revision reverts to follow inside.
				revDepth++
			case "sectPr":
				inSectPr = true
			case "type":
				// <w:sectPr><w:type w:val="nextPage"/> — a nextPage section
				// break ends the page exactly like an explicit page break. A
				// continuous (or odd/even) break does not start one here.
				if inSectPr && pPrDepth > 0 && revDepth == 0 &&
					strings.TrimSpace(attrValue(t, "val")) == "nextPage" {
					cur.pageBreak = true
				}
			case "tbl":
				tableDepth++
			case "tr":
				rowStack = append(rowStack, &rowFrame{})
			case "tc":
				cellStack = append(cellStack, &cellFrame{})
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
					// same <w:p> goes to the next page. Inside a table the row
					// paragraph carries the break instead.
					cur.pageBreak = true
					// Inside a nested paragraph the break marks the enclosing
					// paragraph; its own </w:p> ends it, so nothing may flush here.
					if tableDepth == 0 && pDepth <= 1 {
						flushCur()
					} else {
						// A table row cannot be closed mid-cell, so the break is
						// carried by the row; the text on either side of it must
						// still not glue into one word.
						text.WriteString("\n")
					}
				} else {
					// A soft line break keeps the text in one paragraph but must
					// not glue the two lines together.
					text.WriteString("\n")
				}
			case "cr":
				text.WriteString("\n")
			case "lastRenderedPageBreak":
				// Word records where it last laid the page out. It separates
				// text, but it is a rendering artifact — treating it as a page
				// boundary would make page numbers depend on who last opened
				// the file — so it only becomes a newline.
				text.WriteString("\n")
			case "tab":
				if inRun {
					text.WriteString("\t")
				}
			case "footnoteReference", "endnoteReference":
				id := attrValue(t, "id")
				if id != "" {
					n := noteRefFor(local == "endnoteReference", id)
					text.WriteString(n.marker())
					cur.notes = append(cur.notes, n)
				}
			}

		case xml.EndElement:
			local := t.Name.Local
			switch local {
			case "p":
				if pDepth > 0 {
					pDepth--
				}
				if pDepth == 0 {
					endParagraph()
				} else {
					text.WriteString(" ")
				}
			case "pPr":
				if pPrDepth > 0 {
					pPrDepth--
				}
			case "pPrChange", "rPrChange":
				if revDepth > 0 {
					revDepth--
				}
			case "sectPr":
				inSectPr = false
			case "tbl":
				if tableDepth > 0 {
					tableDepth--
				}
			case "tc":
				endCell()
			case "tr":
				endRow()
			case "r":
				inRun = false
			case "t":
				inText = false
			}

		case xml.CharData:
			if inText {
				text.WriteString(string(t))
			}
		}
	}

	if !sawElement {
		return nil, fmt.Errorf("word/document.xml is not WordprocessingML: no XML element found")
	}
	return paragraphs, nil
}

// attrValue returns the value of the attribute with the given local name.
func attrValue(t xml.StartElement, local string) string {
	for _, attr := range t.Attr {
		if attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
}

// onOff reports whether a boolean OOXML property element is on: the bare form
// (<w:pageBreakBefore/>) and val="1"/"true"/"on" mean on, val="0"/"false"/
// "off" mean off.
func onOff(t xml.StartElement) bool {
	switch strings.ToLower(strings.TrimSpace(attrValue(t, "val"))) {
	case "0", "false", "off":
		return false
	}
	return true
}

// isHeadingStyle reports whether a paragraph style name is a heading:
// Heading1…Heading9 (spacing and case tolerated) or Title.
func isHeadingStyle(style string) bool {
	s := strings.ToLower(strings.TrimSpace(style))
	if s == "title" {
		return true
	}
	s = strings.ReplaceAll(s, " ", "")
	if !strings.HasPrefix(s, "heading") {
		return false
	}
	level := s[len("heading"):]
	return len(level) == 1 && level[0] >= '1' && level[0] <= '9'
}

// parseNotes decodes a notes part (word/footnotes.xml or word/endnotes.xml)
// into id → text. Both parts share one shape: w:footnotes/w:footnote for
// footnotes, w:endnotes/w:endnote for endnotes. The separator,
// continuationSeparator and continuationNotice notes are layout furniture,
// not content, and are skipped.
func parseNotes(data []byte) (map[string]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	// A notes part carries its own encoding declaration, exactly like the body
	// (Word writes windows-1252 for a document saved in that code page).
	// Without a CharsetReader the decoder refuses the part outright and every
	// note body is lost with no diagnostic.
	dec.CharsetReader = charset.NewReaderLabel
	notes := map[string]string{}

	var id string
	var skip bool
	var inFootnote bool
	var inRun bool
	var inText bool
	var paras []string
	var sb strings.Builder

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
			switch t.Name.Local {
			case "footnote", "endnote":
				inFootnote = true
				id = attrValue(t, "id")
				typ := attrValue(t, "type")
				skip = typ == "separator" || typ == "continuationSeparator" || typ == "continuationNotice"
				paras = nil
				sb.Reset()
			case "r":
				inRun = true
			case "t":
				inText = true
			case "tab":
				if inRun {
					sb.WriteString("\t")
				}
			case "br", "cr":
				if inRun {
					sb.WriteString("\n")
				}
			}

		case xml.EndElement:
			switch t.Name.Local {
			case "footnote", "endnote":
				if inFootnote && !skip && id != "" {
					if text := strings.TrimSpace(strings.Join(paras, " ")); text != "" {
						notes[id] = text
					}
				}
				inFootnote = false
				paras = nil
				sb.Reset()
			case "p":
				if inFootnote && !skip {
					if text := strings.TrimSpace(sb.String()); text != "" {
						paras = append(paras, text)
					}
					sb.Reset()
				}
			case "r":
				inRun = false
			case "t":
				inText = false
			}

		case xml.CharData:
			if inText && inFootnote {
				sb.WriteString(string(t))
			}
		}
	}

	return notes, nil
}

// countWords estimates a paragraph's word count. Space-delimited fields carry
// most languages, but Chinese, Japanese and Thai prose has no spaces: 12,000
// runes would count as one word and the whole document would collapse into a
// single pseudo-page. A field with no space in it that is long enough to be a
// clause rather than a word paces at one word per six runes, so unspaced text
// paginates like spaced prose while ordinary prose keeps its field count.
func countWords(s string) int {
	words := 0
	for _, field := range strings.Fields(s) {
		if n := len([]rune(field)); n > 12 {
			words += n / 6
			continue
		}
		words++
	}
	return words
}

// groupPages converts the flat paragraph list into pseudo-pages. An explicit
// page break starts a new page immediately, and a paragraph carrying an active
// pageBreakBefore opens a new page (the accumulated text flushes before it).
// Otherwise, paragraphs accumulate until the word count first reaches
// WordsPerPage; at that point the page may still be extended up to
// pageLookaheadWords to reach a heading, which then starts the next page
// instead of being split away from its section. A page break paragraph itself
// is included in the page it terminates (i.e. the break ends the current page;
// the paragraph belongs to the current page, and the next paragraph starts a
// new one). Note bodies referenced by a page are appended to that page after
// its text.
func groupPages(paragraphs []paragraph, notes noteBodies) []PageText {
	if len(paragraphs) == 0 {
		return nil
	}

	var pages []PageText
	var sb strings.Builder
	var pageNotes []paragraphNote
	wordCount := 0
	pageNum := 1
	// deferUntil is the index of a heading paragraph that should start a new
	// page: the flush waits for it instead of firing at the threshold.
	deferUntil := -1
	// thresholdHit makes the lookahead run once per page, when the running
	// count first reaches WordsPerPage, not on every paragraph past it.
	thresholdHit := false

	flush := func() {
		deferUntil = -1
		thresholdHit = false

		text := strings.TrimSpace(sb.String())
		if text == "" && len(pages) == 0 {
			// Don't emit a leading empty page.
			return
		}
		if lines := noteLines(pageNotes, notes); len(lines) > 0 {
			if text == "" {
				text = strings.Join(lines, "\n")
			} else {
				text += "\n" + strings.Join(lines, "\n")
			}
		}
		pages = append(pages, PageText{PageNum: pageNum, Text: text})
		pageNum++
		sb.Reset()
		pageNotes = pageNotes[:0]
		wordCount = 0
	}

	for i, p := range paragraphs {
		// An active pageBreakBefore opens a page: the text accumulated so
		// far belongs to the previous page. A leading pageBreakBefore on an
		// empty first page must not manufacture an empty page.
		if p.pageBreakBefore && (strings.TrimSpace(sb.String()) != "" || len(pageNotes) > 0) {
			flush()
		}

		// The lookahead picked this heading to open the next page.
		if i == deferUntil {
			if strings.TrimSpace(sb.String()) != "" {
				flush()
			} else {
				// Never flush before a heading when the page is empty: an
				// empty page carries nothing and would only shift page
				// numbers.
				deferUntil = -1
			}
		}

		sb.WriteString(p.text)
		sb.WriteString("\n")
		wordCount += countWords(p.text)
		for _, ref := range p.notes {
			pageNotes = appendNoteRef(pageNotes, ref)
		}

		if p.pageBreak {
			// An explicit break always wins over the deferred heading flush.
			flush()
			continue
		}
		if !thresholdHit && wordCount >= WordsPerPage {
			thresholdHit = true
			if j := nextHeadingWithin(paragraphs, i, wordCount); j >= 0 {
				deferUntil = j
			} else {
				flush()
			}
		}
	}

	// Flush any remaining content.
	if strings.TrimSpace(sb.String()) != "" || len(pageNotes) > 0 {
		flush()
	}

	if len(pages) == 0 {
		// Edge case: all paragraphs were empty but we still had content.
		pages = append(pages, PageText{PageNum: 1, Text: ""})
	}

	return pages
}

// nextHeadingWithin looks ahead from paragraph from for the first heading that
// still fits the open page's budget (pageLookaheadWords). It returns that
// heading's index, or -1 when the page must be flushed where it is: no heading
// follows, the first heading lands past the budget, the budget is already
// spent, or an explicit page break (or a paragraph that opens its own page)
// intervenes — that paragraph pages the document anyway.
func nextHeadingWithin(paragraphs []paragraph, from, base int) int {
	acc := base
	for j := from + 1; j < len(paragraphs); j++ {
		acc += countWords(paragraphs[j].text)
		// An explicit break pages the document at its own paragraph, so
		// deferring past it would only split at the break anyway.
		if paragraphs[j].pageBreak || paragraphs[j].pageBreakBefore || acc > pageLookaheadWords {
			return -1
		}
		if paragraphs[j].heading {
			return j
		}
	}
	return -1
}

// appendNoteRef records a note reference in first-use order, ignoring repeats
// so a note referenced twice is only spelled out once per page.
func appendNoteRef(refs []paragraphNote, ref paragraphNote) []paragraphNote {
	for _, r := range refs {
		if r.ref == ref.ref {
			return refs
		}
	}
	return append(refs, ref)
}

// noteLines renders the "[^N]: text" / "[^eN]: text" lines for the notes a page
// references. References without a body (no notes part, or a body that is only
// a separator) are left as markers only.
func noteLines(refs []paragraphNote, notes noteBodies) []string {
	lines := make([]string, 0, len(refs))
	for _, ref := range refs {
		text := notes.body(ref.ref)
		if text == "" {
			continue
		}
		lines = append(lines, ref.marker()+": "+text)
	}
	return lines
}
