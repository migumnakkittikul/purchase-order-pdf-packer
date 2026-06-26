package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"github.com/xuri/excelize/v2"
)

func init() { model.ConfigPath = "disable" } // no per-user config dir on the target PC

func newConf() *model.Configuration {
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	conf.Unit = types.POINTS // interpret all dimensions in points
	// Merging many single-label files builds a deep page tree; the default
	// limit (100) rejects large label batches. We control these intermediate
	// files, so raise it well beyond any realistic label count.
	conf.Limits.MaxRecursionDepth = 1_000_000
	// Don't add a per-file bookmark on merge - with hundreds of label files it
	// produces an invalid outline/Name tree ("invalid Name ref"), and we don't
	// want bookmarks on the label sheets anyway.
	conf.CreateBookmarks = false
	return conf
}

// labelsNeeded - the label policy:
// if the order unit has a parenthesised pack size (e.g. ลัง(6)), print one
// label per ordered unit (จำนวน); otherwise print one label for the line.
// Example: จำนวน 10, unit ลัง(6) -> 10 labels.
func labelsNeeded(it Item) int {
	if !it.Packed {
		return 1
	}
	q, ok := parseNum(it.Qty)
	if !ok {
		return 1
	}
	n := int(math.Ceil(q))
	if n < 1 {
		n = 1
	}
	return n
}

// ---- number formatting ---------------------------------------------------- //

func addCommas(intPart string) string {
	neg := strings.HasPrefix(intPart, "-")
	intPart = strings.TrimPrefix(intPart, "-")
	n := len(intPart)
	if n <= 3 {
		if neg {
			return "-" + intPart
		}
		return intPart
	}
	var b strings.Builder
	pre := n % 3
	if pre > 0 {
		b.WriteString(intPart[:pre])
	}
	for i := pre; i < n; i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(intPart[i : i+3])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func fmtNum(v float64, dec int) string {
	s := strconv.FormatFloat(v, 'f', dec, 64)
	if dec > 0 {
		parts := strings.SplitN(s, ".", 2)
		return addCommas(parts[0]) + "." + parts[1]
	}
	return addCommas(s)
}

func fmtQty(it Item) string {
	if v, ok := parseNum(it.Qty); ok {
		return fmtNum(v, 0)
	}
	return it.Qty
}

func fmtTotal(it Item) string {
	if v, ok := parseNum(it.Qty); ok {
		return fmtNum(v*float64(it.Pack), 0)
	}
	return ""
}

func fmtMoney(price string) string {
	if v, ok := parseNum(price); ok {
		return fmtNum(v, 2)
	}
	return price
}

// ---- label crop + pack ---------------------------------------------------- //

// labelTile is one cropped single-label PDF plus its source page size.
type labelTile struct {
	file string
	w, h float64
}

// buildPOLabels produces ONE multi-page PDF of this PO's needed labels, in
// order with repetition for copies, each cropped tight to its border box.
// It uses just 3 pdfcpu calls regardless of label count (was 2*distinct + copies).
func buildPOLabels(poDir, norm string, items []Item, loc map[string]labelLoc,
	counts []int, conf *model.Configuration) (*labelTile, error) {

	// distinct label pages needed (first-seen order) + the label page size
	var distinct []int
	seen := map[int]bool{}
	var w, h float64
	for i, it := range items {
		l, ok := loc[it.Code]
		if !ok || counts[i] <= 0 {
			continue
		}
		if !seen[l.Page] {
			seen[l.Page] = true
			distinct = append(distinct, l.Page)
			w, h = l.W, l.H
		}
	}
	if len(distinct) == 0 {
		return nil, nil
	}

	// 1) collect all needed label pages in one pass
	sel := make([]string, len(distinct))
	for i, p := range distinct {
		sel[i] = strconv.Itoa(p)
	}
	collected := filepath.Join(poDir, "collected.pdf")
	if err := pdfapi.CollectFile(norm, collected, sel, conf); err != nil {
		return nil, fmt.Errorf("collect labels: %w", err)
	}

	// 2) crop every collected page to the label's border box (same box for all).
	// Tight inside the top-left quadrant (which has built-in whitespace) so it
	// prints at original size with even margins. Insets from the SAP template.
	box, err := pdfapi.Box(fmt.Sprintf("[%g %g %g %g]", 11.0, h/2-3, w/2-21, h-25), types.POINTS)
	if err != nil {
		return nil, err
	}
	cropped := filepath.Join(poDir, "cropped.pdf")
	if err := pdfapi.CropFile(collected, cropped, nil, box, conf); err != nil {
		return nil, fmt.Errorf("crop labels: %w", err)
	}

	// 3) expand to the final order with repetition (collect supports both)
	idxOf := map[int]int{} // original page -> 1-based index within cropped.pdf
	for i, p := range distinct {
		idxOf[p] = i + 1
	}
	var ord []string
	for i, it := range items {
		l, ok := loc[it.Code]
		if !ok || counts[i] <= 0 {
			continue
		}
		for k := 0; k < counts[i]; k++ {
			ord = append(ord, strconv.Itoa(idxOf[l.Page]))
		}
	}
	ordered := filepath.Join(poDir, "ordered.pdf")
	if err := pdfapi.CollectFile(cropped, ordered, ord, conf); err != nil {
		return nil, fmt.Errorf("order labels: %w", err)
	}
	return &labelTile{file: ordered, w: w, h: h}, nil
}

// packLabels merges ALL collected label tiles and packs them 4-up onto sheets.
// Returns the sheet PDF path ("" if no tiles).
func packLabels(tmpDir string, tiles []labelTile, conf *model.Configuration) (string, error) {
	if len(tiles) == 0 {
		return "", nil
	}
	files := make([]string, len(tiles))
	for i, t := range tiles {
		files[i] = t.file
	}
	merged := filepath.Join(tmpDir, "labels_merged.pdf")
	if err := pdfapi.MergeCreateFile(files, merged, false, conf); err != nil {
		return "", fmt.Errorf("merge labels: %w", err)
	}
	nup, err := pdfapi.PDFNUpConfig(4, "border:off, margin:0", conf)
	if err != nil {
		return "", err
	}
	// set the sheet size in points directly (avoid the description's unit parsing)
	nup.PageDim = &types.Dim{Width: tiles[0].w, Height: tiles[0].h}
	nup.PageSize = ""
	nup.UserDim = true
	nup.InpUnit = types.POINTS
	nup.Margin = 12 // labels print at ~original size; this just spaces them out
	sheets := filepath.Join(tmpDir, "label_sheets.pdf")
	if err := pdfapi.NUpFile([]string{merged}, sheets, nil, nup, conf); err != nil {
		return "", fmt.Errorf("nup labels: %w", err)
	}
	return sheets, nil
}

// ---- xlsx ----------------------------------------------------------------- //

var xlsxHeaders = []string{
	"เลขที่เอกสาร", "สถานที่ส่งสินค้า", "รหัสสินค้า", "ชื่อสินค้า",
	"จำนวน", "หน่วย", "จำนวนรวม (ชิ้น)", "ราคา",
}

func writeXLSX(items []Item, path string) error {
	fx := excelize.NewFile()
	defer fx.Close()
	sh := "PO Items"
	fx.SetSheetName("Sheet1", sh)
	style, _ := fx.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"1F4E78"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
	})
	for i, h := range xlsxHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		fx.SetCellValue(sh, cell, h)
	}
	fx.SetCellStyle(sh, "A1", "H1", style)
	for r, it := range items {
		row := r + 2
		q, _ := parseNum(it.Qty)
		pr, _ := parseNum(it.Price)
		vals := []any{it.DocNo, it.Delivery, it.Code, it.Name, q, it.Unit, q * float64(it.Pack), pr}
		for c, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(c+1, row)
			fx.SetCellValue(sh, cell, v)
		}
	}
	widths := []float64{16, 22, 14, 46, 9, 9, 14, 12}
	for i, w := range widths {
		col, _ := excelize.ColumnNumberToName(i + 1)
		fx.SetColWidth(sh, col, col, w)
	}
	fx.SetPanes(sh, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	return fx.SaveAs(path)
}

// ---- full conversion ------------------------------------------------------ //

// convert builds one combined PDF (per PO: summary table + needed labels) from
// the inputs, and writes an .xlsx copy. Returns (#PDF sections, warnings, err).
// progress (if non-nil) is called as work proceeds: progress(done, total, msg).
func convert(inputs []string, outPDF string, writeXlsx bool,
	progress func(done, total int, msg string)) (int, []string, error) {

	total := len(inputs) + 1 // last step = combine labels + save
	prog := func(done int, msg string) {
		if progress != nil {
			progress(done, total, msg)
		}
	}

	conf := newConf()
	tmpDir, err := os.MkdirTemp("", "popack")
	if err != nil {
		return 0, nil, err
	}
	defer os.RemoveAll(tmpDir)
	fam := loadFontFamily()

	var tablePaths []string  // all PO summary pages (top of the output)
	var allTiles []labelTile // all labels from all POs (combined at the bottom)
	var allRows []Item
	var warnings []string

	for idx, in := range inputs {
		name := filepath.Base(in)
		prog(idx, "Processing "+name)
		poDir := filepath.Join(tmpDir, fmt.Sprintf("po%d", idx))
		if err := os.MkdirAll(poDir, 0o755); err != nil {
			return 0, nil, err
		}
		norm, err := normalizePDF(in)
		if err != nil {
			warnings = append(warnings, name+": could not read PDF; skipped.")
			continue
		}
		f, r, err := pdf.Open(norm)
		if err != nil {
			os.Remove(norm)
			warnings = append(warnings, name+": could not open PDF; skipped.")
			continue
		}
		items, info := extractItems(r)
		codes := make([]string, len(items))
		for i, it := range items {
			codes[i] = it.Code
		}
		loc := scanLabels(r, codes)
		f.Close()

		if len(items) == 0 {
			os.Remove(norm)
			warnings = append(warnings, name+": no PO line items found (different layout or scanned PDF).")
			continue
		}
		allRows = append(allRows, items...)

		// Safety: catch a silently incomplete extraction. Cross-check the sum of
		// qty*price against the PO's own printed subtotal, and flag blank fields.
		var computed float64
		missing := 0
		for _, it := range items {
			q, okq := parseNum(it.Qty)
			p, okp := parseNum(it.Price)
			if okq && okp {
				computed += q * p
			}
			if it.Code == "" || !okq || !okp {
				missing++
			}
		}
		if missing > 0 {
			warnings = append(warnings, fmt.Sprintf("%s: %d line(s) missing code/qty/price - verify.", name, missing))
		}
		if info.PrintedGoods > 0 && math.Abs(computed-info.PrintedGoods) > 0.5 {
			warnings = append(warnings, fmt.Sprintf("%s: extracted total %s != PO total %s - some items may be missing; verify.",
				name, fmtNum(computed, 2), fmtNum(info.PrintedGoods, 2)))
		}

		counts := make([]int, len(items))
		for i, it := range items {
			if _, ok := loc[it.Code]; !ok {
				counts[i] = -1
				warnings = append(warnings, fmt.Sprintf("%s: no label page for item %s.", name, it.Code))
				continue
			}
			counts[i] = labelsNeeded(it)
		}

		docNo := items[0].DocNo
		if docNo == "" {
			docNo = baseName(in)
		}
		tps, err := renderTablePages(poDir, docNo, items, counts, fam)
		if err != nil {
			os.Remove(norm)
			warnings = append(warnings, name+": table render failed: "+err.Error())
			continue
		}
		tile, err := buildPOLabels(poDir, norm, items, loc, counts, conf)
		os.Remove(norm)
		if err != nil {
			warnings = append(warnings, name+": label build failed: "+err.Error())
		}
		tablePaths = append(tablePaths, tps...)
		if tile != nil {
			allTiles = append(allTiles, *tile)
		}
	}

	if len(tablePaths) == 0 {
		return 0, warnings, fmt.Errorf("nothing produced - no readable PO pages in the input")
	}
	prog(len(inputs), "Combining labels and saving...")
	// all summaries first, then all labels packed together at the bottom
	sections := append([]string{}, tablePaths...)
	sheets, err := packLabels(tmpDir, allTiles, conf)
	if err != nil {
		warnings = append(warnings, "label packing failed: "+err.Error())
	} else if sheets != "" {
		sections = append(sections, sheets)
	}
	if err := pdfapi.MergeCreateFile(sections, outPDF, false, conf); err != nil {
		return 0, warnings, fmt.Errorf("final merge: %w", err)
	}
	if writeXlsx && len(allRows) > 0 {
		xlsx := strings.TrimSuffix(outPDF, filepath.Ext(outPDF)) + ".xlsx"
		if err := writeXLSX(allRows, xlsx); err != nil {
			warnings = append(warnings, "Excel copy not written: "+err.Error())
		}
	}
	prog(total, "Done")
	return len(sections), warnings, nil
}
