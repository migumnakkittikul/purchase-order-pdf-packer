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

		fmt.Printf("%s: %d item(s)\n", in, len(items))
		labels := 0
		for _, it := range items {
			fmt.Printf("  %-10s %-40s %6s %-8s %10s  x%d label(s)\n",
				it.Code, it.Name, fmtQty(it), it.Unit, fmtMoney(it.Price), labelsNeeded(it))
			labels += labelsNeeded(it)
		}
		fmt.Printf("  -> %d label(s) to print\n", labels)
	}
}
