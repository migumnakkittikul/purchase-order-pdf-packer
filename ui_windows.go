//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"unsafe"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
	comdlg32       = syscall.NewLazyDLL("comdlg32.dll")
	procGetOpen    = comdlg32.NewProc("GetOpenFileNameW")
	procGetSave    = comdlg32.NewProc("GetSaveFileNameW")
)

// OPENFILENAMEW (amd64 layout)
type openfilename struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

const (
	ofnReadOnly         = 0x00000001
	ofnHideReadOnly     = 0x00000004
	ofnPathMustExist    = 0x00000800
	ofnFileMustExist    = 0x00001000
	ofnOverwritePrompt  = 0x00000002
	ofnAllowMultiselect = 0x00000200
	ofnExplorer         = 0x00080000
)

var pdfFilter = utf16z("PDF files\x00*.pdf;*.PDF\x00All files\x00*.*\x00")

// utf16z builds a UTF-16 buffer; \x00 in s become separators, plus a final NUL.
func utf16z(s string) []uint16 {
	u := utf16FromString(s)
	return append(u, 0)
}
func utf16FromString(s string) []uint16 {
	r := []rune(s)
	var out []uint16
	for _, c := range r {
		if c == 0 {
			out = append(out, 0)
			continue
		}
		out = append(out, uint16(c)) // BMP only; product paths are fine
	}
	return out
}

// openFilesDialog shows a multi-select Open dialog; returns chosen PDF paths.
func openFilesDialog() []string {
	buf := make([]uint16, 1<<16)
	title, _ := syscall.UTF16PtrFromString("Select PO PDF file(s)")
	ofn := openfilename{
		lStructSize:  uint32(unsafe.Sizeof(openfilename{})),
		lpstrFilter:  &pdfFilter[0],
		lpstrFile:    &buf[0],
		nMaxFile:     uint32(len(buf)),
		lpstrTitle:   title,
		nFilterIndex: 1,
		flags:        ofnExplorer | ofnAllowMultiselect | ofnFileMustExist | ofnPathMustExist | ofnHideReadOnly,
	}
	r, _, _ := procGetOpen.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return nil
	}
	return parseMultiSelect(buf)
}

func parseMultiSelect(buf []uint16) []string {
	// segments separated by NUL, terminated by double-NUL
	var segs []string
	start := 0
	for i := 0; i < len(buf); i++ {
		if buf[i] == 0 {
			if i == start { // empty segment => end
				break
			}
			segs = append(segs, string(utf16Decode(buf[start:i])))
			start = i + 1
		}
	}
	if len(segs) == 0 {
		return nil
	}
	if len(segs) == 1 {
		return segs // single full path
	}
	dir := segs[0]
	var out []string
	for _, f := range segs[1:] {
		out = append(out, dir+`\`+f)
	}
	return out
}

func utf16Decode(u []uint16) []rune {
	var r []rune
	for _, c := range u {
		r = append(r, rune(c))
	}
	return r
}

// saveFileDialog shows a Save As dialog; returns the chosen path ("" if cancel).
func saveFileDialog(defName, initialDir string) string {
	buf := make([]uint16, 1<<15)
	if defName != "" {
		copy(buf, utf16FromString(defName))
	}
	title, _ := syscall.UTF16PtrFromString("Save combined PDF as")
	var dirPtr *uint16
	if initialDir != "" {
		dirPtr, _ = syscall.UTF16PtrFromString(initialDir)
	}
	defExt, _ := syscall.UTF16PtrFromString("pdf")
	ofn := openfilename{
		lStructSize:     uint32(unsafe.Sizeof(openfilename{})),
		lpstrFilter:     &pdfFilter[0],
		lpstrFile:       &buf[0],
		nMaxFile:        uint32(len(buf)),
		lpstrTitle:      title,
		lpstrInitialDir: dirPtr,
		lpstrDefExt:     defExt,
		nFilterIndex:    1,
		flags:           ofnExplorer | ofnOverwritePrompt | ofnPathMustExist | ofnHideReadOnly,
	}
	r, _, _ := procGetSave.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return ""
	}
	// single path, NUL-terminated
	end := 0
	for end < len(buf) && buf[end] != 0 {
		end++
	}
	return string(utf16Decode(buf[:end]))
}

const (
	mbOK       = 0x00000000
	mbYesNo    = 0x00000004
	mbIconInfo = 0x00000040
	mbIconErr  = 0x00000010
	idYes      = 6
)

func messageBox(title, text string, flags uint) int {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(title)
	r, _, _ := procMessageBox.Call(0,
		uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), uintptr(flags))
	return int(r)
}

func msgInfo(title, text string)  { messageBox(title, text, mbOK|mbIconInfo) }
func msgError(title, text string) { messageBox(title, text, mbOK|mbIconErr) }
func askYesNo(title, text string) bool {
	return messageBox(title, text, mbYesNo|mbIconInfo) == idYes
}

func openFile(path string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Start()
}
