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

// ---- native progress window ---------------------------------------------- //
//
// A small window with a label and a progress bar. If creating it fails, the
// helpers below no-op so the conversion still runs and saves.

var (
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	comctl32            = syscall.NewLazyDLL("comctl32.dll")
	gdi32               = syscall.NewLazyDLL("gdi32.dll")
	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
	procInitCommonCtrl  = comctl32.NewProc("InitCommonControlsEx")
	procRegisterClassEx = user32.NewProc("RegisterClassExW")
	procCreateWindowEx  = user32.NewProc("CreateWindowExW")
	procDefWindowProc   = user32.NewProc("DefWindowProcW")
	procShowWindow      = user32.NewProc("ShowWindow")
	procUpdateWindow    = user32.NewProc("UpdateWindow")
	procSendMessage     = user32.NewProc("SendMessageW")
	procSetWindowText   = user32.NewProc("SetWindowTextW")
	procDestroyWindow   = user32.NewProc("DestroyWindow")
	procPeekMessage     = user32.NewProc("PeekMessageW")
	procTranslateMsg    = user32.NewProc("TranslateMessage")
	procDispatchMsg     = user32.NewProc("DispatchMessageW")
	procGetSysMetrics   = user32.NewProc("GetSystemMetrics")
	procLoadCursor      = user32.NewProc("LoadCursorW")
	procGetStockObject  = gdi32.NewProc("GetStockObject")
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type initCommonControlsEx struct {
	dwSize uint32
	dwICC  uint32
}

type win32msg struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       struct{ x, y int32 }
	lPrivate uint32
}

type progressWin struct{ hwnd, bar, label uintptr }

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func createProgressWindow(title string, total int) *progressWin {
	const (
		wsCaption  = 0x00C00000
		wsSysMenu  = 0x00080000
		wsChild    = 0x40000000
		wsVisible  = 0x10000000
		pbsSmooth  = 0x01
		swNormal   = 1
		iccProgr   = 0x20
		defGUIFont = 17
		wmSetFont  = 0x0030
		pbmRange32 = 0x0406
		idcArrow   = 32512
	)
	icc := initCommonControlsEx{dwSize: uint32(unsafe.Sizeof(initCommonControlsEx{})), dwICC: iccProgr}
	procInitCommonCtrl.Call(uintptr(unsafe.Pointer(&icc)))

	hInst, _, _ := procGetModuleHandle.Call(0)
	cursor, _, _ := procLoadCursor.Call(0, idcArrow)

	className := u16("popackProgressClass")
	wc := wndClassExW{
		lpfnWndProc:   procDefWindowProc.Addr(),
		hInstance:     hInst,
		hCursor:       cursor,
		hbrBackground: 6, // COLOR_WINDOW+1
		lpszClassName: className,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))) // ok if already registered

	sx, _, _ := procGetSysMetrics.Call(0) // SM_CXSCREEN
	sy, _, _ := procGetSysMetrics.Call(1) // SM_CYSCREEN
	w, h := 460, 160
	x := (int(sx) - w) / 2
	y := (int(sy) - h) / 2

	hwnd, _, _ := procCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(u16(title))),
		wsCaption|wsSysMenu, uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		0, 0, hInst, 0)
	if hwnd == 0 {
		return nil
	}

	label, _, _ := procCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(u16("STATIC"))), uintptr(unsafe.Pointer(u16("Starting..."))),
		wsChild|wsVisible, 24, 22, 400, 26, hwnd, 0, hInst, 0)
	bar, _, _ := procCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(u16("msctls_progress32"))), 0,
		wsChild|wsVisible|pbsSmooth, 24, 60, 400, 26, hwnd, 0, hInst, 0)

	if font, _, _ := procGetStockObject.Call(defGUIFont); font != 0 {
		procSendMessage.Call(label, wmSetFont, font, 1)
	}
	procSendMessage.Call(bar, pbmRange32, 0, uintptr(total))
	procShowWindow.Call(hwnd, swNormal)
	procUpdateWindow.Call(hwnd)

	p := &progressWin{hwnd: hwnd, bar: bar, label: label}
	p.pump()
	return p
}

func (p *progressWin) pump() {
	const pmRemove = 0x0001
	var m win32msg
	for {
		r, _, _ := procPeekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
		if r == 0 {
			break
		}
		procTranslateMsg.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMsg.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (p *progressWin) update(done, total int, msg string) {
	if p == nil || p.hwnd == 0 {
		return
	}
	const pbmSetPos = 0x0402
	procSetWindowText.Call(p.label, uintptr(unsafe.Pointer(u16(msg))))
	procSendMessage.Call(p.bar, pbmSetPos, uintptr(done), 0)
	p.pump()
}

func (p *progressWin) destroy() {
	if p == nil || p.hwnd == 0 {
		return
	}
	procDestroyWindow.Call(p.hwnd)
	p.pump()
	p.hwnd = 0
}

// newProgress creates the progress window and returns helpers to update and
// close it. Returns no-op helpers if the window can't be created, so the
// conversion still runs and saves.
func newProgress(total int) (func(int, int, string), func()) {
	p := createProgressWindow(appTitle, total)
	update := func(done, total int, msg string) { p.update(done, total, msg) }
	closeFn := func() { p.destroy() }
	return update, closeFn
}
