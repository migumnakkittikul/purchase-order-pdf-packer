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
		for pno := 1; pno <= r.NumPage(); pno++ {
			p := r.Page(pno)
			if p.V.IsNull() {
				continue
			}
			for _, l := range clusterLines(p) {
				if s := l.text(); s != "" {
					fmt.Println(s)
				}
			}
		}
		f.Close()
		os.Remove(norm)
	}
}
