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

// cropPOLabels crops this PO's distinct label pages to their border boxes (one
// page each) and returns that file plus a map from product code to its 1-based
// page within it. Ordering and repetition happen globally afterwards, so labels
// can be grouped by branch across POs. Returns ("", nil, 0, nil) if no labels.
func cropPOLabels(poDir, norm string, items []Item, loc map[string]labelLoc,
	counts []int, conf *model.Configuration) (cropped string, codeToPage map[string]int, npages int, err error) {

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
		return "", nil, 0, nil
	}

	// collect the needed label pages in one pass
	sel := make([]string, len(distinct))
	for i, p := range distinct {
		sel[i] = strconv.Itoa(p)
	}
	collected := filepath.Join(poDir, "collected.pdf")
	if err := pdfapi.CollectFile(norm, collected, sel, conf); err != nil {
		return "", nil, 0, fmt.Errorf("collect labels: %w", err)
	}

	// crop every page to the label's border box (same box for all). Cropping a
	// little above the bottom border drops the border line and the stray
	// cut-line tick marks from the label below. Insets from the SAP template.
	box, err := pdfapi.Box(fmt.Sprintf("[%g %g %g %g]", 11.0, h/2+3, w/2-21, h-25), types.POINTS)
	if err != nil {
		return "", nil, 0, err
	}
	cropped = filepath.Join(poDir, "cropped.pdf")
	if err := pdfapi.CropFile(collected, cropped, nil, box, conf); err != nil {
		return "", nil, 0, fmt.Errorf("crop labels: %w", err)
	}

	pageOf := map[int]int{} // original page -> 1-based page within cropped.pdf
	for i, p := range distinct {
		pageOf[p] = i + 1
	}
	codeToPage = map[string]int{}
	for _, it := range items {
		if l, ok := loc[it.Code]; ok {
			if lp, ok2 := pageOf[l.Page]; ok2 {
				codeToPage[it.Code] = lp
			}
		}
	}
	return cropped, codeToPage, len(distinct), nil
}

// stickerSide is the sticker page edge (10 cm) and stickerMargin the thin inner
// margin, both in PDF points. For a sticker printer: one label per page.
const (
	mmToPt        = 72.0 / 25.4
	stickerSide   = 100 * mmToPt // 10 cm
	stickerMargin = 1.5 * mmToPt // thin breathing room
)

// stickerSheets lays out an already-ordered label file ONE PER PAGE on a
// 10x10 cm sticker, each scaled to fit (aspect preserved) inside a thin margin.
func stickerSheets(tmpDir, ordered string, conf *model.Configuration) (string, error) {
	// 1x1 "grid" = one input label per output page; scaled to the sticker minus
	// the margin, aspect preserved, centered, kept upright (no auto-rotate). The
	// n passed here is overridden by the 1x1 grid; pdfcpu just wants a valid one.
	nup, err := pdfapi.PDFNUpConfig(4, "border:off", conf)
	if err != nil {
		return "", err
	}
	nup.Grid = &types.Dim{Width: 1, Height: 1}
	nup.PageDim = &types.Dim{Width: stickerSide, Height: stickerSide}
	nup.PageSize = ""
	nup.UserDim = true
	nup.InpUnit = types.POINTS
	nup.Margin = stickerMargin
	nup.Border = false
	nup.Enforce = false // keep the label upright; don't rotate to "best fit"
	sheets := filepath.Join(tmpDir, "label_sheets.pdf")
	if err := pdfapi.NUpFile([]string{ordered}, sheets, nil, nup, conf); err != nil {
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

// convert builds one combined PDF from the inputs and writes an .xlsx copy. The
// summary is one table per receiving branch (across all POs), each row tagged
// with its PO number; the labels follow, grouped in the same branch order.
// Returns (#PDF sections, warnings, err). progress (if non-nil) is called as
// work proceeds: progress(done, total, msg).
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

	var allRows []Item  // every line item across all POs (grouped by branch later)
	var allCounts []int // labels-needed per item, aligned with allRows
	var warnings []string
	// Per-PO cropped label files (distinct labels, one page each), and, aligned
	// with allRows, which cropped file + page each item's label lives on. Labels
	// are assembled in branch order at the end.
	var cropFiles []string
	var cropPages []int // page count of each cropFiles entry
	var itemCrop []int  // index into cropFiles (or -1), aligned with allRows
	var itemPage []int  // 1-based page within that cropped file, aligned with allRows

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
		for i := range items { // fall back to the file name if the PO number is blank
			if items[i].DocNo == "" {
				items[i].DocNo = baseName(in)
			}
		}

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

		allRows = append(allRows, items...)
		allCounts = append(allCounts, counts...)

		cropped, codeToPage, npages, err := cropPOLabels(poDir, norm, items, loc, counts, conf)
		os.Remove(norm)
		if err != nil {
			warnings = append(warnings, name+": label build failed: "+err.Error())
		}
		cropIdx := -1
		if cropped != "" {
			cropIdx = len(cropFiles)
			cropFiles = append(cropFiles, cropped)
			cropPages = append(cropPages, npages)
		}
		for _, it := range items { // record each item's label location (aligned with allRows)
			if cropIdx >= 0 {
				if pg, ok := codeToPage[it.Code]; ok {
					itemCrop = append(itemCrop, cropIdx)
					itemPage = append(itemPage, pg)
					continue
				}
			}
			itemCrop = append(itemCrop, -1)
			itemPage = append(itemPage, 0)
		}
	}

	if len(allRows) == 0 {
		return 0, warnings, fmt.Errorf("nothing produced - no readable PO pages in the input")
	}
	prog(len(inputs), "Combining labels and saving...")

	// One summary table per branch (delivery), in first-seen order, with a PO
	// column on each row.
	var branchOrder []string
	byBranch := map[string][]int{}
	for i, it := range allRows {
		if _, ok := byBranch[it.Delivery]; !ok {
			branchOrder = append(branchOrder, it.Delivery)
		}
		byBranch[it.Delivery] = append(byBranch[it.Delivery], i)
	}
	var tablePaths []string
	for gi, b := range branchOrder {
		idxs := byBranch[b]
		bItems := make([]Item, len(idxs))
		bCounts := make([]int, len(idxs))
		for j, i := range idxs {
			bItems[j], bCounts[j] = allRows[i], allCounts[i]
		}
		title := b
		if title == "" {
			title = "(ไม่ระบุสาขา)"
		}
		tps, err := renderTablePages(tmpDir, gi, title, bItems, bCounts, fam)
		if err != nil {
			warnings = append(warnings, "table render failed for branch "+b+": "+err.Error())
			continue
		}
		tablePaths = append(tablePaths, tps...)
	}
	if len(tablePaths) == 0 {
		return 0, warnings, fmt.Errorf("nothing produced - table rendering failed")
	}

	// all summaries first, then all labels at the bottom - ordered BY BRANCH to
	// match the tables (each branch's stickers together, in the same row order).
	sections := append([]string{}, tablePaths...)
	if len(cropFiles) > 0 {
		bigCropped := filepath.Join(tmpDir, "labels_all.pdf")
		if err := pdfapi.MergeCreateFile(cropFiles, bigCropped, false, conf); err != nil {
			warnings = append(warnings, "label merge failed: "+err.Error())
		} else {
			off := make([]int, len(cropFiles)) // page offset of each file within bigCropped
			run := 0
			for k := range cropFiles {
				off[k] = run
				run += cropPages[k]
			}
			var sel []string // global pages, branch order, with copies
			for _, b := range branchOrder {
				for _, i := range byBranch[b] {
					if itemCrop[i] < 0 || allCounts[i] <= 0 {
						continue
					}
					g := off[itemCrop[i]] + itemPage[i]
					for k := 0; k < allCounts[i]; k++ {
						sel = append(sel, strconv.Itoa(g))
					}
				}
			}
			if len(sel) > 0 {
				ordered := filepath.Join(tmpDir, "labels_ordered.pdf")
				if err := pdfapi.CollectFile(bigCropped, ordered, sel, conf); err != nil {
					warnings = append(warnings, "label ordering failed: "+err.Error())
				} else if sheets, err := stickerSheets(tmpDir, ordered, conf); err != nil {
					warnings = append(warnings, "label sheets failed: "+err.Error())
				} else if sheets != "" {
					sections = append(sections, sheets)
				}
			}
		}
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
