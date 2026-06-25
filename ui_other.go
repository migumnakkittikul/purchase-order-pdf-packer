//go:build !windows

package main

import "fmt"

// Console fallbacks for non-Windows (used during development/testing).
func msgInfo(title, text string)  { fmt.Printf("[%s]\n%s\n", title, text) }
func msgError(title, text string) { fmt.Printf("[%s] ERROR\n%s\n", title, text) }
func askYesNo(title, text string) bool {
	fmt.Printf("[%s]\n%s\n", title, text)
	return false
}
func openFile(string) {}

// No native pickers off-Windows (development only).
func openFilesDialog() []string         { return nil }
func saveFileDialog(_, _ string) string { return "" }

// newProgress uses the console bar off-Windows (for development/testing).
func newProgress(int) (func(int, int, string), func()) {
	return consoleProgress, func() {}
}
