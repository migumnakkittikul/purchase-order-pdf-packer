// Command POPack converts SAP-generated purchase-order PDFs into a compact
// summary plus the receiving labels that are actually needed.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	args := os.Args[1:]

	var inputs []string
	out := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "-o" && i+1 < len(args) {
			out = args[i+1]
			i++
			continue
		}
		inputs = append(inputs, args[i])
	}
	if len(inputs) == 0 {
		fmt.Println("usage: popack [-o out.pdf] <po.pdf> [more.pdf ...]")
		return
	}
	if out == "" {
		dir := filepath.Dir(inputs[0])
		if len(inputs) == 1 {
			out = filepath.Join(dir, baseName(inputs[0])+"_pack.pdf")
		} else {
			out = filepath.Join(dir, "PO_pack.pdf")
		}
	}

	n, warns, err := convert(inputs, out, true, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d section(s))\n", out, n)
	for _, w := range warns {
		fmt.Println("  warning:", w)
	}
}
