package main

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func init() { model.ConfigPath = "disable" } // no per-user config dir on the target PC

func newConf() *model.Configuration {
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	conf.Unit = types.POINTS // interpret all dimensions in points
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
// order and with repetition for copies, each cropped tight to its border box.
func buildPOLabels(poDir, norm string, items []Item, loc map[string]labelLoc,
	counts []int, conf *model.Configuration) (*labelTile, error) {

	// crop each needed label page to its own single-page PDF
	cropFor := map[int]string{} // source page -> cropped single-label file
	var w, h float64
	for i, it := range items {
		l, ok := loc[it.Code]
		if !ok || counts[i] <= 0 {
			continue
		}
		w, h = l.W, l.H
		if _, done := cropFor[l.Page]; done {
			continue
		}
		// pull this single label page out of the source
		one := filepath.Join(poDir, fmt.Sprintf("page_%d.pdf", l.Page))
		if err := pdfapi.CollectFile(norm, one, []string{strconv.Itoa(l.Page)}, conf); err != nil {
			return nil, fmt.Errorf("collect label page: %w", err)
		}
		// crop it to the label's border box (top-left quadrant)
		box, err := pdfapi.Box(fmt.Sprintf("[%g %g %g %g]", 12.0, h/2, w/2-18, h-28), types.POINTS)
		if err != nil {
			return nil, err
		}
		cr := filepath.Join(poDir, fmt.Sprintf("crop_%d.pdf", l.Page))
		if err := pdfapi.CropFile(one, cr, nil, box, conf); err != nil {
			return nil, fmt.Errorf("crop label: %w", err)
		}
		cropFor[l.Page] = cr
	}
	if len(cropFor) == 0 {
		return nil, nil
	}

	// merge the cropped labels in order, repeating each by its copy count
	var parts []string
	for i, it := range items {
		l, ok := loc[it.Code]
		if !ok || counts[i] <= 0 {
			continue
		}
		for k := 0; k < counts[i]; k++ {
			parts = append(parts, cropFor[l.Page])
		}
	}
	ordered := filepath.Join(poDir, "ordered.pdf")
	if err := pdfapi.MergeCreateFile(parts, ordered, false, conf); err != nil {
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
	nup.Margin = 6 // labels print at ~original size; this just spaces them out
	sheets := filepath.Join(tmpDir, "label_sheets.pdf")
	if err := pdfapi.NUpFile([]string{merged}, sheets, nil, nup, conf); err != nil {
		return "", fmt.Errorf("nup labels: %w", err)
	}
	return sheets, nil
}
