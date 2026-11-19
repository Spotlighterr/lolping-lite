//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

type AppConfig struct {
	PingKey   string `json:"ping_key"`
	EmoteKey  string `json:"emote_key"`
	Volume    int    `json:"volume"`
	AutoStart bool   `json:"auto_start"`
}

var (
	advapi32         = syscall.NewLazyDLL("advapi32.dll")
	pRegCreateKeyExW = advapi32.NewProc("RegCreateKeyExW")
	pRegSetValueExW  = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValueW = advapi32.NewProc("RegDeleteValueW")
	pRegQueryValueEx = advapi32.NewProc("RegQueryValueExW")
	pRegCloseKey     = advapi32.NewProc("RegCloseKey")

	pSendMessageW = user32.NewProc("SendMessageW")
	pIsWindow     = user32.NewProc("IsWindow")

	cfg = AppConfig{
		PingKey:  "alt_or_g",
		EmoteKey: "ctrl",
		Volume:   70,
	}

	settingsHwnd     uintptr
	settingsFont     uintptr
	cbPingHwnd       uintptr
	cbEmoteHwnd      uintptr
	cbVolumeHwnd     uintptr
	chkAutoStartHwnd uintptr
	settingsClassReg bool
)

const (
	hkeyCurrentUser = 0x80000001
	keyWrite        = 0x20006
	keyRead         = 0x20019
	regSZ           = 1

	wsOverlapped  = 0x00000000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
	wsVisible     = 0x10000000
	wsChild       = 0x40000000
	wsTabStop     = 0x00010000
	cbsDropDownL  = 0x0003
	bsAutoCheck   = 0x0003
	bsDefPushBtn  = 0x0001
	bsPushBtn     = 0x0000

	wmSetFont    = 0x0030
	wmClose      = 0x0010
	cbAddString  = 0x0143
	cbSetCurSel  = 0x014E
	cbGetCurSel  = 0x0147
	bmGetCheck   = 0x00F0
	bmSetCheck   = 0x00F1
	bstChecked   = 1
	bstUnchecked = 0

	vkShift = 0x10
	vkT     = 0x54
	vkV     = 0x56
	vkY     = 0x59

	idBtnSave  = 2001
	idBtnTest  = 2002
	idBtnClose = 2003
)

type keyOption struct {
	id    string
	label string
}

var pingKeyOptions = []keyOption{
	{"alt_or_g", "Alt or G + Left Click (Default)"},
	{"alt", "Alt + Left Click"},
	{"g", "G + Left Click"},
	{"v", "V + Left Click"},
	{"shift", "Shift + Left Click"},
}

var emoteKeyOptions = []keyOption{
	{"ctrl", "Ctrl + Left Click (Default)"},
	{"t", "T + Left Click"},
	{"y", "Y + Left Click"},
	{"shift", "Shift + Left Click"},
	{"disabled", "Disabled"},
}

var volumeOptions = []struct {
	val   int
	label string
}{
	{0, "0% (Mute)"},
	{25, "25%"},
	{50, "50%"},
	{70, "70% (Default)"},
	{100, "100%"},
}

func configPath() string {
	dir := os.Getenv("APPDATA")
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "lolPingLite", "config.json")
}

func loadConfig() {
	cfg.AutoStart = getAutoStart()
	b, err := os.ReadFile(configPath())
	if err != nil {
		return
	}
	var loaded AppConfig
	if json.Unmarshal(b, &loaded) == nil {
		if loaded.PingKey != "" {
			cfg.PingKey = loaded.PingKey
		}
		if loaded.EmoteKey != "" {
			cfg.EmoteKey = loaded.EmoteKey
		}
		if loaded.Volume >= 0 && loaded.Volume <= 100 {
			cfg.Volume = loaded.Volume
		}
	}
	cfg.AutoStart = getAutoStart()
}

func saveConfig() {
	p := configPath()
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	if b, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		_ = os.WriteFile(p, b, 0644)
	}
	setAutoStart(cfg.AutoStart)
}

func getAutoStart() bool {
	sub, _ := syscall.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Run`)
	valName, _ := syscall.UTF16PtrFromString("lolPingLite")
	var hKey uintptr
	r, _, _ := pRegCreateKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(sub)), 0, 0, 0, keyRead, 0, uintptr(unsafe.Pointer(&hKey)), 0)
	if r != 0 {
		return false
	}
	defer pRegCloseKey.Call(hKey)
	var typ, sz uint32
	r2, _, _ := pRegQueryValueEx.Call(hKey, uintptr(unsafe.Pointer(valName)), 0, uintptr(unsafe.Pointer(&typ)), 0, uintptr(unsafe.Pointer(&sz)))
	return r2 == 0 && sz > 0
}

func setAutoStart(enabled bool) {
	sub, _ := syscall.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Run`)
	valName, _ := syscall.UTF16PtrFromString("lolPingLite")
	var hKey uintptr
	r, _, _ := pRegCreateKeyExW.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(sub)), 0, 0, 0, keyWrite, 0, uintptr(unsafe.Pointer(&hKey)), 0)
	if r != 0 {
		return
	}
	defer pRegCloseKey.Call(hKey)
	if !enabled {
		pRegDeleteValueW.Call(hKey, uintptr(unsafe.Pointer(valName)))
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmdUTF16, _ := syscall.UTF16FromString(`"` + exe + `"`)
	pRegSetValueExW.Call(
		hKey,
		uintptr(unsafe.Pointer(valName)),
		0,
		regSZ,
		uintptr(unsafe.Pointer(&cmdUTF16[0])),
		uintptr(len(cmdUTF16)*2),
	)
}

func isPingTriggerDown() bool {
	switch cfg.PingKey {
	case "alt":
		return isKeyDown(VK_MENU)
	case "g":
		return isKeyDown(vkG)
	case "v":
		return isKeyDown(vkV)
	case "shift":
		return isKeyDown(vkShift)
	default:
		return isKeyDown(VK_MENU) || isKeyDown(vkG)
	}
}

func isEmoteTriggerDown() bool {
	switch cfg.EmoteKey {
	case "disabled":
		return false
	case "t":
		return isKeyDown(vkT)
	case "y":
		return isKeyDown(vkY)
	case "shift":
		return isKeyDown(vkShift)
	default:
		return isKeyDown(VK_CONTROL)
	}
}

func pingVolume() float64 {
	return float64(cfg.Volume) / 100.0
}

func hoverVolume() float64 {
	return float64(cfg.Volume) / 200.0
}

func settingsWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_COMMAND:
		switch uint32(wParam & 0xFFFF) {
		case idBtnSave:
			if idx, _, _ := pSendMessageW.Call(cbPingHwnd, cbGetCurSel, 0, 0); int(idx) >= 0 && int(idx) < len(pingKeyOptions) {
				cfg.PingKey = pingKeyOptions[idx].id
			}
			if idx, _, _ := pSendMessageW.Call(cbEmoteHwnd, cbGetCurSel, 0, 0); int(idx) >= 0 && int(idx) < len(emoteKeyOptions) {
				cfg.EmoteKey = emoteKeyOptions[idx].id
			}
			if idx, _, _ := pSendMessageW.Call(cbVolumeHwnd, cbGetCurSel, 0, 0); int(idx) >= 0 && int(idx) < len(volumeOptions) {
				cfg.Volume = volumeOptions[idx].val
			}
			chk, _, _ := pSendMessageW.Call(chkAutoStartHwnd, bmGetCheck, 0, 0)
			cfg.AutoStart = chk == bstChecked
			saveConfig()
			updateTrayTooltip()
			pDestroyWindow.Call(hwnd)
			return 0
		case idBtnTest:
			if app != nil {
				var pt POINT
				pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
				app.emoteMode = false
				app.activeIdx = -1
				app.anchorX = pt.X
				app.anchorY = pt.Y
				playSoundVolume(app.pingCenter.sound, pingVolume())
				startEffect(app.pingCenter, false)
			}
			return 0
		case idBtnClose:
			pDestroyWindow.Call(hwnd)
			return 0
		}
	case wmClose:
		pDestroyWindow.Call(hwnd)
		return 0
	case WM_DESTROY:
		settingsHwnd = 0
		if settingsFont != 0 {
			pDeleteObject.Call(settingsFont)
			settingsFont = 0
		}
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func openSettingsWindow() {
	if settingsHwnd != 0 {
		if ok, _, _ := pIsWindow.Call(settingsHwnd); ok != 0 {
			pShowWindow.Call(settingsHwnd, SW_SHOWNOACTIVATE)
			pSetForegroundWindow.Call(settingsHwnd)
			return
		}
		settingsHwnd = 0
	}

	clsName, _ := syscall.UTF16PtrFromString("lolPingLiteSettingsClass")
	if !settingsClassReg {
		wc := WNDCLASSEXW{
			CbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
			LpfnWndProc:   syscall.NewCallback(settingsWndProc),
			HInstance:     app.hInstance,
			HIcon:         app.trayIcon,
			HIconSm:       app.trayIcon,
			HbrBackground: 16, // COLOR_BTNFACE + 1
			LpszClassName: clsName,
		}
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		settingsClassReg = true
	}

	const winW, winH = 400, 275
	sw, _, _ := pGetSystemMetrics.Call(0)
	sh, _, _ := pGetSystemMetrics.Call(1)
	x := (int(sw) - winW) / 2
	y := (int(sh) - winH) / 2

	title, _ := syscall.UTF16PtrFromString("lolPing Lite - Settings")
	style := uintptr(wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox | wsVisible)
	settingsHwnd, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(clsName)), uintptr(unsafe.Pointer(title)), style, uintptr(x), uintptr(y), winW, winH, 0, 0, app.hInstance, 0)
	if settingsHwnd == 0 {
		return
	}

	face, _ := syscall.UTF16PtrFromString("Segoe UI")
	settingsFont, _, _ = pCreateFontW.Call(uintptr(^uintptr(15)+1), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))

	clsStatic, _ := syscall.UTF16PtrFromString("STATIC")
	clsCombo, _ := syscall.UTF16PtrFromString("COMBOBOX")
	clsBtn, _ := syscall.UTF16PtrFromString("BUTTON")

	addCtrl := func(cls *uint16, text string, st uintptr, cx, cy, cw, ch, id uintptr) uintptr {
		txt, _ := syscall.UTF16PtrFromString(text)
		h, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(txt)), st, cx, cy, cw, ch, settingsHwnd, id, app.hInstance, 0)
		if settingsFont != 0 && h != 0 {
			pSendMessageW.Call(h, wmSetFont, settingsFont, 1)
		}
		return h
	}

	// Ping Hotkey
	addCtrl(clsStatic, "Ping Wheel Hotkey:", wsChild|wsVisible, 20, 22, 130, 22, 0)
	cbPingHwnd = addCtrl(clsCombo, "", wsChild|wsVisible|wsTabStop|cbsDropDownL, 155, 18, 210, 160, 0)
	pingSel := 0
	for i, o := range pingKeyOptions {
		s, _ := syscall.UTF16PtrFromString(o.label)
		pSendMessageW.Call(cbPingHwnd, cbAddString, 0, uintptr(unsafe.Pointer(s)))
		if o.id == cfg.PingKey {
			pingSel = i
		}
	}
	pSendMessageW.Call(cbPingHwnd, cbSetCurSel, uintptr(pingSel), 0)

	// Emote Hotkey
	addCtrl(clsStatic, "Emote Wheel Hotkey:", wsChild|wsVisible, 20, 60, 130, 22, 0)
	cbEmoteHwnd = addCtrl(clsCombo, "", wsChild|wsVisible|wsTabStop|cbsDropDownL, 155, 56, 210, 160, 0)
	emoteSel := 0
	for i, o := range emoteKeyOptions {
		s, _ := syscall.UTF16PtrFromString(o.label)
		pSendMessageW.Call(cbEmoteHwnd, cbAddString, 0, uintptr(unsafe.Pointer(s)))
		if o.id == cfg.EmoteKey {
			emoteSel = i
		}
	}
	pSendMessageW.Call(cbEmoteHwnd, cbSetCurSel, uintptr(emoteSel), 0)

	// Volume
	addCtrl(clsStatic, "Sound Volume:", wsChild|wsVisible, 20, 98, 130, 22, 0)
	cbVolumeHwnd = addCtrl(clsCombo, "", wsChild|wsVisible|wsTabStop|cbsDropDownL, 155, 94, 210, 160, 0)
	volSel := 3
	for i, o := range volumeOptions {
		s, _ := syscall.UTF16PtrFromString(o.label)
		pSendMessageW.Call(cbVolumeHwnd, cbAddString, 0, uintptr(unsafe.Pointer(s)))
		if o.val == cfg.Volume {
			volSel = i
		}
	}
	pSendMessageW.Call(cbVolumeHwnd, cbSetCurSel, uintptr(volSel), 0)

	// Auto-start checkbox
	chkAutoStartHwnd = addCtrl(clsBtn, "Start lolPing Lite automatically with Windows", wsChild|wsVisible|wsTabStop|bsAutoCheck, 20, 140, 345, 24, 0)
	if cfg.AutoStart {
		pSendMessageW.Call(chkAutoStartHwnd, bmSetCheck, bstChecked, 0)
	} else {
		pSendMessageW.Call(chkAutoStartHwnd, bmSetCheck, bstUnchecked, 0)
	}

	// Buttons
	addCtrl(clsBtn, "Save & Apply", wsChild|wsVisible|wsTabStop|bsDefPushBtn, 20, 188, 115, 32, idBtnSave)
	addCtrl(clsBtn, "Test Ping", wsChild|wsVisible|wsTabStop|bsPushBtn, 148, 188, 105, 32, idBtnTest)
	addCtrl(clsBtn, "Close", wsChild|wsVisible|wsTabStop|bsPushBtn, 266, 188, 100, 32, idBtnClose)

	pSetForegroundWindow.Call(settingsHwnd)
}
