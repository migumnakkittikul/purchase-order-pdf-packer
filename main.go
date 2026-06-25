// Command POPack converts SAP-generated purchase-order PDFs into a compact
// summary plus the receiving labels that are actually needed.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const appTitle = "PO PDF Packer"

// consoleProgress draws a simple text progress bar (non-Windows fallback).
func consoleProgress(done, total int, msg string) {
	const w = 28
	filled, pct := w, 100
	if total > 0 {
		filled = done * w / total
		pct = done * 100 / total
	}
	if filled > w {
		filled = w
	}
	if pct > 100 {
		pct = 100
	}
	fmt.Printf("\r  [%s%s] %3d%%  %-46.46s",
		strings.Repeat("#", filled), strings.Repeat("-", w-filled), pct, msg)
	if done >= total {
		fmt.Println()
	}
}

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

	// Double-clicked with no files: pick the PDFs, then a save location.
	// Dragging files onto the icon also works.
	if len(inputs) == 0 {
		inputs = openFilesDialog()
		if len(inputs) == 0 {
			return // cancelled
		}
		def := "PO_pack.pdf"
		if len(inputs) == 1 {
			def = baseName(inputs[0]) + "_pack.pdf"
		}
		out = saveFileDialog(def, filepath.Dir(inputs[0]))
		if out == "" {
			return // cancelled
		}
	}

	if out == "" {
		dir := filepath.Dir(inputs[0])
		if len(inputs) == 1 {
			out = filepath.Join(dir, baseName(inputs[0])+"_pack.pdf")
		} else {
			out = filepath.Join(dir, "PO_pack.pdf")
		}
	}

	prog, closeProg := newProgress(len(inputs) + 1)
	n, warns, err := convert(inputs, out, true, prog)
	closeProg()
	if err != nil {
		msgError(appTitle, "Could not process the file(s):\n\n"+err.Error())
		os.Exit(1)
	}
	msg := fmt.Sprintf("Finished: %d page group(s).\n\nSaved to:\n%s\n\nAn Excel (.xlsx) copy was saved next to it.",
		n, out)
	if len(warns) > 0 {
		msg += "\n\nNotes:\n- " + strings.Join(warns, "\n- ")
	}
	if askYesNo(appTitle, msg+"\n\nOpen it now?") {
		openFile(out)
	}
}
