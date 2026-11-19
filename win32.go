//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	winmm    = syscall.NewLazyDLL("winmm.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	pDwmFlush = dwmapi.NewProc("DwmFlush")

	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	pGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	pCreateMutexW                  = kernel32.NewProc("CreateMutexW")
	pCloseHandle                   = kernel32.NewProc("CloseHandle")
	pRegisterWindowMessageW        = user32.NewProc("RegisterWindowMessageW")
	pDestroyWindow                 = user32.NewProc("DestroyWindow")
	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pSetTimer                      = user32.NewProc("SetTimer")
	pKillTimer                     = user32.NewProc("KillTimer")
	pSetWindowsHookExW             = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx           = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx                = user32.NewProc("CallNextHookEx")
	pGetAsyncKeyState              = user32.NewProc("GetAsyncKeyState")
	pGetCursorPos                  = user32.NewProc("GetCursorPos")
	pUpdateLayeredWindow           = user32.NewProc("UpdateLayeredWindow")
	pGetDC                         = user32.NewProc("GetDC")
	pReleaseDC                     = user32.NewProc("ReleaseDC")
	pCreateCompatibleDC            = gdi32.NewProc("CreateCompatibleDC")
	pDeleteDC                      = gdi32.NewProc("DeleteDC")
	pCreateDIBSection              = gdi32.NewProc("CreateDIBSection")
	pSelectObject                  = gdi32.NewProc("SelectObject")
	pDeleteObject                  = gdi32.NewProc("DeleteObject")
	pLoadIconW                     = user32.NewProc("LoadIconW")
	pCreateIconIndirect            = user32.NewProc("CreateIconIndirect")
	pDestroyIcon                   = user32.NewProc("DestroyIcon")
	pCreateBitmap                  = gdi32.NewProc("CreateBitmap")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pShellNotifyIconW              = shell32.NewProc("Shell_NotifyIconW")
	pCreatePopupMenu               = user32.NewProc("CreatePopupMenu")
	pAppendMenuW                   = user32.NewProc("AppendMenuW")
	pTrackPopupMenu                = user32.NewProc("TrackPopupMenu")
	pDestroyMenu                   = user32.NewProc("DestroyMenu")
	pSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	pPlaySoundW                    = winmm.NewProc("PlaySoundW")
)

const (
	WS_POPUP          = 0x80000000
	WS_EX_TOPMOST     = 0x00000008
	WS_EX_TRANSPARENT = 0x00000020
	WS_EX_TOOLWINDOW  = 0x00000080
	WS_EX_LAYERED     = 0x00080000
	WS_EX_NOACTIVATE  = 0x08000000
	SW_HIDE           = 0
	SW_SHOWNOACTIVATE = 4
	HWND_TOPMOST      = ^uintptr(0)
	SWP_NOACTIVATE    = 0x0010
	SWP_SHOWWINDOW    = 0x0040
	SWP_NOSIZE        = 0x0001
	SWP_NOMOVE        = 0x0002

	WM_DESTROY     = 0x0002
	WM_NULL        = 0
	WM_CONTEXTMENU = 0x007B
	WM_COMMAND     = 0x0111
	WM_TIMER       = 0x0113
	WM_LBUTTONDOWN = 0x0201
	WM_LBUTTONUP   = 0x0202
	WM_MOUSEMOVE   = 0x0200
	WM_RBUTTONDOWN = 0x0204
	WM_RBUTTONUP   = 0x0205
	WM_APP         = 0x8000

	WH_MOUSE_LL = 14
	VK_CONTROL  = 0x11
	VK_MENU     = 0x12 // Alt

	ULW_ALPHA      = 0x00000002
	AC_SRC_OVER    = 0x00
	AC_SRC_ALPHA   = 0x01
	DIB_RGB_COLORS = 0

	NIM_ADD              = 0x00000000
	NIM_MODIFY           = 0x00000001
	NIM_DELETE           = 0x00000002
	NIM_SETVERSION       = 0x00000004
	NOTIFYICON_VERSION_4 = 4
	NIN_SELECT           = 0x0400
	NIN_KEYSELECT        = 0x0401
	NIF_MESSAGE          = 0x00000001
	NIF_ICON             = 0x00000002
	NIF_TIP              = 0x00000004
	NIF_INFO             = 0x00000010
	NIF_SHOWTIP          = 0x00000080

	MF_STRING       = 0x00000000
	MF_CHECKED      = 0x00000008
	MF_SEPARATOR    = 0x00000800
	TPM_RIGHTBUTTON = 0x0002

	SND_ASYNC     = 0x0001
	SND_NODEFAULT = 0x0002
	SND_MEMORY    = 0x0004

	IDI_APPLICATION      = 32512
	ERROR_ALREADY_EXISTS = 183
)

type POINT struct {
	X, Y int32
}

type SIZE struct {
	CX, CY int32
}

type MSG struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type BITMAPINFO struct {
	BmiHeader BITMAPINFOHEADER
	BmiColors [1]uint32
}

type BLENDFUNCTION struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

type MSLLHOOKSTRUCT struct {
	Pt          POINT
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type NOTIFYICONDATAW struct {
	CbSize           uint32
	Hwnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

type ICONINFO struct {
	FIcon              uint32
	XHotspot, YHotspot uint32
	HbmMask, HbmColor  uintptr
}

// CreateIconFromResourceEx cannot reliably consume an arbitrary PNG: it expects
// an icon resource payload. Supply a real 32-bit alpha DIB and monochrome mask.
func createTrayIcon() uintptr {
	w, _, _ := pGetSystemMetrics.Call(49) // SM_CXSMICON
	h, _, _ := pGetSystemMetrics.Call(50) // SM_CYSMICON
	if w < 16 || w > 64 {
		w = 32
	}
	if h < 16 || h > 64 {
		h = 32
	}
	im := decodeAndResizePNG(mustAsset("assets/textures/pingwheel_basicpingrender.png"), int(w), int(h))
	s := newLayeredSurface(int(w), int(h))
	defer s.destroy()
	s.clear()
	s.blitPremult(im, int(w)/2, int(h)/2, 1)
	maskBits := make([]byte, ((int(w)+15)/16)*2*int(h))
	mask, _, _ := pCreateBitmap.Call(w, h, 1, 1, uintptr(unsafe.Pointer(&maskBits[0])))
	if mask == 0 {
		return 0
	}
	defer pDeleteObject.Call(mask)
	// The color bitmap must not be selected in a DC when passed to USER32.
	pSelectObject.Call(s.hdcMem, s.oldObj)
	info := ICONINFO{FIcon: 1, HbmMask: mask, HbmColor: s.hbmp}
	icon, _, _ := pCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	return icon
}

func addTrayIcon() {
	if app.trayAdded {
		return
	}
	r, _, _ := pShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&app.nid)))
	app.trayAdded = r != 0
	if app.trayAdded {
		app.nid.UVersion = NOTIFYICON_VERSION_4
		pShellNotifyIconW.Call(NIM_SETVERSION, uintptr(unsafe.Pointer(&app.nid)))
		pKillTimer.Call(app.trayHwnd, trayTimerID)
	} else {
		// Explorer may not yet own a taskbar, e.g. immediately after sign-in.
		pSetTimer.Call(app.trayHwnd, trayTimerID, 2000, 0)
	}
}

func updateTrayTooltip() {
	text := "lolPing Lite (Alt/G+Click: Ping | Ctrl+Click: Emote)"
	if !app.enabled {
		text = "lolPing Lite - Paused (click to resume)"
	}
	tip, _ := syscall.UTF16FromString(text)
	clear(app.nid.SzTip[:])
	copy(app.nid.SzTip[:], tip)
	if app.trayAdded {
		r, _, _ := pShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&app.nid)))
		if r == 0 {
			app.trayAdded = false
			addTrayIcon()
		}
	}
}

func enableDPIAware() {
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	if pSetProcessDpiAwarenessContext.Find() == nil {
		if r, _, _ := pSetProcessDpiAwarenessContext.Call(^uintptr(3)); r != 0 {
			return
		}
	}
	pSetProcessDPIAware.Call()
}

func isKeyDown(vk int) bool {
	r, _, _ := pGetAsyncKeyState.Call(uintptr(vk))
	return (r & 0x8000) != 0
}

var (
	pCreateFontW           = gdi32.NewProc("CreateFontW")
	pSetTextColor          = gdi32.NewProc("SetTextColor")
	pSetBkMode             = gdi32.NewProc("SetBkMode")
	pTextOutW              = gdi32.NewProc("TextOutW")
	pGetTextExtentPoint32W = gdi32.NewProc("GetTextExtentPoint32W")
	pGetTextMetricsW       = gdi32.NewProc("GetTextMetricsW")
	pGdiFlush              = gdi32.NewProc("GdiFlush")
)

type TEXTMETRICW struct {
	TmHeight, TmAscent, TmDescent, TmInternalLeading, TmExternalLeading                          int32
	TmAveCharWidth, TmMaxCharWidth, TmWeight, TmOverhang, TmDigitizedAspectX, TmDigitizedAspectY int32
	TmFirstChar, TmLastChar, TmDefaultChar, TmBreakChar                                          uint16
	TmItalic, TmUnderlined, TmStruckOut, TmPitchAndFamily, TmCharSet                             byte
}
