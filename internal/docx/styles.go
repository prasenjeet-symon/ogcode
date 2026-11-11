package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"strconv"
	"strings"

	"golang.org/x/net/html/charset"
)

// styleDef is the subset of one <w:style> the reader needs: its kind, id,
// display name, the style it is based on, and its own paragraph properties.
type styleDef struct {
	Type    string     `xml:"type,attr"`
	StyleID string     `xml:"styleId,attr"`
	Name    styleValue `xml:"name"`
	BasedOn styleValue `xml:"basedOn"`
	PPr     *stylePPr  `xml:"pPr"`
}

// stylePPr holds the paragraph properties of a style: the outline level, which
// makes it a heading, and the page-break-before flag, which opens a page.
type stylePPr struct {
	OutlineLvl      *styleValue `xml:"outlineLvl"`
	PageBreakBefore *styleValue `xml:"pageBreakBefore"`
}

// styleValue is an element whose payload is its w:val attribute, e.g.
// <w:name w:val="heading 1"/> or <w:basedOn w:val="Heading1"/>.
type styleValue struct {
	Val string `xml:"val,attr"`
}

// stylesDoc is the subset of word/styles.xml the reader needs: the style
// definitions directly under the document root.
type stylesDoc struct {
	Styles []styleDef `xml:"style"`
}

// styleSheet is what word/styles.xml resolves to for the reader, per style id:
// whether paragraphs in that style are headings, and whether they open a page
// (<w:pPr><w:pageBreakBefore/></w:pPr> in the style — Word's "page break
// before" setting on a heading style). The zero value answers false for every
// lookup, which is what a missing styles part means.
type styleSheet struct {
	headings        map[string]bool
	pageBreakBefore map[string]bool
}

// readStyles resolves the archive's word/styles.xml. A missing or unparsable
// styles part yields the zero styleSheet, which only costs us the custom
// heading styles and style-level page breaks it would have recognised.
func readStyles(files []*zip.File) styleSheet {
	for _, f := range files {
		if f.Name != "word/styles.xml" {
			continue
		}
		data, err := readPart(f)
		if err != nil {
			return styleSheet{}
		}
		sheet, err := parseStyles(data)
		if err != nil {
			return styleSheet{}
		}
		return sheet
	}
	return styleSheet{}
}

// parseStyles returns what paragraphs in each style are: headings, and
// paragraphs that open a page. A paragraph style counts as a heading when its
// w:name is one the built-in check accepts (Heading 1…9, Title — Word writes
// those English names into every localised document, so a
// w:styleId="Ttulo1" whose name is "heading 1" is a heading) or when its pPr
// carries an outlineLvl 0…8; both properties are inherited through the style
// it is w:basedOn. Character and table styles share the name space but never
// flag a paragraph, so they are ignored.
func parseStyles(data []byte) (styleSheet, error) {
	var doc stylesDoc
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = charset.NewReaderLabel
	if err := dec.Decode(&doc); err != nil {
		return styleSheet{}, err
	}

	defs := make(map[string]*styleDef, len(doc.Styles))
	for i := range doc.Styles {
		def := &doc.Styles[i]
		if def.StyleID == "" {
			continue
		}
		if def.Type != "" && !strings.EqualFold(def.Type, "paragraph") {
			continue
		}
		defs[def.StyleID] = def
	}

	sheet := styleSheet{
		headings:        make(map[string]bool, len(defs)),
		pageBreakBefore: make(map[string]bool, len(defs)),
	}
	// resolving guards the basedOn walk: a cycle must resolve to "not a
	// heading" (and to "no page break"), not recurse forever.
	resolving := make(map[string]bool, len(defs))

	var isHeading func(id string) bool
	isHeading = func(id string) bool {
		if h, ok := sheet.headings[id]; ok {
			return h
		}
		def := defs[id]
		if def == nil || resolving[id] {
			return false
		}
		resolving[id] = true
		h := isHeadingStyle(def.Name.Val) || styleOutlineHeading(def.PPr)
		if !h && def.BasedOn.Val != "" {
			h = isHeading(def.BasedOn.Val)
		}
		resolving[id] = false
		sheet.headings[id] = h
		return h
	}

	var breaksPage func(id string) bool
	breaksPage = func(id string) bool {
		if b, ok := sheet.pageBreakBefore[id]; ok {
			return b
		}
		def := defs[id]
		if def == nil || resolving[id] {
			return false
		}
		resolving[id] = true
		b := stylePageBreakBefore(def.PPr)
		if !b && def.BasedOn.Val != "" {
			b = breaksPage(def.BasedOn.Val)
		}
		resolving[id] = false
		sheet.pageBreakBefore[id] = b
		return b
	}

	for id := range defs {
		isHeading(id)
		breaksPage(id)
	}
	return sheet, nil
}

// styleOutlineHeading reports whether a style's pPr pins an outline level 0…8
// (9 is body text, not a heading).
func styleOutlineHeading(ppr *stylePPr) bool {
	if ppr == nil || ppr.OutlineLvl == nil {
		return false
	}
	lvl, err := strconv.Atoi(strings.TrimSpace(ppr.OutlineLvl.Val))
	return err == nil && lvl >= 0 && lvl <= 8
}

// stylePageBreakBefore reports whether a style's pPr opens a page. The bare
// element is on; w:val="0"/"false"/"off" switches it off again.
func stylePageBreakBefore(ppr *stylePPr) bool {
	if ppr == nil || ppr.PageBreakBefore == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(ppr.PageBreakBefore.Val)) {
	case "0", "false", "off":
		return false
	}
	return true
}
