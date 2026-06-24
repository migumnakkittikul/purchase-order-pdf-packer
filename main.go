// Command POPack converts SAP-generated purchase-order PDFs into a compact
// summary plus the receiving labels that are actually needed.
package main

import (
	"fmt"
	"os"

	"github.com/ledongthuc/pdf"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Println("usage: popack <po.pdf> [more.pdf ...]")
		return
	}
	fam := loadFontFamily()
	for _, in := range args {
		norm, err := normalizePDF(in)
		if err != nil {
			fmt.Fprintln(os.Stderr, in+":", err)
			continue
		}
		f, r, err := pdf.Open(norm)
		if err != nil {
			os.Remove(norm)
			fmt.Fprintln(os.Stderr, in+":", err)
			continue
		}
		items := extractItems(r)
		f.Close()
		os.Remove(norm)
		if len(items) == 0 {
			fmt.Println(in + ": no line items found")
			continue
		}

		counts := make([]int, len(items))
		for i, it := range items {
			counts[i] = labelsNeeded(it)
		}
		docNo := items[0].DocNo
		if docNo == "" {
			docNo = baseName(in)
		}
		dir, _ := os.MkdirTemp("", "popack")
		paths, err := renderTablePages(dir, docNo, items, counts, fam)
		if err != nil {
			os.RemoveAll(dir)
			fmt.Fprintln(os.Stderr, in+":", err)
			continue
		}
		out := baseName(in) + "_summary.pdf"
		if data, err := os.ReadFile(paths[0]); err == nil {
			os.WriteFile(out, data, 0o644)
		}
		os.RemoveAll(dir)
		fmt.Printf("%s: %d item(s) -> %s (%d page(s))\n", in, len(items), out, len(paths))
	}
}
