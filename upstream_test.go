//go:build windows

package main

import (
	"math"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

func TestUpstreamGeometry(t *testing.T) {
	if wheelSize != 600 || hubRadius != 86 || wheelOuter != 188 || wheelExt != 288 || wheelIconR != 137 {
		t.Fatal("upstream geometry changed")
	}
	for i := 0; i < 8; i++ {
		a := (float64(i)*45 - 90) * math.Pi / 180
		for _, radius := range []float64{86, 188, 288, 900} {
			if got := sectorFromDelta(radius*math.Cos(a), radius*math.Sin(a)); got != i {
				t.Fatalf("radius %g direction %d got %d", radius, i, got)
			}
		}
	}
	if sectorFromDelta(85.999, 0) != -1 {
		t.Fatal("dead zone must be exactly 86")
	}
	s := softwareSurface(600)
	s.buildWheelBase()
	// Cardinal flares exist outside the metal ring; diagonal corners are empty.
	alpha := s.pixels[(50*600+300)*4+3]
	if math.Abs(float64(alpha)-255*.55*.38) > 2 {
		t.Fatalf("flare alpha=%d", alpha)
	}
	if s.pixels[(100*600+100)*4+3] != 0 {
		t.Fatal("diagonal flare is not upstream geometry")
	}
	d := math.Hypot(100.5, .5)
	fraction := (d - 86) / 102
	px := s.pixels[(300*600+400)*4:][:4]
	expected := []float64{lerp(49, 84, fraction) * .92, lerp(45, 78, fraction) * .92, lerp(42, 74, fraction) * .92, 255 * .92}
	for i, want := range expected {
		if math.Abs(float64(px[i])-want) > 2 {
			t.Fatalf("metal channel %d got %d want %g", i, px[i], want)
		}
	}
}

func TestUpstreamTimelines(t *testing.T) {
	if math.Abs(cssEase(.5, 0, 0, .58, 1)-.684643) > 1e-5 {
		t.Fatal("CSS easing must solve the bezier x coordinate")
	}
	app = &appState{}
	loadSlots()
	expected := []string{"SRP_12", "SRP_9", "OnMyWay", "SRP_4", "ComeHere", "SRP_7", "MIA", "AreaIsWarded"}
	for i, slot := range app.pingSlots {
		if slot.sound != "assets/sounds/"+expected[i]+".wav" {
			t.Fatalf("slice %d sound mismatch", i)
		}
	}
	a := openEmote(app.emoteSlots[6].frames)
	if a == nil {
		t.Fatal("bundled animation missing")
	}
	defer a.close()
	if a.frames[len(a.frames)-1].endMS != 3000 {
		t.Fatal("bundled emote must preserve the 3s upstream timeline")
	}
	first := append([]byte(nil), a.at(0).pix...)
	later := a.at(.5).pix
	changed := false
	for i, v := range first {
		if later[i] != v {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("emote still image replaced its animation")
	}
	s := softwareSurface(popSize)
	s.drawPop(app.pingSlots[0].popIcon, 1.5/3.2, 255, 59, 85, false)
	// Third ring starts at 1.4s, long after the first two have disappeared.
	if s.pixels[((popSize/2)*popSize+popSize/2+40)*4+3] == 0 {
		t.Fatal("missing delayed third ring")
	}
	s.clear()
	s.drawFieldGlow(40, 40, 180, 100, .9, 30, 160, 230)
	for _, v := range s.pixels {
		if v != 0 {
			t.Fatal("field outline survives its 900ms deadline")
		}
	}
}

func TestRightClickCancel(t *testing.T) {
	app = &appState{enabled: true, dragging: true, activeIdx: 0, wheelTransition: time.Now()}
	point := &MSLLHOOKSTRUCT{Pt: POINT{100, 100}}
	if mouseHookProc(0, WM_RBUTTONDOWN, point) != 1 || app.dragging || !app.cancelled {
		t.Fatal("right click did not cancel and consume input")
	}
	if mouseHookProc(0, WM_RBUTTONUP, point) != 1 {
		t.Fatal("cancel leaked right-button release")
	}
	if mouseHookProc(0, WM_LBUTTONUP, point) != 1 || app.cancelled || len(app.effects) != 0 {
		t.Fatal("left release after cancel triggered a ping")
	}
	app.dragging = true
	mouseHookProc(0, WM_RBUTTONDOWN, point)
	mouseHookProc(0, WM_LBUTTONUP, point)
	if mouseHookProc(0, WM_RBUTTONUP, point) != 1 || app.cancelRight {
		t.Fatal("left-first button release leaked cancellation's right-button release")
	}
}

func TestOverlayDestroyKeepsMessageLoop(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	peek := user32.NewProc("PeekMessageW")
	var msg MSG
	// Native tray tests may leave the tray owner's WM_QUIT in this thread queue.
	for {
		r, _, _ := peek.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1)
		if r == 0 {
			break
		}
	}
	app = &appState{trayHwnd: 12345}
	wndProc(54321, WM_DESTROY, 0, 0)
	if r, _, _ := peek.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1); r != 0 {
		t.Fatal("destroying a finished ping terminated the application's message loop")
	}
	wndProc(app.trayHwnd, WM_DESTROY, 0, 0)
	if r, _, err := peek.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1); r == 0 {
		t.Fatalf("destroying the tray owner did not quit: %v", err)
	}
}
