//go:build windows

package main

import (
	"encoding/json"
	"os"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"
	"unsafe"
)

// --smoke-test uses a separate mutex and the same window/message/render/audio
// paths, so it can be verified without terminating a user's running instance.
var smokeMode = len(os.Args) > 1 && os.Args[1] == "--smoke-test"

const smokeTimerID = 5

type memorySample struct {
	Phase                   string
	WorkingSetMB, PrivateMB float64
	GDIObjects              uint32
}

var smoke struct {
	start       time.Time
	step        int
	samples     []memorySample
	cancelled   bool
	audioVoices int
}

func sampleMemory(phase string) {
	type counters struct {
		Size, Faults                                                                                          uint32
		PeakWorkingSet, WorkingSet, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile, Private uintptr
	}
	var c counters
	c.Size = uint32(unsafe.Sizeof(c))
	process, _, _ := kernel32.NewProc("GetCurrentProcess").Call()
	psapi := syscall.NewLazyDLL("psapi.dll")
	psapi.NewProc("GetProcessMemoryInfo").Call(process, uintptr(unsafe.Pointer(&c)), uintptr(c.Size))
	gdi, _, _ := user32.NewProc("GetGuiResources").Call(process, 0)
	smoke.samples = append(smoke.samples, memorySample{phase, float64(c.WorkingSet) / (1 << 20), float64(c.Private) / (1 << 20), uint32(gdi)})
}
func smokeTick() {
	elapsed := time.Since(smoke.start).Seconds()
	switch smoke.step {
	case 0:
		sampleMemory("idle")
		app.anchorX = 500
		app.anchorY = 400
		app.curX = 500
		app.curY = 400
		app.dragging = true
		app.emoteMode = false
		wndProc(app.trayHwnd, wmWheelEvt, 1, 0)
		smoke.step++
	case 1:
		if elapsed < .4 {
			return
		}
		app.activeIdx = 0
		app.curY = 250
		renderWheelNow()
		sampleMemory("wheel")
		smoke.step++
	case 2:
		if elapsed < .8 {
			return
		}
		point := &MSLLHOOKSTRUCT{Pt: POINT{500, 250}}
		mouseHookProc(0, WM_RBUTTONDOWN, point)
		mouseHookProc(0, WM_RBUTTONUP, point)
		mouseHookProc(0, WM_LBUTTONUP, point)
		smoke.cancelled = !app.dragging && len(app.effects) == 0
		smoke.step++
	case 3:
		if elapsed < 1.2 {
			return
		}
		app.activeIdx = 3
		triggerPop()
		smoke.audioVoices = len(voices)
		smoke.step++
	case 4:
		if elapsed < 1.6 {
			return
		}
		sampleMemory("all-in")
		smoke.step++
	default:
		i := smoke.step - 5
		if i < 8 {
			if elapsed < 2+float64(i)*3.4 {
				return
			}
			app.emoteMode = true
			app.activeIdx = i
			triggerPop()
			sampleMemory("emote-" + app.emoteSlots[i].label)
			smoke.step++
			return
		}
		if elapsed < 30 {
			return
		}
		runtime.GC()
		debug.FreeOSMemory()
		sampleMemory("settled")
		report := struct {
			Samples          []memorySample
			RightClickCancel bool
			AudioVoices      int
			RemainingEffects int
		}{smoke.samples, smoke.cancelled, smoke.audioVoices, len(app.effects)}
		data, _ := json.MarshalIndent(report, "", "  ")
		os.MkdirAll(".tmp", 0755)
		os.WriteFile(".tmp/smoke.json", data, 0644)
		pPostQuitMessage.Call(0)
	}
}
