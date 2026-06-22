package main

import (
	"os"

	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/api"
)

// normalizePDF runs the PDF through pdfcpu so strict readers can parse it.
// Returns the path of a temp normalized file (the caller removes it).
func normalizePDF(path string) (string, error) {
	tmp, err := os.CreateTemp("", "popack-norm-*.pdf")
	if err != nil {
		return "", err
	}
	tmp.Close()
	if err := pdfcpu.OptimizeFile(path, tmp.Name(), nil); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}
