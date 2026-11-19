//go:build windows

package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

func softwareSurface(size int) *layeredSurface {
	return &layeredSurface{width: size, height: size, pixels: make([]byte, size*size*4)}
}
func assertPremult(t *testing.T, s *layeredSurface) {
	t.Helper()
	for i := 0; i < len(s.pixels); i += 4 {
		for k := 0; k < 3; k++ {
			if s.pixels[i+k] > s.pixels[i+3] {
				t.Fatalf("RGB exceeds alpha at pixel %d: %v", i/4, s.pixels[i:i+4])
			}
		}
	}
}
func preview(t *testing.T, name string, s *layeredSurface) {
	t.Helper()
	if os.Getenv("LOLPING_PREVIEW") == "" {
		return
	}
	dst := image.NewNRGBA(image.Rect(0, 0, s.width, s.height))
	// Composite onto a desktop-like dark backdrop, keeping alpha fringes visible.
	for y := 0; y < s.height; y++ {
		for x := 0; x < s.width; x++ {
			i := (y*s.width + x) * 4
			inv := 255 - int(s.pixels[i+3])
			dst.SetNRGBA(x, y, color.NRGBA{uint8(int(s.pixels[i+2]) + 23*inv/255), uint8(int(s.pixels[i+1]) + 30*inv/255), uint8(int(s.pixels[i]) + 40*inv/255), 255})
		}
	}
	if err := os.MkdirAll(".tmp", 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(".tmp", name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = png.Encode(f, dst); err != nil {
		t.Fatal(err)
	}
}
func TestWheelAssetsAndAnimation(t *testing.T) {
	app = &appState{}
	loadSlots()
	for _, slots := range [][]*wheelSlot{app.pingSlots, app.emoteSlots} {
		for _, slot := range slots {
			if slot.popIcon.w <= slot.icon.w {
				t.Fatal("pop reuses small wheel asset")
			}
			for _, im := range []*premultImg{slot.icon, slot.popIcon} {
				ink := 0
				for y := 0; y < im.h; y++ {
					for x := 0; x < im.w; x++ {
						a := im.pix[(y*im.w+x)*4+3]
						if a > 0 {
							ink++
						}
					}
				}
				if ink < im.w*im.h/12 {
					t.Fatal("empty or undersized asset")
				}
			}
		}
	}
	s := softwareSurface(wheelSize)
	var icons []*premultImg
	var colors [][3]uint8
	for _, slot := range app.pingSlots {
		icons = append(icons, slot.icon)
		colors = append(colors, [3]uint8{slot.r, slot.g, slot.b})
	}
	for active := -1; active < 8; active++ {
		angle := (float64(active)*45 - 90) * math.Pi / 180
		dx, dy := 100*math.Cos(angle), 100*math.Sin(angle)
		if active >= 0 && sectorFromDelta(dx, dy) != active {
			t.Fatalf("sector %d disagrees with drawing", active)
		}
		s.drawWheel(icons, app.pingCenter.icon, active, dx, dy, colors, 200, 170, 110)
		assertPremult(t, s)
		if active == 0 {
			preview(t, "ping-wheel-danger", s)
		}
		if active == 3 {
			preview(t, "ping-wheel-all-in", s)
		}
		if active == -1 {
			preview(t, "ping-wheel-idle", s)
		}
	}
	if sectorFromDelta(0, 0) != -1 || sectorFromDelta(0, hubRadius-0.01) != -1 {
		t.Fatal("hub deadzone disagrees with geometry")
	}
	icons = nil
	for _, slot := range app.emoteSlots {
		icons = append(icons, slot.icon)
	}
	s.drawWheel(icons, nil, 1, 80, -80, colors, 10, 200, 185)
	assertPremult(t, s)
	preview(t, "emote-wheel", s)
	pop := softwareSurface(popSize)
	for _, emote := range []bool{false, true} {
		icon := app.pingSlots[0].popIcon
		name := "ping-pop"
		if emote {
			icon = app.emoteSlots[0].popIcon
			name = "emote-pop"
		}
		for _, p := range []float64{0, 0.025, 0.09, 0.18, 0.36, 0.65, 0.90, 1} {
			pop.drawPop(icon, p, 235, 80, 80, emote)
			assertPremult(t, pop)
			if p == 0.18 {
				preview(t, name+"-bounce", pop)
			}
			if p == 0.36 {
				preview(t, name+"-hold", pop)
			}
			for x := 0; x < popSize; x++ {
				for _, i := range []int{x * 4, ((popSize-1)*popSize + x) * 4, (x * popSize) * 4, (x*popSize + popSize - 1) * 4} {
					if pop.pixels[i+3] != 0 {
						t.Fatalf("%s clips at p=%f", name, p)
					}
				}
			}
			if p == 1 {
				for _, v := range pop.pixels {
					if v != 0 {
						t.Fatal("pop does not finish fully transparent")
					}
				}
			}
		}
	}
	allIn := app.pingSlots[3]
	if allIn.popSecond == nil {
		t.Fatal("All In is missing its second sword layer")
	}
	for _, p := range []float64{0.025, 0.069, 0.12, 0.5, 1} {
		pop.drawPop(allIn.popIcon, p, allIn.r, allIn.g, allIn.b, false, allIn.popSecond)
		assertPremult(t, pop)
		if p == 0.12 {
			preview(t, "all-in-pop", pop)
		}
	}
}

func TestNativeTray(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof(NOTIFYICONDATAW{}) != 976 {
		t.Fatal("invalid NOTIFYICONDATAW ABI")
	}
	app = &appState{enabled: true}
	icon := createTrayIcon()
	if icon == 0 {
		t.Fatal("CreateIconIndirect failed")
	}
	defer pDestroyIcon.Call(icon)
	hInst, _, _ := pGetModuleHandleW.Call(0)
	name, _ := syscall.UTF16PtrFromString("lolPingLiteTrayTest")
	wc := WNDCLASSEXW{CbSize: uint32(unsafe.Sizeof(WNDCLASSEXW{})), LpfnWndProc: syscall.NewCallback(wndProc), HInstance: hInst, LpszClassName: name}
	if r, _, _ := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		t.Fatal("test class registration failed")
	}
	hwnd, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, 0, 0, hInst, 0)
	if hwnd == 0 {
		t.Fatal("test tray owner failed")
	}
	defer pDestroyWindow.Call(hwnd)
	// Exercise the real GDI -> UpdateLayeredWindow path as well as software rendering.
	overlay, _, _ := pCreateWindowExW.Call(WS_EX_LAYERED|WS_EX_TOOLWINDOW|WS_EX_NOACTIVATE|WS_EX_TRANSPARENT, uintptr(unsafe.Pointer(name)), 0, WS_POPUP, 0, 0, popSize, popSize, 0, 0, hInst, 0)
	if overlay == 0 {
		t.Fatal("layered window creation failed")
	}
	defer pDestroyWindow.Call(overlay)
	surface := newLayeredSurface(popSize, popSize)
	defer surface.destroy()
	surface.drawPop(decodeAndResizePNG(mustAsset("assets/textures/pingwheel_basicpingrender.png"), 128, 128), 0.09, 46, 139, 255, false)
	if !surface.present(overlay, 0, 0) {
		t.Fatal("UpdateLayeredWindow failed")
	}
	t.Log("CreateIconIndirect and native layered presentation succeeded")
	app.trayHwnd = hwnd
	app.nid = NOTIFYICONDATAW{CbSize: uint32(unsafe.Sizeof(NOTIFYICONDATAW{})), Hwnd: hwnd, UID: 1, UFlags: NIF_MESSAGE | NIF_ICON | NIF_TIP | NIF_SHOWTIP, UCallbackMessage: wmTrayIcon, HIcon: icon}
	defer pShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&app.nid)))
	addTrayIcon()
	if !app.trayAdded {
		r, _, err := pShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&app.nid)))
		t.Logf("Shell_NotifyIconW NIM_ADD failed: return=%d error=%v hwnd=%x icon=%x", r, err, hwnd, icon)
		// Compare with the stock Windows icon and minimal flags to distinguish
		// our generated icon from a session that cannot communicate with Explorer.
		stock, _, _ := pLoadIconW.Call(0, IDI_APPLICATION)
		app.nid.HIcon = stock
		app.nid.UFlags = NIF_ICON
		r, _, err = pShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&app.nid)))
		t.Logf("stock icon minimal registration: return=%d error=%v", r, err)
		if r == 0 {
			t.Skip("This process cannot register even a stock Windows icon with Explorer")
		}
		t.Fatal("generated icon registration failed while stock icon succeeded")
	}
	updateTrayTooltip()
	if !app.trayAdded || app.nid.SzTip[0] == 0 {
		t.Fatal("tooltip update failed")
	}
	app.enabled = false
	updateTrayTooltip()
	if !app.trayAdded {
		t.Fatal("paused tooltip failed")
	}
	// Exercise re-registration after the old taskbar registration disappears.
	pShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&app.nid)))
	app.trayAdded = false
	addTrayIcon()
	if !app.trayAdded {
		t.Fatal("tray recovery failed")
	}
}
