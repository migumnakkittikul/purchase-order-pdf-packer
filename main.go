// Command POPack converts SAP-generated purchase-order PDFs into a compact
// summary plus the receiving labels that are actually needed.
package main

import (
	"fmt"
	"os"
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
		fmt.Println("normalized", in, "->", norm)
		os.Remove(norm)
	}
}
