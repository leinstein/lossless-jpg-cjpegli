//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")

	pCreateWindowEx   = user32.NewProc("CreateWindowExW")
	pDefWindowProc    = user32.NewProc("DefWindowProcW")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pDispatchMessage  = user32.NewProc("DispatchMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pIsDialogMessage  = user32.NewProc("IsDialogMessageW")
	pGetMessage       = user32.NewProc("GetMessageW")
	pLoadCursor       = user32.NewProc("LoadCursorW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pRegisterClassEx  = user32.NewProc("RegisterClassExW")
	pSendMessage      = user32.NewProc("SendMessageW")
	pSetWindowText    = user32.NewProc("SetWindowTextW")
	pGetWindowText    = user32.NewProc("GetWindowTextW")
	pGetWindowTextLen = user32.NewProc("GetWindowTextLengthW")
	pShowWindow       = user32.NewProc("ShowWindow")
	pUpdateWindow     = user32.NewProc("UpdateWindow")
	pMessageBox       = user32.NewProc("MessageBoxW")
	pEnableWindow     = user32.NewProc("EnableWindow")

	pGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	pGetStockObject      = gdi32.NewProc("GetStockObject")
	pGetOpenFileName     = comdlg32.NewProc("GetOpenFileNameW")
	pSHBrowseForFolder   = shell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	pCoTaskMemFree       = ole32.NewProc("CoTaskMemFree")
	pCoInitializeEx      = ole32.NewProc("CoInitializeEx")
	pCoCreateInstance    = ole32.NewProc("CoCreateInstance")
)

const (
	WS_OVERLAPPEDWINDOW = 0x00CF0000
	WS_VISIBLE          = 0x10000000
	WS_CHILD            = 0x40000000
	WS_TABSTOP          = 0x00010000
	WS_BORDER           = 0x00800000
	WS_VSCROLL          = 0x00200000

	ES_LEFT        = 0x0000
	ES_MULTILINE   = 0x0004
	ES_AUTOHSCROLL = 0x0080
	ES_AUTOVSCROLL = 0x0040
	ES_READONLY    = 0x0800

	BS_PUSHBUTTON      = 0x00000000
	BS_AUTOCHECKBOX    = 0x00000003
	BS_AUTORADIOBUTTON = 0x00000009
	BS_GROUPBOX        = 0x00000007

	WM_CREATE        = 0x0001
	WM_DESTROY       = 0x0002
	WM_COMMAND       = 0x0111
	WM_SETFONT       = 0x0030
	WM_GETTEXT       = 0x000D
	WM_GETTEXTLENGTH = 0x000E
	BM_GETCHECK      = 0x00F0
	BM_SETCHECK      = 0x00F1
	BST_CHECKED      = 1

	COLOR_WINDOW     = 5
	DEFAULT_GUI_FONT = 17
	IDC_ARROW        = 32512
	SW_SHOWDEFAULT   = 10

	OFN_PATHMUSTEXIST = 0x00000800
	OFN_FILEMUSTEXIST = 0x00001000
	OFN_EXPLORER      = 0x00080000
	OFN_NOCHANGEDIR   = 0x00000008

	BIF_RETURNONLYFSDIRS = 0x00000001
	BIF_NEWDIALOGSTYLE   = 0x00000040
	BIF_EDITBOX          = 0x00000010

	COINIT_APARTMENTTHREADED = 0x2

	ID_CJPEGLI_EDIT  = 1001
	ID_CJPEGLI_BTN   = 1002
	ID_INPUT_EDIT    = 1003
	ID_INPUT_FILE    = 1004
	ID_INPUT_FOLDER  = 1005
	ID_OUT_EDIT      = 1006
	ID_OUT_BTN       = 1007
	ID_Q_EDIT        = 1008
	ID_SAMPLING_EDIT = 1009
	ID_SAMPLING_422  = 2101
	ID_SAMPLING_444  = 2102
	ID_WIDTH_EDIT    = 1010
	ID_TARGET_EDIT   = 1011
	ID_MINQ_EDIT     = 1012
	ID_MAXQ_EDIT     = 1013
	ID_RECURSIVE     = 1014
	ID_OVERWRITE     = 1015
	ID_KEEP_LARGER   = 1016
	ID_JPEGTRAN      = 1017
	ID_START         = 1018
	ID_LOG_EDIT      = 1019
)

type POINT struct{ X, Y int32 }
type MSG struct {
	Hwnd           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             POINT
}
type WNDCLASSEX struct {
	CbSize                                   uint32
	Style                                    uint32
	LpfnWndProc                              uintptr
	CbClsExtra, CbWndExtra                   int32
	HInstance, HIcon, HCursor, HbrBackground uintptr
	LpszMenuName, LpszClassName              *uint16
	HIconSm                                  uintptr
}
type OPENFILENAME struct {
	LStructSize                    uint32
	HwndOwner, HInstance           uintptr
	LpstrFilter, LpstrCustomFilter *uint16
	NMaxCustFilter, NFilterIndex   uint32
	LpstrFile                      *uint16
	NMaxFile                       uint32
	LpstrFileTitle                 *uint16
	NMaxFileTitle                  uint32
	LpstrInitialDir, LpstrTitle    *uint16
	Flags                          uint32
	NFileOffset, NFileExtension    uint16
	LpstrDefExt                    *uint16
	LCustData, LpfnHook            uintptr
	LpTemplateName                 *uint16
	PvReserved                     uintptr
	DwReserved, FlagsEx            uint32
}
type BROWSEINFO struct {
	HwndOwner, PidlRoot uintptr
	PszDisplayName      *uint16
	LpszTitle           *uint16
	UlFlags             uint32
	Lpfn                uintptr
	LParam              uintptr
	IImage              int32
}

type App struct {
	hwnd                                                                                     uintptr
	hfont                                                                                    uintptr
	cjpegliEdit, inputEdit, outEdit                                                          uintptr
	qEdit, samplingEdit, sampling422, sampling444, widthEdit, targetEdit, minQEdit, maxQEdit uintptr
	recursiveCheck, overwriteCheck, keepLargerCheck, jpegtranCheck                           uintptr
	startBtn, logEdit                                                                        uintptr
	logMu                                                                                    sync.Mutex
	logText                                                                                  string
	running                                                                                  bool
}

var app *App

func main() {
	runtime.LockOSThread()
	pCoInitializeEx.Call(0, COINIT_APARTMENTTHREADED)
	app = &App{}
	hInstance, _, _ := pGetModuleHandle.Call(0)
	className := utf16Ptr("TeaJpegliCompressorWindow")
	cursor, _, _ := pLoadCursor.Call(0, IDC_ARROW)
	wc := WNDCLASSEX{CbSize: uint32(unsafe.Sizeof(WNDCLASSEX{})), LpfnWndProc: syscall.NewCallback(wndProc), HInstance: hInstance, HCursor: cursor, HbrBackground: COLOR_WINDOW + 1, LpszClassName: className}
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	title := utf16Ptr("微损压缩JPG")
	hwnd, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)), WS_OVERLAPPEDWINDOW|WS_VISIBLE, 100, 80, 840, 650, 0, 0, hInstance, 0)
	app.hwnd = hwnd
	pShowWindow.Call(hwnd, SW_SHOWDEFAULT)
	pUpdateWindow.Call(hwnd)
	var msg MSG
	for {
		ret, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_CREATE:
		initControls(hwnd)
		return 0
	case WM_DESTROY:
		pPostQuitMessage.Call(0)
		return 0
	case WM_COMMAND:
		id := int(wParam & 0xffff)
		handleCommand(id)
		return 0
	}
	ret, _, _ := pDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

func initControls(hwnd uintptr) {
	font, _, _ := pGetStockObject.Call(DEFAULT_GUI_FONT)
	app.hfont = font

	group(hwnd, "图片来源", 16, 16, 792, 106)
	label(hwnd, "输入：", 34, 52, 50, 22)
	app.inputEdit = edit(hwnd, "", 90, 48, 500, 24, ID_INPUT_EDIT)
	button(hwnd, "选择图片", 604, 47, 86, 27, ID_INPUT_FILE)
	button(hwnd, "选择文件夹", 700, 47, 92, 27, ID_INPUT_FOLDER)
	label(hwnd, "输出：", 34, 86, 50, 22)
	app.outEdit = edit(hwnd, "", 90, 82, 500, 24, ID_OUT_EDIT)
	button(hwnd, "输出目录", 604, 81, 86, 27, ID_OUT_BTN)
	label(hwnd, "留空自动生成", 700, 86, 96, 22)

	group(hwnd, "常用参数", 16, 136, 792, 118)
	label(hwnd, "质量 q", 34, 171, 50, 22)
	app.qEdit = edit(hwnd, "85", 90, 167, 62, 24, ID_Q_EDIT)
	label(hwnd, "采样", 176, 171, 42, 22)
	app.samplingEdit = radio(hwnd, "420 体积最小", 220, 167, 110, 24, ID_SAMPLING_EDIT, true)
	app.sampling422 = radio(hwnd, "422 体积中等", 342, 167, 110, 24, ID_SAMPLING_422, false)
	app.sampling444 = radio(hwnd, "444 体积最大", 464, 167, 110, 24, ID_SAMPLING_444, false)

	label(hwnd, "最大宽", 34, 209, 50, 22)
	app.widthEdit = edit(hwnd, "1280", 90, 205, 62, 24, ID_WIDTH_EDIT)
	label(hwnd, "目标KB", 176, 209, 55, 22)
	app.targetEdit = edit(hwnd, "0", 234, 205, 62, 24, ID_TARGET_EDIT)
	label(hwnd, "0 表示固定质量；填写体积后会在 min q / max q 范围内自动寻找合适质量", 314, 209, 470, 22)

	group(hwnd, "高级参数", 16, 268, 792, 118)
	label(hwnd, "min q", 34, 303, 50, 22)
	app.minQEdit = edit(hwnd, "72", 90, 299, 62, 24, ID_MINQ_EDIT)
	label(hwnd, "max q", 176, 303, 50, 22)
	app.maxQEdit = edit(hwnd, "90", 234, 299, 62, 24, ID_MAXQ_EDIT)
	app.recursiveCheck = checkbox(hwnd, "递归文件夹", 314, 299, 105, 25, ID_RECURSIVE, true)
	app.overwriteCheck = checkbox(hwnd, "覆盖已存在", 430, 299, 105, 25, ID_OVERWRITE, false)
	app.keepLargerCheck = checkbox(hwnd, "变大保留原图", 546, 299, 125, 25, ID_KEEP_LARGER, true)

	app.jpegtranCheck = checkbox(hwnd, "jpegtran 二次优化", 90, 337, 145, 25, ID_JPEGTRAN, true)
	label(hwnd, "需要 tools\\jpegtran.exe；没有会自动跳过", 244, 341, 320, 22)

	app.startBtn = button(hwnd, "开始压缩", 34, 404, 140, 34, ID_START)
	label(hwnd, "选择图片使用新版 COM 选择器；也支持拖拽/粘贴路径。", 196, 412, 560, 22)
	app.logEdit = multiline(hwnd, "", 16, 454, 792, 146, ID_LOG_EDIT)

	app.appendLog("准备就绪。")
	app.appendLog("请把 cjpegli.exe 放在：程序目录\\tools\\cjpegli.exe")
	app.appendLog("推荐参数：q=85，采样=420，最大宽=1280。")
	app.appendLog("native-v5：选择图片改用 IFileOpenDialog，绕开老式选图接口。")
}

func group(parent uintptr, text string, x, y, w, h int32) uintptr {
	return createControl(parent, "BUTTON", text, WS_CHILD|WS_VISIBLE|BS_GROUPBOX, 0, x, y, w, h, 0)
}

func handleCommand(id int) {
	if app == nil {
		return
	}
	switch id {
	case ID_INPUT_FILE:
		if p := openFileDialog(app.hwnd, "选择图片", "图片\x00*.jpg;*.jpeg;*.png\x00所有文件\x00*.*\x00"); p != "" {
			setText(app.inputEdit, p)
		}
	case ID_INPUT_FOLDER:
		if p := browseFolder(app.hwnd, "选择图片文件夹"); p != "" {
			setText(app.inputEdit, p)
		}
	case ID_OUT_BTN:
		if p := browseFolder(app.hwnd, "选择输出目录"); p != "" {
			setText(app.outEdit, p)
		}
	case ID_START:
		app.startCompression()
	}
}

func (a *App) startCompression() {
	if a.running {
		return
	}
	inputs := parsePaths(getText(a.inputEdit))
	if len(inputs) == 0 {
		message(a.hwnd, "请先选择图片或文件夹。", "提示")
		return
	}
	exeDir := executableDir()
	cjpegli := findBundledCjpegli(exeDir)
	if cjpegli == "" {
		message(a.hwnd, "未找到 cjpegli.exe。\r\n\r\n请放到：\r\n"+filepath.Join(exeDir, "tools", "cjpegli.exe"), "缺少 cjpegli.exe")
		return
	}
	opt := Options{
		CjpegliPath:  cjpegli,
		JpegtranPath: findTool("jpegtran.exe", exeDir),
		Quality:      atoiDefault(getText(a.qEdit), 85),
		Sampling:     selectedSampling(a),
		MaxWidth:     atoiDefault(getText(a.widthEdit), 1280),
		TargetKB:     atoiDefault(getText(a.targetEdit), 0),
		MinQuality:   atoiDefault(getText(a.minQEdit), 72),
		MaxQuality:   atoiDefault(getText(a.maxQEdit), 90),
		OutDir:       strings.TrimSpace(getText(a.outEdit)),
		Recursive:    checked(a.recursiveCheck),
		Overwrite:    checked(a.overwriteCheck),
		KeepIfLarger: checked(a.keepLargerCheck),
		UseJpegtran:  checked(a.jpegtranCheck),
	}
	a.logMu.Lock()
	a.logText = ""
	a.logMu.Unlock()
	setText(a.logEdit, "")
	a.appendLog("开始压缩...")
	a.appendLog("cjpegli：" + cjpegli)
	if opt.UseJpegtran && opt.JpegtranPath != "" {
		a.appendLog("jpegtran：" + opt.JpegtranPath)
	}
	a.running = true
	pEnableWindow.Call(a.startBtn, 0)
	go func() {
		summary := runBatch(inputs, opt, func(s string) { a.appendLog(s) })
		a.appendLog("输出目录：" + summary.OutRoot)
		a.running = false
		pEnableWindow.Call(a.startBtn, 1)
		if summary.Failed == 0 {
			message(a.hwnd, "压缩完成。\r\n输出目录：\r\n"+summary.OutRoot, "完成")
		} else {
			message(a.hwnd, fmt.Sprintf("压缩完成，但有 %d 个失败。\r\n请查看日志。", summary.Failed), "完成")
		}
	}()
}

func (a *App) appendLog(s string) {
	a.logMu.Lock()
	if a.logText == "" {
		a.logText = s
	} else {
		a.logText += "\r\n" + s
	}
	text := a.logText
	a.logMu.Unlock()
	setText(a.logEdit, text)
}

func label(parent uintptr, text string, x, y, w, h int32) uintptr {
	return createControl(parent, "STATIC", text, WS_CHILD|WS_VISIBLE, 0, x, y, w, h, 0)
}
func edit(parent uintptr, text string, x, y, w, h int32, id int) uintptr {
	return createControl(parent, "EDIT", text, WS_CHILD|WS_VISIBLE|WS_BORDER|WS_TABSTOP|ES_AUTOHSCROLL|ES_LEFT, 0, x, y, w, h, id)
}
func multiline(parent uintptr, text string, x, y, w, h int32, id int) uintptr {
	return createControl(parent, "EDIT", text, WS_CHILD|WS_VISIBLE|WS_BORDER|WS_VSCROLL|ES_MULTILINE|ES_AUTOVSCROLL|ES_READONLY, 0, x, y, w, h, id)
}
func button(parent uintptr, text string, x, y, w, h int32, id int) uintptr {
	return createControl(parent, "BUTTON", text, WS_CHILD|WS_VISIBLE|WS_TABSTOP|BS_PUSHBUTTON, 0, x, y, w, h, id)
}
func checkbox(parent uintptr, text string, x, y, w, h int32, id int, isChecked bool) uintptr {
	hwnd := createControl(parent, "BUTTON", text, WS_CHILD|WS_VISIBLE|WS_TABSTOP|BS_AUTOCHECKBOX, 0, x, y, w, h, id)
	if isChecked {
		pSendMessage.Call(hwnd, BM_SETCHECK, BST_CHECKED, 0)
	}
	return hwnd
}
func radio(parent uintptr, text string, x, y, w, h int32, id int, isChecked bool) uintptr {
	hwnd := createControl(parent, "BUTTON", text, WS_CHILD|WS_VISIBLE|WS_TABSTOP|BS_AUTORADIOBUTTON, 0, x, y, w, h, id)
	if isChecked {
		pSendMessage.Call(hwnd, BM_SETCHECK, BST_CHECKED, 0)
	}
	return hwnd
}

func createControl(parent uintptr, class, text string, style uint32, exStyle uint32, x, y, w, h int32, id int) uintptr {
	hwnd, _, _ := pCreateWindowEx.Call(uintptr(exStyle), uintptr(unsafe.Pointer(utf16Ptr(class))), uintptr(unsafe.Pointer(utf16Ptr(text))), uintptr(style), uintptr(x), uintptr(y), uintptr(w), uintptr(h), parent, uintptr(id), 0, 0)
	if app != nil && app.hfont != 0 {
		pSendMessage.Call(hwnd, WM_SETFONT, app.hfont, 1)
	}
	return hwnd
}

func setText(hwnd uintptr, s string) { pSetWindowText.Call(hwnd, uintptr(unsafe.Pointer(utf16Ptr(s)))) }
func getText(hwnd uintptr) string {
	l, _, _ := pGetWindowTextLen.Call(hwnd)
	buf := make([]uint16, int(l)+2)
	pGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}
func checked(hwnd uintptr) bool {
	r, _, _ := pSendMessage.Call(hwnd, BM_GETCHECK, 0, 0)
	return r == BST_CHECKED
}
func message(hwnd uintptr, text, title string) {
	pMessageBox.Call(hwnd, uintptr(unsafe.Pointer(utf16Ptr(text))), uintptr(unsafe.Pointer(utf16Ptr(title))), 0)
}

type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type COMDLG_FILTERSPEC struct {
	PszName *uint16
	PszSpec *uint16
}

var (
	CLSID_FileOpenDialog = GUID{0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	IID_IFileOpenDialog  = GUID{0xD57C7288, 0xD4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
)

const (
	CLSCTX_INPROC_SERVER = 0x1
	FOS_FORCEFILESYSTEM  = 0x00000040
	FOS_PATHMUSTEXIST    = 0x00000800
	FOS_FILEMUSTEXIST    = 0x00001000
	SIGDN_FILESYSPATH    = 0x80058000
	HRESULT_CANCELLED    = 0x800704C7
)

func comCall(obj uintptr, method uintptr, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + method*unsafe.Sizeof(uintptr(0))))
	all := append([]uintptr{obj}, args...)
	r, _, _ := syscall.SyscallN(fn, all...)
	return r
}

func comRelease(obj uintptr) {
	if obj != 0 {
		_ = comCall(obj, 2)
	}
}

func comSelectImage(owner uintptr) (string, error) {
	var dlg uintptr
	hr, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&CLSID_FileOpenDialog)), 0, CLSCTX_INPROC_SERVER, uintptr(unsafe.Pointer(&IID_IFileOpenDialog)), uintptr(unsafe.Pointer(&dlg)))
	if hr != 0 || dlg == 0 {
		return "", fmt.Errorf("无法创建文件选择器，HRESULT=0x%X", hr)
	}
	defer comRelease(dlg)
	filters := []COMDLG_FILTERSPEC{{utf16Ptr("图片文件"), utf16Ptr("*.jpg;*.jpeg;*.png")}, {utf16Ptr("所有文件"), utf16Ptr("*.*")}}
	_ = comCall(dlg, 4, uintptr(len(filters)), uintptr(unsafe.Pointer(&filters[0])))
	var opts uint32
	_ = comCall(dlg, 10, uintptr(unsafe.Pointer(&opts)))
	opts |= FOS_FORCEFILESYSTEM | FOS_PATHMUSTEXIST | FOS_FILEMUSTEXIST
	_ = comCall(dlg, 9, uintptr(opts))
	_ = comCall(dlg, 17, uintptr(unsafe.Pointer(utf16Ptr("选择图片"))))
	hr = comCall(dlg, 3, owner)
	if hr == HRESULT_CANCELLED {
		return "", nil
	}
	if hr != 0 {
		return "", fmt.Errorf("打开文件选择器失败，HRESULT=0x%X", hr)
	}
	var item uintptr
	hr = comCall(dlg, 20, uintptr(unsafe.Pointer(&item)))
	if hr != 0 || item == 0 {
		return "", fmt.Errorf("读取选择结果失败，HRESULT=0x%X", hr)
	}
	defer comRelease(item)
	var pwsz uintptr
	hr = comCall(item, 5, SIGDN_FILESYSPATH, uintptr(unsafe.Pointer(&pwsz)))
	if hr != 0 || pwsz == 0 {
		return "", fmt.Errorf("读取文件路径失败，HRESULT=0x%X", hr)
	}
	defer pCoTaskMemFree.Call(pwsz)
	return utf16PtrToString(pwsz), nil
}

func utf16PtrToString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	buf := make([]uint16, 0, 260)
	for i := uintptr(0); ; i += 2 {
		ch := *(*uint16)(unsafe.Pointer(ptr + i))
		if ch == 0 {
			break
		}
		buf = append(buf, ch)
	}
	return syscall.UTF16ToString(buf)
}

func openFileDialog(owner uintptr, title, filter string) string {
	fileBuf := make([]uint16, 32768)
	f := syscall.StringToUTF16(filter + "\x00")
	ofn := OPENFILENAME{
		LStructSize: uint32(unsafe.Sizeof(OPENFILENAME{})),
		HwndOwner:   owner,
		LpstrFilter: &f[0],
		LpstrFile:   &fileBuf[0],
		NMaxFile:    uint32(len(fileBuf)),
		LpstrTitle:  utf16Ptr(title),
		Flags:       OFN_EXPLORER | OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST | OFN_NOCHANGEDIR,
	}
	r, _, _ := pGetOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(fileBuf)
}

func browseFolder(owner uintptr, title string) string {
	var display [260]uint16
	bi := BROWSEINFO{HwndOwner: owner, PszDisplayName: &display[0], LpszTitle: utf16Ptr(title), UlFlags: BIF_RETURNONLYFSDIRS | BIF_NEWDIALOGSTYLE | BIF_EDITBOX}
	pidl, _, _ := pSHBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer pCoTaskMemFree.Call(pidl)
	var path [260]uint16
	r, _, _ := pSHGetPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&path[0])))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(path[:])
}

func findBundledCjpegli(exeDir string) string {
	candidates := []string{
		filepath.Join(exeDir, "tools", "cjpegli.exe"),
		filepath.Join(exeDir, "cjpegli.exe"),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func selectedSampling(a *App) string {
	if checked(a.sampling444) {
		return "444"
	}
	if checked(a.sampling422) {
		return "422"
	}
	return "420"
}

func parsePaths(s string) []string {
	s = strings.ReplaceAll(s, "\r", "\n")
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), "\"")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
func atoiDefault(s string, def int) int {
	i, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return i
}
func utf16Ptr(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func init() {
	// Ensure relative default output resolves next to the current working directory when double-clicked.
	if exe, err := os.Executable(); err == nil {
		_ = os.Chdir(filepath.Dir(exe))
	}
}
