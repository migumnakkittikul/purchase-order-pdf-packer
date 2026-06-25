// Command POPack converts a retailer's SAP-generated purchase-order PDFs into a
// single compact PDF (an order summary plus only the receiving labels that are
// needed) and an Excel copy. It builds to a standalone Windows executable.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const appTitle = "PO PDF Packer"

// logStep appends a timestamped line to a log file in the temp dir, so if the
// tool stalls on someone's machine the last line shows how far it got.
func logStep(format string, a ...any) {
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "POPack.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format("15:04:05.000")+"  "+format+"\n", a...)
}

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

	// Collect input PDFs (also supports -o out.pdf from the command line).
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

	logStep("start: %d file(s) -> %q", len(inputs), out)
	rawProg, closeProg := newProgress(len(inputs) + 1)
	prog := func(d, t int, m string) {
		logStep("step %d/%d %s", d, t, m)
		rawProg(d, t, m)
	}
	n, warns, err := convert(inputs, out, true, prog)
	logStep("convert returned: sections=%d warns=%d err=%v", n, len(warns), err)
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
	logStep("end")
}
