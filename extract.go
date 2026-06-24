package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/api"
)

// Item is one PO line item.
type Item struct {
	DocNo    string
	Delivery string
	Code     string
	Name     string
	Qty      string
	Unit     string
	Pack     int
	Packed   bool // the order unit had a parenthesised pack size, e.g. ลัง(6)
	Price    string
}

// Column x-boundaries, taken from the SAP-generated PO table header (points).
// [lo, hi)
var (
	colCode  = [2]float64{0, 54}
	colName  = [2]float64{54, 219}
	colQty   = [2]float64{388, 470}
	colPrice = [2]float64{470, 545}
)

var (
	rePOPage    = regexp.MustCompile(`PO\.No\.`)
	reDocNo     = regexp.MustCompile(`เลขที่เอกสาร\s*[:：]\s*([0-9]+)`)
	reDelivery  = regexp.MustCompile(`สถานที่ส่งสินค้า\s*[:：]\s*(.+)`)
	reCode      = regexp.MustCompile(`^\d{6,}$`)
	reNum       = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?`)
	rePack      = regexp.MustCompile(`\((\d+)\)`)
	reTrailSpac = regexp.MustCompile(`\s+`)
)

// normalizePDF runs the PDF through pdfcpu so strict readers can parse it.
// Returns the path of a temp normalized file (the caller removes it).
func normalizePDF(path string) (string, error) {
	tmp, err := os.CreateTemp("", "popack-norm-*.pdf")
	if err != nil {
		return "", err
	}
	tmp.Close()
	if err := pdfcpu.OptimizeFile(path, tmp.Name(), nil); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

type frag struct {
	x float64
	s string
}

type textLine struct {
	y     float64
	frags []frag // stream order preserved
}

// clusterLines groups char fragments into visual lines by Y (stream order kept).
func clusterLines(p pdf.Page) []textLine {
	const tol = 3.5
	var lines []textLine
	for _, t := range p.Content().Text {
		if t.S == "�" { // drop spurious replacement glyphs
			continue
		}
		placed := false
		for i := range lines {
			if t.Y-lines[i].y < tol && lines[i].y-t.Y < tol {
				lines[i].frags = append(lines[i].frags, frag{t.X, t.S})
				placed = true
				break
			}
		}
		if !placed {
			lines = append(lines, textLine{y: t.Y, frags: []frag{{t.X, t.S}}})
		}
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].y > lines[j].y }) // top first
	return lines
}

func (l textLine) col(lo, hi float64) string {
	var b strings.Builder
	for _, f := range l.frags {
		if f.x >= lo && f.x < hi {
			b.WriteString(f.s)
		}
	}
	return strings.TrimSpace(b.String())
}

func (l textLine) text() string {
	var b strings.Builder
	for _, f := range l.frags {
		b.WriteString(f.s)
	}
	return strings.TrimSpace(b.String())
}

func firstNum(s string) string { return reNum.FindString(s) }

// parseQtyUnit reads the จำนวนหน่วย cell, e.g. "36.00 ชิ้น" or "1.00 ลัง(12)".
func parseQtyUnit(qtyCell string) (qty, unit string, pack int, packed bool) {
	pack = 1
	qty = firstNum(qtyCell)
	rest := qtyCell
	if qty != "" {
		if i := strings.Index(rest, qty); i >= 0 {
			rest = rest[i+len(qty):]
		}
	}
	rest = strings.ReplaceAll(rest, " ", "")
	if m := rePack.FindStringSubmatch(rest); m != nil {
		pack, _ = strconv.Atoi(m[1])
		packed = true
	}
	unit = strings.TrimSpace(rePack.ReplaceAllString(rest, ""))
	return
}

// extractItems reads the line items from an already-open normalized PO reader.
func extractItems(r *pdf.Reader) []Item {
	var items []Item
	var lastDoc, lastDelivery string

	for pno := 1; pno <= r.NumPage(); pno++ {
		p := r.Page(pno)
		if p.V.IsNull() {
			continue
		}
		lines := clusterLines(p)
		var full strings.Builder
		for _, l := range lines {
			full.WriteString(l.text())
			full.WriteByte('\n')
		}
		pageText := full.String()
		if !rePOPage.MatchString(pageText) {
			continue // not a PO content page
		}

		doc := ""
		if m := reDocNo.FindStringSubmatch(pageText); m != nil {
			doc = m[1]
		}
		delivery := ""
		if m := reDelivery.FindStringSubmatch(pageText); m != nil {
			delivery = reTrailSpac.ReplaceAllString(strings.TrimSpace(m[1]), " ")
		}
		if doc == "" {
			doc = lastDoc
		}
		if delivery == "" {
			delivery = lastDelivery
		}
		lastDoc, lastDelivery = doc, delivery

		for _, l := range lines {
			code := l.col(colCode[0], colCode[1])
			if !reCode.MatchString(code) {
				continue
			}
			qty, unit, pack, packed := parseQtyUnit(l.col(colQty[0], colQty[1]))
			items = append(items, Item{
				DocNo:    doc,
				Delivery: delivery,
				Code:     code,
				Name:     l.col(colName[0], colName[1]),
				Qty:      qty,
				Unit:     unit,
				Pack:     pack,
				Packed:   packed,
				Price:    firstNum(l.col(colPrice[0], colPrice[1])),
			})
		}
	}
	return items
}

func parseNum(s string) (float64, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func baseName(p string) string {
	return strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
}
