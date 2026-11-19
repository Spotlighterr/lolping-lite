//go:build windows

package main

import (
	"embed"
	"math"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

//go:embed assets/sounds/Base.wav assets/sounds/SRP_12.wav assets/sounds/SRP_9.wav assets/sounds/OnMyWay.wav assets/sounds/SRP_4.wav assets/sounds/ComeHere.wav assets/sounds/AreaIsWarded.wav assets/sounds/MIA.wav assets/sounds/SRP_7.wav assets/sounds/button.wav
//go:embed assets/textures/pingwheel_basicpingrender.png assets/textures/pingwheel_retreatrender.png assets/textures/pingwheel_pushrender.png assets/textures/omw_cyan.png assets/textures/png_all_in.png assets/textures/png_assist_me_128x128.png assets/textures/pingwheel_enemyvisionrender.png assets/textures/png_enemymissing_128x128.png assets/textures/mt11_18_vision.png
//go:embed assets/emotes/*.png
//go:embed assets/textures/pingwheel_allin_render_01.png assets/textures/pingwheel_allin_render_02.png
//go:embed assets/emotes/*.wav
var embeddedAssets embed.FS

const (
	wmTrayIcon   = WM_APP + 1
	wmWheelEvt   = WM_APP + 2
	wmVsyncTick  = WM_APP + 3
	cmdToggle    = 1001
	cmdTest      = 1002
	cmdQuit      = 1003
	popTimerID   = 1
	wheelTimerID = 2
	trayTimerID  = 3
	soundTimerID = 4

	wheelSize = 600
	popSize   = 320
	vkG       = 0x47
)

type wheelSlot struct {
	label     string
	frames    string
	icon      *premultImg
	tinted    *premultImg
	popIcon   *premultImg
	popSecond *premultImg
	sound     string
	r, g, b   uint8
}

type appState struct {
	hInstance       uintptr
	trayHwnd        uintptr
	wheelHwnd       uintptr
	popHwnd         uintptr
	nid             NOTIFYICONDATAW
	mouseHook       uintptr
	taskbarCreated  uint32
	trayIcon        uintptr
	trayAdded       bool
	wheelDirty      bool
	wheelClosing    bool
	wheelOpacity    float64
	wheelTransition time.Time
	cancelled       bool
	cancelRight     bool
	wheelMoved      bool
	emoteCenter     *wheelSlot
	wedge           [8]float64
	wedgeFrom       [8]float64
	hover           [8]float64
	hoverFrom       [8]float64
	hoverStart      time.Time
	hoverIdx        int

	enabled   bool
	dragging  bool
	emoteMode bool
	anchorX   int32
	anchorY   int32
	curX      int32
	curY      int32
	activeIdx int

	wheelSurf *layeredSurface
	popSurf   *layeredSurface

	pingCenter *wheelSlot
	pingSlots  []*wheelSlot
	emoteSlots []*wheelSlot

	effects []*popEffect

	vsyncWake    chan struct{}
	vsyncStop    chan struct{}
	vsyncActive  atomic.Bool
	vsyncPending atomic.Bool
}

var app *appState

func wakeVsync() {
	if app == nil || app.vsyncWake == nil {
		return
	}
	app.vsyncActive.Store(true)
	select {
	case app.vsyncWake <- struct{}{}:
	default:
	}
}

func runVsyncPacer() {
	for {
		select {
		case <-app.vsyncStop:
			return
		case <-app.vsyncWake:
		}
		for app.vsyncActive.Load() {
			select {
			case <-app.vsyncStop:
				return
			default:
			}
			r, _, _ := pDwmFlush.Call()
			if r != 0 {
				time.Sleep(7 * time.Millisecond)
			}
			if app.vsyncActive.Load() && app.trayHwnd != 0 && app.vsyncPending.CompareAndSwap(false, true) {
				pPostMessageW.Call(app.trayHwnd, wmVsyncTick, 0, 0)
			}
		}
	}
}

func mustAsset(path string) []byte {
	b, err := embeddedAssets.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return b
}

func loadSlots() {
	app.pingCenter = &wheelSlot{
		icon:    decodePNG(mustAsset("assets/textures/pingwheel_basicpingrender.png"), 40, 40, false),
		popIcon: decodePNG(mustAsset("assets/textures/pingwheel_basicpingrender.png"), 110, 110, false),
		sound:   "assets/sounds/Base.wav",
		r:       46, g: 139, b: 255,
	}

	pingDefs := []struct {
		tex     string
		snd     string
		r, g, b uint8
	}{
		{"assets/textures/pingwheel_retreatrender.png", "assets/sounds/SRP_12.wav", 255, 59, 85},           // Top: Danger / Retreat
		{"assets/textures/pingwheel_pushrender.png", "assets/sounds/SRP_9.wav", 53, 210, 122},              // Top-Right: Push
		{"assets/textures/omw_cyan.png", "assets/sounds/OnMyWay.wav", 46, 139, 255},                        // Right: On My Way
		{"assets/textures/png_all_in.png", "assets/sounds/SRP_4.wav", 242, 179, 32},                        // Bottom-Right: All In (complete crossed swords)
		{"assets/textures/png_assist_me_128x128.png", "assets/sounds/ComeHere.wav", 53, 210, 122},          // Bottom: Assist Me
		{"assets/textures/mt11_18_vision.png", "assets/sounds/SRP_7.wav", 53, 210, 122},                    // Bottom-Left: Need Vision
		{"assets/textures/png_enemymissing_128x128.png", "assets/sounds/MIA.wav", 242, 197, 24},            // Left: Enemy Missing
		{"assets/textures/pingwheel_enemyvisionrender.png", "assets/sounds/AreaIsWarded.wav", 255, 59, 85}, // Top-Left: Enemy Vision
	}
	for _, d := range pingDefs {
		app.pingSlots = append(app.pingSlots, &wheelSlot{
			icon:    decodePNG(mustAsset(d.tex), 48, 48, false),
			popIcon: decodePNG(mustAsset(d.tex), 110, 110, false),
			sound:   d.snd,
			r:       d.r, g: d.g, b: d.b,
		})
	}
	// Preserve the shared export canvas so the two sword layers align on impact.
	app.pingSlots[3].popIcon = decodePNG(mustAsset("assets/textures/pingwheel_allin_render_01.png"), 110, 110, false)
	app.pingSlots[3].popSecond = decodePNG(mustAsset("assets/textures/pingwheel_allin_render_02.png"), 110, 110, false)

	names := []string{"Danger", "Push", "On My Way", "All In", "Assist Me", "Need Vision", "Enemy Missing", "Enemy Vision"}
	for i, slot := range app.pingSlots {
		slot.label = names[i]
		slot.tinted = tintPing(slot.icon)
	}
	slugs := []string{"unworthy_cn", "poro_snax", "sad_kitten", "cheeky_poro", "angry_kitty", "gg_heart", "bee_happy", "bee_mad", "facepalm"}
	labels := []string{"Unworthy (Chinese text)", "Poro Snax", "Sad Kitten", "Cheeky Poro", "Angry Kitty", "GG <3", "Bee Happy", "Bee Mad", "Facepalm"}
	for i, slug := range slugs {
		slot := &wheelSlot{icon: roundIcon(decodePNG(mustAsset("assets/emotes/"+slug+".png"), 48, 48, false)),
			popIcon: decodePNG(mustAsset("assets/emotes/"+slug+".png"), 150, 150, false),
			label:   labels[i], sound: "assets/emotes/" + slug + ".wav", frames: "assets/emotes/" + slug + ".frames"}
		if i == 8 {
			app.emoteCenter = slot
		} else {
			app.emoteSlots = append(app.emoteSlots, slot)
		}
	}

}

func sectorFromDelta(dx, dy float64) int {
	dist := math.Hypot(dx, dy)
	if dist < hubRadius*wheelSize/600 {
		return -1
	}
	ang := math.Atan2(dy, dx)*180.0/math.Pi + 90.0 + 22.5
	if ang < 0 {
		ang += 360.0
	}
	if ang >= 360.0 {
		ang -= 360.0
	}
	sec := int(ang / 45.0)
	if sec < 0 || sec > 7 {
		return 0
	}
	return sec
}

func mouseHookProc(nCode int32, wParam uintptr, ms *MSLLHOOKSTRUCT) uintptr {
	if nCode >= 0 && app.enabled {
		switch uint32(wParam) {
		case WM_LBUTTONDOWN:
			if !app.dragging {
				alt := isKeyDown(VK_MENU) || isKeyDown(vkG)
				ctrl := isKeyDown(VK_CONTROL)
				if alt || ctrl {
					app.dragging = true
					app.emoteMode = ctrl && !alt
					app.anchorX = ms.Pt.X
					app.anchorY = ms.Pt.Y
					app.curX = ms.Pt.X
					app.curY = ms.Pt.Y
					app.activeIdx = -1
					app.cancelled = false
					app.wheelMoved = false
					pPostMessageW.Call(app.trayHwnd, wmWheelEvt, 1, 0)
					return 1
				}
			}
		case WM_RBUTTONDOWN:
			if app.dragging {
				app.dragging = false
				app.cancelled = true
				app.cancelRight = true
				pPostMessageW.Call(app.trayHwnd, wmWheelEvt, 4, 0)
				return 1
			}
		case WM_RBUTTONUP:
			if app.cancelRight {
				app.cancelRight = false
				return 1
			}
		case WM_MOUSEMOVE:
			if app.dragging {
				app.curX = ms.Pt.X
				app.curY = ms.Pt.Y
				app.activeIdx = sectorFromDelta(float64(ms.Pt.X-app.anchorX), float64(ms.Pt.Y-app.anchorY))
				if math.Hypot(float64(ms.Pt.X-app.anchorX), float64(ms.Pt.Y-app.anchorY)) > 8 {
					app.wheelMoved = true
				}
				app.wheelDirty = true
			}
		case WM_LBUTTONUP:
			if app.cancelled {
				app.cancelled = false
				return 1
			}
			if app.dragging {
				app.curX, app.curY = ms.Pt.X, ms.Pt.Y
				app.activeIdx = sectorFromDelta(float64(ms.Pt.X-app.anchorX), float64(ms.Pt.Y-app.anchorY))
				app.dragging = false
				if app.activeIdx < 0 && app.wheelMoved {
					pPostMessageW.Call(app.trayHwnd, wmWheelEvt, 4, 0)
				} else {
					pPostMessageW.Call(app.trayHwnd, wmWheelEvt, 3, 0)
				}
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(app.mouseHook, uintptr(nCode), wParam, uintptr(unsafe.Pointer(ms)))
	return r
}

func renderWheelNow() {
	slots := app.pingSlots
	center := app.pingCenter.icon
	if app.emoteMode {
		slots = app.emoteSlots
		center = app.emoteCenter.icon
	}
	var icons []*premultImg
	now := time.Now()
	if app.hoverIdx != app.activeIdx {
		app.hoverFrom = app.hover
		app.wedgeFrom = app.wedge
		app.hoverStart = now
		app.hoverIdx = app.activeIdx
		if app.activeIdx >= 0 && app.dragging {
			playSoundVolume("assets/sounds/button.wav", .35)
		}
	}
	u := cssEase(now.Sub(app.hoverStart).Seconds()/.1, .25, .1, .25, 1)
	for i, slot := range slots {
		target := 0.0
		if i == app.activeIdx {
			target = 1
		}
		app.hover[i] = lerp(app.hoverFrom[i], target, u)
		app.wedge[i] = lerp(app.wedgeFrom[i], target, cssEase(now.Sub(app.hoverStart).Seconds()/.08, .25, .1, .25, 1))
		icons = append(icons, slot.icon)
	}
	app.wheelSurf.drawWheel(icons, center, app.activeIdx, 0, 0, nil, 0, 0, 0)
	t := cssEase(now.Sub(app.wheelTransition).Seconds()/.12, 0, 0, .58, 1)
	opacity := t
	if app.wheelClosing {
		opacity = app.wheelOpacity * (1 - t)
	}
	app.wheelSurf.presentAnimated(app.wheelHwnd, app.anchorX-wheelSize/2, app.anchorY-wheelSize/2, opacity, .85+.15*opacity)
	if app.wheelClosing && t >= 1 {
		pKillTimer.Call(app.trayHwnd, wheelTimerID)
		pShowWindow.Call(app.wheelHwnd, SW_HIDE)
		app.wheelClosing = false
	}
}
func closeWheel() {
	app.activeIdx = -1
	app.wheelOpacity = cssEase(time.Since(app.wheelTransition).Seconds()/.12, 0, 0, .58, 1)
	app.wheelClosing = true
	app.wheelTransition = time.Now()
	wakeVsync()
}

func triggerPop() {
	app.wheelDirty = false
	var slot *wheelSlot
	if app.emoteMode {
		if app.activeIdx >= 0 && app.activeIdx < len(app.emoteSlots) {
			slot = app.emoteSlots[app.activeIdx]
		} else {
			slot = app.emoteCenter
		}
	} else {
		if app.activeIdx >= 0 && app.activeIdx < len(app.pingSlots) {
			slot = app.pingSlots[app.activeIdx]
		} else {
			slot = app.pingCenter
		}
	}

	closeWheel()
	playSoundAsset(slot.sound)
	startEffect(slot, app.emoteMode)
}

func showTrayMenu(hwnd uintptr) {
	menu, _, _ := pCreatePopupMenu.Call()
	var flags uintptr = MF_STRING
	if app.enabled {
		flags |= MF_CHECKED
	}
	lblEnabled, _ := syscall.UTF16PtrFromString("Enabled (Alt/G+Click: Ping | Ctrl+Click: Emote)")
	lblTest, _ := syscall.UTF16PtrFromString("Test Ping Now")
	lblQuit, _ := syscall.UTF16PtrFromString("Quit lolPing Lite")
	pAppendMenuW.Call(menu, flags, cmdToggle, uintptr(unsafe.Pointer(lblEnabled)))
	pAppendMenuW.Call(menu, MF_STRING, cmdTest, uintptr(unsafe.Pointer(lblTest)))
	pAppendMenuW.Call(menu, MF_SEPARATOR, 0, 0)
	pAppendMenuW.Call(menu, MF_STRING, cmdQuit, uintptr(unsafe.Pointer(lblQuit)))

	var pt POINT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	pSetForegroundWindow.Call(hwnd)
	pTrackPopupMenu.Call(menu, TPM_RIGHTBUTTON, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	pPostMessageW.Call(hwnd, WM_NULL, 0, 0)
	pDestroyMenu.Call(menu)
}

func wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	if app != nil && app.taskbarCreated != 0 && msg == app.taskbarCreated {
		app.trayAdded = false
		addTrayIcon()
		return 0
	}
	switch msg {
	case wmWheelEvt:
		switch wParam {
		case 1:
			app.wheelClosing = false
			app.wheelTransition = time.Now()
			app.hoverIdx = -1
			app.hover = [8]float64{}
			app.wedge = [8]float64{}
			app.hoverFrom = app.hover
			app.wedgeFrom = app.wedge
			app.wheelDirty = true
			renderWheelNow()
			pSetWindowPos.Call(app.wheelHwnd, HWND_TOPMOST, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE|SWP_SHOWWINDOW)
			wakeVsync()
		case 3:
			triggerPop()
		case 4:
			closeWheel()
		}
		return 0
	case wmVsyncTick:
		app.vsyncPending.Store(false)
		if app.dragging || app.wheelClosing {
			needDraw := app.wheelClosing || app.wheelDirty || app.hoverIdx != app.activeIdx ||
				time.Since(app.hoverStart) <= 130*time.Millisecond ||
				time.Since(app.wheelTransition) <= 150*time.Millisecond
			if needDraw {
				app.wheelDirty = false
				renderWheelNow()
			}
		}
		if len(app.effects) > 0 {
			tickEffects()
		}
		if !app.dragging && !app.wheelClosing && len(app.effects) == 0 {
			app.vsyncActive.Store(false)
		}
		return 0
	case WM_TIMER:
		if wParam == smokeTimerID {
			smokeTick()
			return 0
		}
		reapSounds()
		if wParam == soundTimerID {
			if len(voices) == 0 {
				pKillTimer.Call(app.trayHwnd, soundTimerID)
			}
			return 0
		}
		if wParam == trayTimerID {
			if !app.trayAdded {
				addTrayIcon()
			}
			return 0
		}
		if wParam == wheelTimerID {
			if app.dragging || app.wheelClosing {
				needDraw := app.wheelClosing || app.wheelDirty || app.hoverIdx != app.activeIdx ||
					time.Since(app.hoverStart) <= 130*time.Millisecond ||
					time.Since(app.wheelTransition) <= 150*time.Millisecond
				if needDraw {
					app.wheelDirty = false
					renderWheelNow()
				}
			}
			return 0
		}
		if wParam == popTimerID {
			tickEffects()
		}
		return 0
	case wmTrayIcon:
		if event := uint32(lParam) & 0xFFFF; event == WM_RBUTTONUP || event == WM_LBUTTONUP || event == WM_CONTEXTMENU || event == NIN_SELECT || event == NIN_KEYSELECT {
			showTrayMenu(hwnd)
		}
		return 0
	case WM_COMMAND:
		switch uint32(wParam & 0xFFFF) {
		case cmdToggle:
			app.enabled = !app.enabled
			if !app.enabled {
				app.dragging = false
				pKillTimer.Call(app.trayHwnd, wheelTimerID)
				pShowWindow.Call(app.wheelHwnd, SW_HIDE)
			}
			updateTrayTooltip()
		case cmdTest:
			var pt POINT
			pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			app.emoteMode = false
			app.activeIdx = -1
			app.anchorX = pt.X
			app.anchorY = pt.Y
			triggerPop()
		case cmdQuit:
			pPostQuitMessage.Call(0)
		}
		return 0
	case WM_DESTROY:
		if app != nil && hwnd == app.trayHwnd {
			pPostQuitMessage.Call(0)
		}
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

func main() {
	runtime.LockOSThread()
	debug.SetMemoryLimit(48 << 20)
	enableDPIAware()

	mutexName := "Local\\lolPingLiteMutex"
	if smokeMode {
		mutexName += ".Smoke"
	}
	mtxName, _ := syscall.UTF16PtrFromString(mutexName)
	mutex, _, lastErr := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(mtxName)))
	if mutex != 0 {
		defer pCloseHandle.Call(mutex)
	}
	if lastErr == syscall.Errno(ERROR_ALREADY_EXISTS) {
		return
	}

	hInst, _, _ := pGetModuleHandleW.Call(0)
	app = &appState{
		hInstance: hInst,
		enabled:   true,
		activeIdx: -1,
		wheelSurf: newLayeredSurface(wheelSize, wheelSize),
		popSurf:   newLayeredSurface(popSize, popSize),
		vsyncWake: make(chan struct{}, 1),
		vsyncStop: make(chan struct{}),
	}
	go runVsyncPacer()
	defer close(app.vsyncStop)
	loadSlots()
	app.wheelSurf.buildWheelBase()
	prewarmShadows()

	clsName, _ := syscall.UTF16PtrFromString("lolPingLiteClass")
	hIcon := createTrayIcon()
	if hIcon != 0 {
		defer pDestroyIcon.Call(hIcon)
	}
	if hIcon == 0 {
		hIcon, _, _ = pLoadIconW.Call(0, IDI_APPLICATION)
	}
	app.trayIcon = hIcon
	taskbarMsg, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	registered, _, _ := pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(taskbarMsg)))
	app.taskbarCreated = uint32(registered)
	wc := WNDCLASSEXW{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     hInst,
		HIcon:         hIcon,
		HIconSm:       hIcon,
		LpszClassName: clsName,
	}
	if r, _, _ := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		panic("RegisterClassExW failed")
	}

	title, _ := syscall.UTF16PtrFromString("lolPing Lite")
	app.trayHwnd, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(clsName)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 0, 0, 0, 0, hInst, 0)

	exOverlay := uintptr(WS_EX_LAYERED | WS_EX_TOPMOST | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE | WS_EX_TRANSPARENT)
	app.wheelHwnd, _, _ = pCreateWindowExW.Call(exOverlay, uintptr(unsafe.Pointer(clsName)), 0, WS_POPUP, 0, 0, wheelSize, wheelSize, 0, 0, hInst, 0)
	app.popHwnd, _, _ = pCreateWindowExW.Call(exOverlay, uintptr(unsafe.Pointer(clsName)), 0, WS_POPUP, 0, 0, popSize, popSize, 0, 0, hInst, 0)
	if app.trayHwnd == 0 || app.wheelHwnd == 0 || app.popHwnd == 0 {
		panic("CreateWindowExW failed")
	}

	app.nid = NOTIFYICONDATAW{
		CbSize:           uint32(unsafe.Sizeof(NOTIFYICONDATAW{})),
		Hwnd:             app.trayHwnd,
		UID:              1,
		UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP | NIF_SHOWTIP,
		UCallbackMessage: wmTrayIcon,
		HIcon:            hIcon,
	}
	tipUTF16, _ := syscall.UTF16FromString("lolPing Lite (Alt/G+Click: Ping | Ctrl+Click: Emote)")
	copy(app.nid.SzTip[:], tipUTF16)
	addTrayIcon()

	app.mouseHook, _, _ = pSetWindowsHookExW.Call(WH_MOUSE_LL, syscall.NewCallback(mouseHookProc), hInst, 0)
	if app.mouseHook == 0 {
		panic("SetWindowsHookExW failed")
	}
	// Release PNG decoder scratch memory before entering the idle message loop.
	runtime.GC()
	debug.FreeOSMemory()

	if smokeMode {
		smoke.start = time.Now()
		pSetTimer.Call(app.trayHwnd, smokeTimerID, 50, 0)
	}
	var msg MSG
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	if app.mouseHook != 0 {
		pUnhookWindowsHookEx.Call(app.mouseHook)
	}
	pShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&app.nid)))
	stopEffects()
	stopSounds()
	pDestroyWindow.Call(app.wheelHwnd)
	pDestroyWindow.Call(app.popHwnd)
	pDestroyWindow.Call(app.trayHwnd)
	app.wheelSurf.destroy()
	app.popSurf.destroy()
}
