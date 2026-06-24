package main

import (
	"fmt"
	"image/color"
	"path/filepath"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers"
)

const (
	pageW  = 842.0
	pageH  = 595.0
	rowH   = 27.0
	hdrH   = 29.0
	topPad = 100.0 // space from page top down to the header row
	botPad = 40.0  // bottom page margin
)

var (
	cInk    = color.RGBA{26, 28, 31, 255}
	cHead   = color.RGBA{31, 78, 120, 255}
	cGrid   = color.RGBA{205, 205, 205, 255}
	cZebra  = color.RGBA{244, 246, 249, 255}
	cWhite  = color.RGBA{255, 255, 255, 255}
	cGray   = color.RGBA{110, 110, 110, 255}
	cTransp = color.RGBA{0, 0, 0, 0}
)

type colSpec struct {
	head  string
	w     float64
	align canvas.TextAlign
	val   func(it Item, idx, count int) string
}

func summaryColumns() []colSpec {
	return []colSpec{
		{"#", 26, canvas.Center, func(it Item, i, c int) string { return fmt.Sprintf("%d", i+1) }},
		{"รหัสสินค้า", 66, canvas.Left, func(it Item, i, c int) string { return it.Code }},
		{"ชื่อสินค้า", 316, canvas.Left, func(it Item, i, c int) string { return it.Name }},
		{"ส่งไปที่", 92, canvas.Left, func(it Item, i, c int) string { return it.Delivery }},
		{"จำนวน", 48, canvas.Right, func(it Item, i, c int) string { return fmtQty(it) }},
		{"หน่วย", 46, canvas.Center, func(it Item, i, c int) string { return it.Unit }},
		{"รวม(ชิ้น)", 58, canvas.Right, func(it Item, i, c int) string { return fmtTotal(it) }},
		{"ราคา", 58, canvas.Right, func(it Item, i, c int) string { return fmtMoney(it.Price) }},
		{"ป้าย", 38, canvas.Right, func(it Item, i, c int) string {
			if c < 0 {
				return "-"
			}
			return fmt.Sprintf("%d", c)
		}},
	}
}

func loadFontFamily() *canvas.FontFamily {
	fam := canvas.NewFontFamily("th")
	fam.LoadFont(sarabunRegular, 0, canvas.FontRegular)
	fam.LoadFont(sarabunBold, 0, canvas.FontBold)
	return fam
}

// renderTablePages renders the order summary; returns one PDF path per page.
// counts[i] = labels for item i (-1 means no label page found).
func renderTablePages(tmpDir, docNo string, items []Item, counts []int, fam *canvas.FontFamily) ([]string, error) {
	cols := summaryColumns()
	totalW := 0.0
	for _, c := range cols {
		totalW += c.w
	}
	top := pageH - topPad // top edge (y-up) of the header row
	perPage := int((top - botPad - hdrH) / rowH)
	if perPage < 1 {
		perPage = 1
	}
	nPages := (len(items) + perPage - 1) / perPage
	if nPages < 1 {
		nPages = 1
	}
	totalLabels := 0
	for _, c := range counts {
		if c > 0 {
			totalLabels += c
		}
	}

	var paths []string
	for pg := 0; pg < nPages; pg++ {
		lo := pg * perPage
		hi := lo + perPage
		if hi > len(items) {
			hi = len(items)
		}
		path := filepath.Join(tmpDir, fmt.Sprintf("table_%s_%d.pdf", docNo, pg))
		if err := renderOnePage(path, docNo, items[lo:hi], counts[lo:hi], lo,
			cols, totalW, top, len(items), totalLabels, pg+1, nPages, fam); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func renderOnePage(path, docNo string, items []Item, counts []int, idxOff int,
	cols []colSpec, totalW, top float64, totalItems, totalLabels, pgNo, pgTot int,
	fam *canvas.FontFamily) error {

	// canvas authors in millimetres and Face() takes points (converted to mm
	// internally). So: page in mm, coordinates multiplied by S (pt->mm), and
	// font sizes passed in points as-is. (No ctx.Scale - that double-shrinks text.)
	const S = 25.4 / 72.0
	c := canvas.New(pageW*S, pageH*S)
	ctx := canvas.NewContext(c)

	fill := func(x, y, w, h float64, col color.Color) {
		ctx.SetFillColor(col)
		ctx.SetStrokeColor(cTransp)
		ctx.DrawPath(x*S, y*S, canvas.Rectangle(w*S, h*S))
	}
	hline := func(x, y, w float64) { fill(x, y, w, 0.5, cGrid) }
	vline := func(x, y, h float64) { fill(x, y, 0.5, h, cGrid) }
	cell := func(s string, x, yTop, w, h float64, al canvas.TextAlign, face *canvas.FontFace) {
		if s == "" {
			return
		}
		box := canvas.NewTextBox(face, s, w*S, h*S, al, canvas.Center, nil)
		ctx.DrawText(x*S, yTop*S, box)
	}

	left := (pageW - totalW) / 2 // center the table horizontally

	faceTitle := fam.Face(17, cInk, canvas.FontBold)
	faceSub := fam.Face(10.5, cGray, canvas.FontRegular)
	faceHdr := fam.Face(10.5, cWhite, canvas.FontBold)
	faceCell := fam.Face(10, cInk, canvas.FontRegular)

	// title + subtitle (baseline-anchored; kept clear of the top edge)
	ctx.DrawText(left*S, (pageH-50)*S, canvas.NewTextLine(faceTitle,
		fmt.Sprintf("ใบสั่งซื้อ  เลขที่ %s", docNo), canvas.Left))
	ctx.DrawText(left*S, (pageH-70)*S, canvas.NewTextLine(faceSub,
		fmt.Sprintf("%d รายการ   ป้ายที่ต้องพิมพ์ %d ใบ   หน้า %d/%d",
			totalItems, totalLabels, pgNo, pgTot), canvas.Left))

	// header row background + labels
	fill(left, top-hdrH, totalW, hdrH, cHead)
	x := left
	for _, col := range cols {
		cell(col.head, x+5, top, col.w-10, hdrH, canvas.Center, faceHdr)
		x += col.w
	}

	// body
	for i := range items {
		yTop := (top - hdrH) - float64(i)*rowH
		if i%2 == 1 {
			fill(left, yTop-rowH, totalW, rowH, cZebra)
		}
		x = left
		for _, col := range cols {
			cell(col.val(items[i], idxOff+i, counts[i]), x+6, yTop, col.w-12, rowH, col.align, faceCell)
			x += col.w
		}
	}

	// grid lines
	bottom := (top - hdrH) - float64(len(items))*rowH
	hline(left, top, totalW)      // table top
	hline(left, top-hdrH, totalW) // under header
	for i := 0; i <= len(items); i++ {
		hline(left, (top-hdrH)-float64(i)*rowH, totalW)
	}
	x = left
	for i := 0; i <= len(cols); i++ {
		vline(x, bottom, top-bottom)
		if i < len(cols) {
			x += cols[i].w
		}
	}

	return c.WriteFile(path, renderers.PDF())
}
