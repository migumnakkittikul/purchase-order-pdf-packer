package main

import (
	"encoding/hex"
	"io"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"
)

var reHexTok = regexp.MustCompile(`<([0-9A-Fa-f]+)>`)

// decodeContent returns the page's content stream with every <hex> text token
// hex-decoded and concatenated. The product code / PO number are drawn with an
// ASCII-mapped font, so their digits appear verbatim here - which lets us find
// the label's product WITHOUT interpreting the stream (ledongthuc's interpreter
// panics on these pages because of their inline images).
func decodeContent(p pdf.Page) (out string) {
	defer func() { _ = recover() }() // be safe against odd stream objects
	var b strings.Builder
	add := func(v pdf.Value) {
		r := v.Reader()
		if r == nil {
			return
		}
		data, _ := io.ReadAll(r)
		for _, m := range reHexTok.FindAllSubmatch(data, -1) {
			if d, err := hex.DecodeString(string(m[1])); err == nil {
				b.Write(d)
			}
		}
	}
	c := p.V.Key("Contents")
	if c.Kind() == pdf.Array {
		for i := 0; i < c.Len(); i++ {
			add(c.Index(i))
		}
	} else {
		add(c)
	}
	return b.String()
}

// labelLoc is where a product's label lives: its page and that page's size.
type labelLoc struct {
	Page int
	W, H float64
}

func pageDims(p pdf.Page) (w, h float64) {
	mb := p.V.Key("MediaBox")
	if mb.Len() != 4 {
		return 0, 0
	}
	return mb.Index(2).Float64() - mb.Index(0).Float64(),
		mb.Index(3).Float64() - mb.Index(1).Float64()
}

// safePageText returns a page's interpreted text, recovering from the panics
// ledongthuc throws on the SAP pages. Used as a fallback for ordinary PDFs
// whose text isn't in the hex-mapped font decodeContent targets.
func safePageText(p pdf.Page) (s string) {
	defer func() { _ = recover() }()
	var b strings.Builder
	for _, t := range p.Content().Text {
		if t.S != "�" {
			b.WriteString(t.S)
		}
	}
	return b.String()
}

// scanLabels maps each item code to the label page that carries it. Label pages
// are the landscape pages; each shows exactly one product code from the PO.
func scanLabels(r *pdf.Reader, itemCodes []string) map[string]labelLoc {
	codeSet := make(map[string]bool, len(itemCodes))
	for _, c := range itemCodes {
		codeSet[c] = true
	}
	loc := map[string]labelLoc{}
	for pno := 1; pno <= r.NumPage(); pno++ {
		p := r.Page(pno)
		if p.V.IsNull() {
			continue
		}
		w, h := pageDims(p)
		if w <= h { // labels are landscape
			continue
		}
		blob := decodeContent(p)
		var hits []string
		for code := range codeSet {
			if strings.Contains(blob, code) {
				hits = append(hits, code)
			}
		}
		if len(hits) == 0 {
			text := safePageText(p) // ordinary (non-SAP) PDFs
			for code := range codeSet {
				if strings.Contains(text, code) {
					hits = append(hits, code)
				}
			}
		}
		if len(hits) != 1 {
			continue // 0 = not a product label; >1 = ambiguous, skip
		}
		if _, seen := loc[hits[0]]; !seen {
			loc[hits[0]] = labelLoc{Page: pno, W: w, H: h} // first page wins
		}
	}
	return loc
}
