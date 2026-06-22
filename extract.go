package main

import (
	"os"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
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

type frag struct {
	x float64
	s string
}

type textLine struct {
	y     float64
	frags []frag // stream order preserved
}

// clusterLines groups char fragments into visual lines by Y (stream order kept).
func clusterLines(p pdf.Page) []textLine {
	const tol = 3.5
	var lines []textLine
	for _, t := range p.Content().Text {
		placed := false
		for i := range lines {
			if t.Y-lines[i].y < tol && lines[i].y-t.Y < tol {
				lines[i].frags = append(lines[i].frags, frag{t.X, t.S})
				placed = true
				break
			}
		}
		if !placed {
			lines = append(lines, textLine{y: t.Y, frags: []frag{{t.X, t.S}}})
		}
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].y > lines[j].y }) // top first
	return lines
}

func (l textLine) text() string {
	var b strings.Builder
	for _, f := range l.frags {
		b.WriteString(f.s)
	}
	return strings.TrimSpace(b.String())
}
