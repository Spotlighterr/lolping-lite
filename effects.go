//go:build windows

package main

import (
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const maxConcurrentPings = 8

type popSlot struct {
	hwnd    uintptr
	surface *layeredSurface
	inUse   bool
	shared  bool
}

type popEffect struct {
	poolSlot  *popSlot
	hwnd      uintptr
	surface   *layeredSurface
	slot      *wheelSlot
	emote     bool
	animation *emoteAnimation
	start     time.Time
	x, y      int32
}

var popPool []*popSlot

func prewarmShadows() {
	if app == nil {
		return
	}
	warm := func(s *wheelSlot) {
		if s == nil {
			return
		}
		if s.popIcon != nil {
			getShadow(s.popIcon, s.r, s.g, s.b)
		}
		if s.popSecond != nil {
			getShadow(s.popSecond, s.r, s.g, s.b)
		}
	}
	warm(app.pingCenter)
	for _, s := range app.pingSlots {
		warm(s)
	}
}

func acquirePopSlot() *popSlot {
	for _, ps := range popPool {
		if !ps.inUse {
			ps.inUse = true
			return ps
		}
	}
	if len(popPool) == 0 && app.popHwnd != 0 && app.popSurf != nil {
		ps := &popSlot{hwnd: app.popHwnd, surface: app.popSurf, inUse: true, shared: true}
		popPool = append(popPool, ps)
		return ps
	}
	cls, _ := syscall.UTF16PtrFromString("lolPingLiteClass")
	hwnd, _, _ := pCreateWindowExW.Call(
		WS_EX_LAYERED|WS_EX_TOPMOST|WS_EX_TOOLWINDOW|WS_EX_NOACTIVATE|WS_EX_TRANSPARENT,
		uintptr(unsafe.Pointer(cls)), 0, WS_POPUP, 0, 0, popSize, popSize, 0, 0, app.hInstance, 0,
	)
	if hwnd == 0 {
		return nil
	}
	ps := &popSlot{hwnd: hwnd, surface: newLayeredSurface(popSize, popSize), inUse: true}
	popPool = append(popPool, ps)
	return ps
}

func (e *popEffect) destroy() {
	if e.animation != nil {
		e.animation.close()
		e.animation = nil
	}
	pShowWindow.Call(e.hwnd, SW_HIDE)
	if e.poolSlot != nil {
		e.poolSlot.inUse = false
		e.poolSlot = nil
	}
}

func startEffect(slot *wheelSlot, emote bool) {
	pingCount := 0
	live := app.effects[:0]
	for _, e := range app.effects {
		if e.emote && emote {
			e.destroy()
		} else {
			live = append(live, e)
			if !e.emote {
				pingCount++
			}
		}
	}
	app.effects = live
	if !emote && pingCount >= maxConcurrentPings {
		for i, e := range app.effects {
			if !e.emote {
				e.destroy()
				app.effects = append(app.effects[:i], app.effects[i+1:]...)
				break
			}
		}
	}
	ps := acquirePopSlot()
	if ps == nil {
		return
	}
	e := &popEffect{
		poolSlot: ps,
		hwnd:     ps.hwnd,
		surface:  ps.surface,
		slot:     slot,
		emote:    emote,
		start:    time.Now(),
		x:        app.anchorX - popSize/2,
		y:        app.anchorY - popSize/2,
	}
	if emote {
		e.animation = openEmote(slot.frames)
	}
	app.effects = append(app.effects, e)
	e.render(0)
	pSetWindowPos.Call(e.hwnd, HWND_TOPMOST, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE|SWP_SHOWWINDOW)
	wakeVsync()
}

func (e *popEffect) drawOnly(elapsed float64) {
	if e.emote && e.animation != nil {
		e.surface.clear()
		e.surface.blitScaled(e.animation.at(elapsed), popSize/2, popSize/2, 187.5/188, 1)
	} else {
		duration := 3.2
		if e.emote {
			duration = 3
		}
		e.surface.drawPop(e.slot.popIcon, elapsed/duration, e.slot.r, e.slot.g, e.slot.b, e.emote, e.slot.popSecond)
	}
}

func (e *popEffect) render(elapsed float64) {
	e.drawOnly(elapsed)
	e.surface.present(e.hwnd, e.x, e.y)
}

func tickEffects() {
	now := time.Now()
	live := app.effects[:0]
	var elapsedList [maxConcurrentPings + 2]float64
	for _, e := range app.effects {
		elapsed := now.Sub(e.start).Seconds()
		duration := 3.2
		if e.emote {
			duration = 3
		}
		if elapsed >= duration+.15 {
			e.destroy()
		} else {
			elapsedList[len(live)] = elapsed
			live = append(live, e)
		}
	}
	for i := len(live); i < len(app.effects); i++ {
		app.effects[i] = nil
	}
	app.effects = live
	if len(live) == 0 {
		pKillTimer.Call(app.trayHwnd, popTimerID)
		return
	}
	if len(live) == 1 {
		live[0].render(elapsedList[0])
		return
	}
	var wg sync.WaitGroup
	wg.Add(len(live))
	for i, e := range live {
		el := elapsedList[i]
		go func(eff *popEffect, t float64) {
			eff.drawOnly(t)
			wg.Done()
		}(e, el)
	}
	wg.Wait()
	for _, e := range live {
		e.surface.present(e.hwnd, e.x, e.y)
	}
}

func stopEffects() {
	for _, e := range app.effects {
		e.destroy()
	}
	app.effects = nil
	for _, ps := range popPool {
		if !ps.shared {
			pDestroyWindow.Call(ps.hwnd)
			ps.surface.destroy()
		}
	}
	popPool = nil
}
