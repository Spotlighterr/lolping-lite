//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework CoreGraphics -framework CoreText -framework AVFoundation -framework ApplicationServices
#include "darwin_cocoa.h"
#include <stdlib.h>
*/
import "C"

import (
	"bytes"
	"compress/zlib"
	"embed"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
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
	wheelSize          = 600
	popSize            = 320
	hubRadius          = 86.0
	wheelOuter         = 188.0
	wheelExt           = 288.0
	wheelIconR         = 137.0
	maxConcurrentPings = 8
)

type premultImg struct {
	w, h int
	pix  []byte // RGBA premultiplied
}

type wedgePix struct {
	idx        uint32
	r, g, b, a uint8
}

type shadowKey struct {
	im      *premultImg
	r, g, b uint8
}

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

type frameEntry struct {
	endMS  uint32
	offset int64
	size   int64
}

type emoteAnimation struct {
	file    io.ReadSeekCloser
	frames  []frameEntry
	image   *premultImg
	current int
}

type macSurface struct {
	id           C.int
	width        int
	height       int
	pixels       []byte // BGRA premultiplied
	wheelBase    []byte
	sectorWedges [8][]wedgePix
	textCache    map[string]*premultImg
}

type macPopSlot struct {
	surf  *macSurface
	inUse bool
}

type macPopEffect struct {
	poolSlot  *macPopSlot
	slot      *wheelSlot
	emote     bool
	animation *emoteAnimation
	start     time.Time
	x, y      int
}

type macAppState struct {
	enabled         bool
	dragging        bool
	emoteMode       bool
	wheelClosing    bool
	wheelDirty      bool
	wheelOpacity    float64
	wheelTransition time.Time
	anchorX         int
	anchorY         int
	activeIdx       int
	hoverIdx        int
	hoverStart      time.Time
	hover           [8]float64
	hoverFrom       [8]float64
	wedge           [8]float64
	wedgeFrom       [8]float64

	wheelSurf  *macSurface
	pingCenter *wheelSlot
	emoteCenter *wheelSlot
	pingSlots  []*wheelSlot
	emoteSlots []*wheelSlot

	popPool []*macPopSlot
	effects []*macPopEffect
}

var (
	mapp          *macAppState
	globalShadows = make(map[shadowKey]*premultImg)
)

func mustAsset(path string) []byte {
	b, err := embeddedAssets.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return b
}

func clampByte(v float64) byte { return byte(math.Max(0, math.Min(255, v)) + 0.5) }
func unit(v float64) float64   { return math.Max(0, math.Min(1, v)) }
func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func cssEase(t, x1, y1, x2, y2 float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	lo, hi := 0.0, 1.0
	for i := 0; i < 22; i++ {
		m := (lo + hi) / 2
		v := 3*(1-m)*(1-m)*m*x1 + 3*(1-m)*m*m*x2 + m*m*m
		if v < t {
			lo = m
		} else {
			hi = m
		}
	}
	m := (lo + hi) / 2
	return 3*(1-m)*(1-m)*m*y1 + 3*(1-m)*m*m*y2 + m*m*m
}
func easeOut(t float64) float64          { return cssEase(t, 0, 0, .58, 1) }
func easeIn(t float64) float64           { return cssEase(t, .42, 0, 1, 1) }
func easeInOut(t float64) float64        { return cssEase(t, .42, 0, .58, 1) }
func circleEdge(d, r float64) float64    { return unit(r + .5 - d) }
func strokeEdge(d, r, w float64) float64 { return unit(w/2 + .5 - math.Abs(d-r)) }
func mixColor(a, b [3]uint8, t float64) [3]uint8 {
	return [3]uint8{
		clampByte(lerp(float64(a[0]), float64(b[0]), t)),
		clampByte(lerp(float64(a[1]), float64(b[1]), t)),
		clampByte(lerp(float64(a[2]), float64(b[2]), t)),
	}
}

func newMacSurface(w, h int) *macSurface {
	return &macSurface{
		id:     C.mac_create_overlay(C.int(w), C.int(h)),
		width:  w,
		height: h,
		pixels: make([]byte, w*h*4),
	}
}

func (s *macSurface) clear() { clear(s.pixels) }

func (s *macSurface) present(x, y int, opacity float64) {
	if len(s.pixels) == 0 {
		return
	}
	C.mac_update_overlay(s.id, unsafe.Pointer(&s.pixels[0]), C.int(s.width), C.int(s.height), C.int(x), C.int(y), C.float(opacity))
}

func (s *macSurface) hide() { C.mac_hide_overlay(s.id) }

func (s *macSurface) blendPixel(x, y int, r, g, b, a uint8) {
	if x < 0 || y < 0 || x >= s.width || y >= s.height || a == 0 {
		return
	}
	i := (y*s.width + x) * 4
	inv := 255 - uint32(a)
	s.pixels[i+0] = byte(uint32(b) + uint32(s.pixels[i+0])*inv/255)
	s.pixels[i+1] = byte(uint32(g) + uint32(s.pixels[i+1])*inv/255)
	s.pixels[i+2] = byte(uint32(r) + uint32(s.pixels[i+2])*inv/255)
	s.pixels[i+3] = byte(uint32(a) + uint32(s.pixels[i+3])*inv/255)
}

func (s *macSurface) paint(x, y int, r, g, b uint8, opacity float64) {
	a := clampByte(255 * unit(opacity))
	f := float64(a) / 255
	s.blendPixel(x, y, clampByte(float64(r)*f), clampByte(float64(g)*f), clampByte(float64(b)*f), a)
}

func decodePNG(data []byte, tw, th int, trim bool) *premultImg {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}
	bounds := src.Bounds()
	ink := image.Rectangle{}
	if trim {
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				_, _, _, a := src.At(x, y).RGBA()
				if a > 512 {
					ink = ink.Union(image.Rect(x, y, x+1, y+1))
				}
			}
		}
	} else {
		ink = bounds
	}
	out := &premultImg{tw, th, make([]byte, tw*th*4)}
	if ink.Empty() {
		return out
	}
	scale := math.Min(float64(tw-4)/float64(ink.Dx()), float64(th-4)/float64(ink.Dy()))
	dw := max(1, int(math.Round(float64(ink.Dx())*scale)))
	dh := max(1, int(math.Round(float64(ink.Dy())*scale)))
	ox, oy := (tw-dw)/2, (th-dh)/2
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			x0 := float64(ink.Min.X) + float64(x)*float64(ink.Dx())/float64(dw)
			x1 := float64(ink.Min.X) + float64(x+1)*float64(ink.Dx())/float64(dw)
			y0 := float64(ink.Min.Y) + float64(y)*float64(ink.Dy())/float64(dh)
			y1 := float64(ink.Min.Y) + float64(y+1)*float64(ink.Dy())/float64(dh)
			var sum [4]float64
			for sy := int(math.Floor(y0)); sy < int(math.Ceil(y1)); sy++ {
				for sx := int(math.Floor(x0)); sx < int(math.Ceil(x1)); sx++ {
					weight := (math.Min(x1, float64(sx+1)) - math.Max(x0, float64(sx))) * (math.Min(y1, float64(sy+1)) - math.Max(y0, float64(sy)))
					r, g, b, a := src.At(sx, sy).RGBA()
					v := [4]uint32{r, g, b, a}
					for k := range sum {
						sum[k] += float64(v[k]) * weight / 257
					}
				}
			}
			i := ((y+oy)*tw + x + ox) * 4
			for k := range sum {
				out.pix[i+k] = clampByte(sum[k] / ((x1 - x0) * (y1 - y0)))
			}
		}
	}
	return out
}

func tintPing(im *premultImg) *premultImg {
	out := &premultImg{im.w, im.h, make([]byte, len(im.pix))}
	for i := 0; i < len(im.pix); i += 4 {
		r, g, b := float64(im.pix[i]), float64(im.pix[i+1]), float64(im.pix[i+2])
		a := float64(im.pix[i+3])
		gray := .2126*r + .7152*g + .0722*b
		rr := lerp(gray, .393*gray+.769*gray+.189*gray, .75)
		gg := lerp(gray, .349*gray+.686*gray+.168*gray, .75)
		bb := lerp(gray, .272*gray+.534*gray+.131*gray, .75)
		r, g, b = math.Min(a, rr), math.Min(a, gg), math.Min(a, bb)
		sat := 1.3
		rr = (.213+.787*sat)*r + (.715-.715*sat)*g + (.072-.072*sat)*b
		gg = (.213-.213*sat)*r + (.715+.285*sat)*g + (.072-.072*sat)*b
		bb = (.213-.213*sat)*r + (.715-.715*sat)*g + (.072+.928*sat)*b
		out.pix[i+0] = min(im.pix[i+3], clampByte(math.Min(a, math.Max(0, rr))*1.15))
		out.pix[i+1] = min(im.pix[i+3], clampByte(math.Min(a, math.Max(0, gg))*1.15))
		out.pix[i+2] = min(im.pix[i+3], clampByte(math.Min(a, math.Max(0, bb))*1.15))
		out.pix[i+3] = im.pix[i+3]
	}
	return out
}

func roundIcon(im *premultImg) *premultImg {
	for y := 0; y < im.h; y++ {
		for x := 0; x < im.w; x++ {
			i := (y*im.w + x) * 4
			cov := circleEdge(math.Hypot(float64(x)+.5-float64(im.w)/2, float64(y)+.5-float64(im.h)/2), float64(im.w)/2)
			for k := 0; k < 4; k++ {
				im.pix[i+k] = clampByte(float64(im.pix[i+k]) * cov)
			}
		}
	}
	return im
}

func shadowImage(im *premultImg, r, g, b uint8, sigma float64) *premultImg {
	pad := int(math.Ceil(3 * sigma))
	w, h := im.w+2*pad, im.h+2*pad
	a, tmp := make([]float64, w*h), make([]float64, w*h)
	for y := 0; y < im.h; y++ {
		for x := 0; x < im.w; x++ {
			a[(y+pad)*w+x+pad] = float64(im.pix[(y*im.w+x)*4+3]) / 255
		}
	}
	radius := max(1, int(sigma))
	diameter := float64(2*radius + 1)
	for pass := 0; pass < 3; pass++ {
		for y := 0; y < h; y++ {
			sum := 0.0
			for x := -radius; x < w; x++ {
				if x+radius < w {
					sum += a[y*w+x+radius]
				}
				if x-radius-1 >= 0 {
					sum -= a[y*w+x-radius-1]
				}
				if x >= 0 {
					tmp[y*w+x] = sum / diameter
				}
			}
		}
		for x := 0; x < w; x++ {
			sum := 0.0
			for y := -radius; y < h; y++ {
				if y+radius < h {
					sum += tmp[(y+radius)*w+x]
				}
				if y-radius-1 >= 0 {
					sum -= tmp[(y-radius-1)*w+x]
				}
				if y >= 0 {
					a[y*w+x] = sum / diameter
				}
			}
		}
	}
	out := &premultImg{w, h, make([]byte, w*h*4)}
	for i, v := range a {
		alpha := clampByte(255 * v)
		out.pix[i*4+0] = byte(uint32(r) * uint32(alpha) / 255)
		out.pix[i*4+1] = byte(uint32(g) * uint32(alpha) / 255)
		out.pix[i*4+2] = byte(uint32(b) * uint32(alpha) / 255)
		out.pix[i*4+3] = alpha
	}
	return out
}

func getShadow(im *premultImg, r, g, b uint8) *premultImg {
	key := shadowKey{im, r, g, b}
	if sh := globalShadows[key]; sh != nil {
		return sh
	}
	sh := shadowImage(im, r, g, b, 10)
	ox, oy := (sh.w-im.w)/2, (sh.h-im.h)/2
	for y := 0; y < im.h; y++ {
		for x := 0; x < im.w; x++ {
			si := (y*im.w + x) * 4
			sa := uint32(im.pix[si+3])
			if sa == 0 {
				continue
			}
			di := ((oy+y)*sh.w + ox + x) * 4
			inv := 255 - sa
			sh.pix[di+0] = byte(uint32(im.pix[si+0]) + (uint32(sh.pix[di+0])*inv)/255)
			sh.pix[di+1] = byte(uint32(im.pix[si+1]) + (uint32(sh.pix[di+1])*inv)/255)
			sh.pix[di+2] = byte(uint32(im.pix[si+2]) + (uint32(sh.pix[di+2])*inv)/255)
			sh.pix[di+3] = byte(sa + (uint32(sh.pix[di+3])*inv)/255)
		}
	}
	globalShadows[key] = sh
	return sh
}

func (s *macSurface) blitPremult(im *premultImg, cx, cy int, alpha float64) {
	if im == nil || alpha <= 0 {
		return
	}
	au := uint32(clampByte(255 * unit(alpha)))
	if au == 0 {
		return
	}
	x0, y0 := cx-im.w/2, cy-im.h/2
	sx0, sy0 := 0, 0
	if x0 < 0 {
		sx0, x0 = -x0, 0
	}
	if y0 < 0 {
		sy0, y0 = -y0, 0
	}
	w, h := im.w-sx0, im.h-sy0
	if x0+w > s.width {
		w = s.width - x0
	}
	if y0+h > s.height {
		h = s.height - y0
	}
	if w <= 0 || h <= 0 {
		return
	}
	src, dst := im.pix, s.pixels
	sw, dw := im.w, s.width
	for y := 0; y < h; y++ {
		si := ((sy0+y)*sw + sx0) * 4
		di := ((y0+y)*dw + x0) * 4
		for x := 0; x < w; x++ {
			sa := (uint32(src[si+3]) * au) / 255
			if sa != 0 {
				inv := 255 - sa
				sr := (uint32(src[si+0]) * au) / 255
				sg := (uint32(src[si+1]) * au) / 255
				sb := (uint32(src[si+2]) * au) / 255
				dst[di+0] = byte(sb + (uint32(dst[di+0])*inv)/255)
				dst[di+1] = byte(sg + (uint32(dst[di+1])*inv)/255)
				dst[di+2] = byte(sr + (uint32(dst[di+2])*inv)/255)
				dst[di+3] = byte(sa + (uint32(dst[di+3])*inv)/255)
			}
			si += 4
			di += 4
		}
	}
}

func (s *macSurface) blitScaled(im *premultImg, cx, cy, scale, alpha float64) {
	if im == nil || scale <= 0 || alpha <= 0 {
		return
	}
	if math.Abs(scale-1.0) < 0.005 {
		s.blitPremult(im, int(math.Round(cx)), int(math.Round(cy)), alpha)
		return
	}
	au := uint32(clampByte(255 * unit(alpha)))
	if au == 0 {
		return
	}
	w, h := float64(im.w)*scale, float64(im.h)*scale
	yStart := max(0, int(math.Floor(cy-h/2)))
	yEnd := min(s.height, int(math.Ceil(cy+h/2)))
	xStart := max(0, int(math.Floor(cx-w/2)))
	xEnd := min(s.width, int(math.Ceil(cx+w/2)))
	if xStart >= xEnd || yStart >= yEnd {
		return
	}
	step := int32(math.Round(65536.0 / scale))
	sxBase := int32(math.Round(((float64(xStart)+0.5-(cx-w/2))/scale - 0.5) * 65536.0))
	syCur := int32(math.Round(((float64(yStart)+0.5-(cy-h/2))/scale - 0.5) * 65536.0))
	src, dst := im.pix, s.pixels
	sw, sh, dw := im.w, im.h, s.width
	for y := yStart; y < yEnd; y++ {
		iy := int(syCur >> 16)
		fy := uint32((syCur >> 8) & 0xFF)
		ify := 256 - fy
		syCur += step
		if iy < -1 || iy >= sh {
			continue
		}
		y0Ok, y1Ok := iy >= 0, iy+1 < sh
		row0, row1 := iy*sw, (iy+1)*sw
		di := (y*dw + xStart) * 4
		sxCur := sxBase
		for x := xStart; x < xEnd; x++ {
			ix := int(sxCur >> 16)
			fx := uint32((sxCur >> 8) & 0xFF)
			sxCur += step
			if ix >= -1 && ix < sw {
				ifx := 256 - fx
				var r, g, b, a uint32
				if y0Ok {
					if ix >= 0 {
						o, w00 := (row0+ix)*4, ifx*ify
						r += uint32(src[o+0]) * w00
						g += uint32(src[o+1]) * w00
						b += uint32(src[o+2]) * w00
						a += uint32(src[o+3]) * w00
					}
					if ix+1 < sw {
						o, w10 := (row0+ix+1)*4, fx*ify
						r += uint32(src[o+0]) * w10
						g += uint32(src[o+1]) * w10
						b += uint32(src[o+2]) * w10
						a += uint32(src[o+3]) * w10
					}
				}
				if y1Ok {
					if ix >= 0 {
						o, w01 := (row1+ix)*4, ifx*fy
						r += uint32(src[o+0]) * w01
						g += uint32(src[o+1]) * w01
						b += uint32(src[o+2]) * w01
						a += uint32(src[o+3]) * w01
					}
					if ix+1 < sw {
						o, w11 := (row1+ix+1)*4, fx*fy
						r += uint32(src[o+0]) * w11
						g += uint32(src[o+1]) * w11
						b += uint32(src[o+2]) * w11
						a += uint32(src[o+3]) * w11
					}
				}
				sa := ((a >> 16) * au) / 255
				if sa != 0 {
					inv := 255 - sa
					sr := ((r >> 16) * au) / 255
					sg := ((g >> 16) * au) / 255
					sb := ((b >> 16) * au) / 255
					dst[di+0] = byte(sb + (uint32(dst[di+0])*inv)/255)
					dst[di+1] = byte(sg + (uint32(dst[di+1])*inv)/255)
					dst[di+2] = byte(sr + (uint32(dst[di+2])*inv)/255)
					dst[di+3] = byte(sa + (uint32(dst[di+3])*inv)/255)
				}
			}
			di += 4
		}
	}
}

func (s *macSurface) drawText(value string, cx, cy, fontSize float64, c [3]uint8) {
	key := fmt.Sprintf("%s/%.1f/%v", value, fontSize, c)
	if im := s.textCache[key]; im != nil {
		s.blitPremult(im, int(math.Round(cx)), int(math.Round(cy)), 1)
		return
	}
	cs := C.CString(value)
	defer C.free(unsafe.Pointer(cs))
	var w, h C.int
	buf := C.mac_rasterize_text(cs, C.float(fontSize), &w, &h)
	if buf == nil {
		return
	}
	defer C.mac_free(unsafe.Pointer(buf))
	iw, ih := int(w), int(h)
	mask := unsafe.Slice((*byte)(unsafe.Pointer(buf)), iw*ih)
	im := &premultImg{w: iw, h: ih, pix: make([]byte, iw*ih*4)}
	for i, alpha := range mask {
		im.pix[i*4+0] = byte(uint32(c[0]) * uint32(alpha) / 255)
		im.pix[i*4+1] = byte(uint32(c[1]) * uint32(alpha) / 255)
		im.pix[i*4+2] = byte(uint32(c[2]) * uint32(alpha) / 255)
		im.pix[i*4+3] = alpha
	}
	if s.textCache == nil {
		s.textCache = make(map[string]*premultImg)
	}
	s.textCache[key] = im
	s.blitPremult(im, int(math.Round(cx)), int(math.Round(cy)), 1)
}

func (s *macSurface) buildWheelBase() {
	s.clear()
	for k := range s.sectorWedges {
		s.sectorWedges[k] = nil
	}
	scale := float64(s.width) / 600
	cx, cy := float64(s.width)/2, float64(s.height)/2
	for y := 0; y < s.height; y++ {
		for x := 0; x < s.width; x++ {
			dx, dy := (float64(x)+.5-cx)/scale, (float64(y)+.5-cy)/scale
			d := math.Hypot(dx, dy)
			a := math.Mod(math.Atan2(dy, dx)+math.Pi/2+math.Pi/8+2*math.Pi, 2*math.Pi)
			rem := math.Mod(a, math.Pi/4)
			line := math.Sin(math.Min(rem, math.Pi/4-rem)) * d
			sector := min(7, int(a/(math.Pi/4)))
			if sector%2 == 0 && d >= wheelOuter && d < wheelExt {
				s.paint(x, y, 92, 96, 102, .55*unit((wheelExt-d)/(wheelExt-wheelOuter)))
			}
			if d >= hubRadius && d <= wheelOuter+.5/scale {
				c := mixColor([3]uint8{42, 45, 49}, [3]uint8{74, 78, 84}, unit((d-hubRadius)/(wheelOuter-hubRadius)))
				s.paint(x, y, c[0], c[1], c[2], .92*circleEdge(d*scale, wheelOuter*scale))
			}
			if d >= hubRadius && d <= wheelOuter {
				s.paint(x, y, 160, 138, 92, .45*unit(1.2*scale/2+.5-line*scale))
			}
			reach := wheelOuter
			if sector%2 == 0 {
				reach = wheelExt
			}
			if d >= hubRadius && d <= reach {
				c := mixColor([3]uint8{138, 143, 150}, [3]uint8{154, 160, 168}, unit((d-hubRadius)/(wheelOuter-hubRadius)))
				alpha := .55
				if d > wheelOuter {
					alpha *= unit((wheelExt - d) / (wheelExt - wheelOuter))
				}
				if alpha > 0.004 {
					s.sectorWedges[sector] = append(s.sectorWedges[sector], wedgePix{
						idx: uint32((y*s.width + x) * 4),
						r:   c[0], g: c[1], b: c[2], a: clampByte(alpha * 255),
					})
				}
			}
		}
	}
	for y := int(cy - 94*scale); y <= int(cy+94*scale); y++ {
		for x := int(cx - 94*scale); x <= int(cx+94*scale); x++ {
			dx, dy := float64(x)+.5-cx, float64(y)+.5-cy
			d := math.Hypot(dx, dy)
			s.paint(x, y, 26, 26, 26, .6*strokeEdge(d, 90*scale, 6*scale))
			c := mixColor([3]uint8{44, 93, 96}, [3]uint8{17, 41, 44}, unit(math.Hypot(dx, dy+10*scale)/(86*scale)))
			s.paint(x, y, c[0], c[1], c[2], circleEdge(d, 86*scale))
			s.paint(x, y, 200, 170, 110, strokeEdge(d, 86*scale, 3*scale))
			s.paint(x, y, 138, 116, 72, .6*strokeEdge(d, 79*scale, scale))
		}
	}
	s.drawText("BACK", cx+6*scale, cy+47*scale, 9*scale, [3]uint8{108, 122, 124})
	for y := int(cy + 39*scale); y <= int(cy+57*scale); y++ {
		for x := int(cx - 27*scale); x <= int(cx-14*scale); x++ {
			mx, my := (float64(x)+.5-cx)/scale+26, (float64(y)+.5-cy)/scale-40
			qx, qy := math.Abs(mx-5.5), math.Abs(my-8)-2.5
			sd := math.Hypot(qx, math.Max(qy, 0)) - 5.5
			cov := unit(.65*scale + .5 - math.Abs(sd*scale))
			if mx >= 5.5 && my <= 7 && my >= 0 && sd <= 0 {
				cov = 1
			}
			s.paint(x, y, 108, 122, 124, .55*cov)
		}
	}
	s.wheelBase = append([]byte(nil), s.pixels...)
}

func (s *macSurface) drawWheel() {
	if s.wheelBase == nil {
		s.buildWheelBase()
	}
	copy(s.pixels, s.wheelBase)
	scale := float64(s.width) / 600
	cx, cy := float64(s.width)/2, float64(s.height)/2

	for sec := 0; sec < 8; sec++ {
		hot := mapp.wedge[sec]
		if hot <= 0.004 {
			continue
		}
		hotU := uint32(hot * 255)
		pix := s.pixels
		for _, wp := range s.sectorWedges[sec] {
			sa := (uint32(wp.a) * hotU) / 255
			if sa == 0 {
				continue
			}
			inv := 255 - sa
			i := wp.idx
			pix[i+0] = byte((uint32(wp.b)*sa + uint32(pix[i+0])*inv) / 255)
			pix[i+1] = byte((uint32(wp.g)*sa + uint32(pix[i+1])*inv) / 255)
			pix[i+2] = byte((uint32(wp.r)*sa + uint32(pix[i+2])*inv) / 255)
			pix[i+3] = byte(sa + (uint32(pix[i+3])*inv)/255)
		}
	}

	slots := mapp.pingSlots
	center := mapp.pingCenter.icon
	label := "PING"
	if mapp.emoteMode {
		slots = mapp.emoteSlots
		center = mapp.emoteCenter.icon
		label = "EMOTE"
	}
	for i, sl := range slots {
		hot := mapp.hover[i]
		a := (float64(i)*45 - 90) * math.Pi / 180
		ix := cx + wheelIconR*scale*math.Cos(a)
		iy := cy + wheelIconR*scale*math.Sin(a)
		sc := 48 * scale / float64(sl.icon.w) * (1 + .18*hot)
		if !mapp.emoteMode && hot < 0.99 && sl.tinted != nil {
			if hot <= 0.01 {
				s.blitScaled(sl.tinted, ix, iy, sc, 1)
			} else {
				s.blitScaled(sl.tinted, ix, iy, sc, 1-hot)
				s.blitScaled(sl.icon, ix, iy, sc, hot)
			}
		} else {
			s.blitScaled(sl.icon, ix, iy, sc, 1)
		}
	}
	if center != nil {
		s.blitScaled(center, cx, cy-38*scale, 40*scale/float64(center.w), 1)
	}
	if mapp.activeIdx >= 0 && mapp.activeIdx < len(slots) {
		label = slots[mapp.activeIdx].label
	}
	chars := []rune(label)
	if len(chars) > 14 {
		label = string(chars[:13]) + "\u2026"
	}
	s.drawText(label, cx, cy+2*scale, 15*scale, [3]uint8{31, 169, 224})
}

func bobAt(t, delay, period, amplitude float64) float64 {
	if t < delay {
		return 0
	}
	p := math.Mod(t-delay, period) / period
	if p < .5 {
		return -amplitude * easeInOut(p*2)
	}
	return -amplitude * (1 - easeInOut((p-.5)*2))
}

func keyLerp(t float64, times, values []float64, ease func(float64) float64) float64 {
	if t <= times[0] {
		return values[0]
	}
	for i := 1; i < len(times); i++ {
		if t <= times[i] {
			return lerp(values[i-1], values[i], ease((t-times[i-1])/(times[i]-times[i-1])))
		}
	}
	return values[len(values)-1]
}

func (s *macSurface) drawPop(icon *premultImg, p float64, r, g, b uint8, emote bool, second *premultImg) {
	s.clear()
	if p < 0 || p >= 1 {
		return
	}
	cx, cy := float64(s.width)/2, float64(s.height)/2
	if emote {
		t := p * 3
		scale := keyLerp(t, []float64{0, .15, .25}, []float64{0, 1.15, 1}, easeOut)
		fade := 1.0
		if t >= 2.65 {
			q := easeIn((t - 2.65) / .35)
			scale = 1 - .15*q
			fade = 1 - q
		}
		phase := math.Mod(t, 1.4) / 1.4
		bob := keyLerp(phase, []float64{0, .25, .75, 1}, []float64{0, -4, 4, 0}, easeInOut)
		s.blitScaled(getShadow(icon, 255, 255, 255), cx, cy+bob, 150/float64(icon.w)*scale, fade)
		return
	}
	t := p * 3.2
	size := 110.0
	ground := keyLerp(p, []float64{0, .1, .85, 1}, []float64{0, .75, .55, 0}, easeOut)
	for y := int(cy + size*.2); y < int(cy+size*.44); y++ {
		for x := int(cx - size*.42); x < int(cx+size*.42); x++ {
			d := math.Hypot((float64(x)+.5-cx)/(size*.42), (float64(y)+.5-cy-size*.32)/(size*.12))
			s.paint(x, y, r, g, b, ground*unit(1-d))
		}
	}
	var lut [512]uint8
	for _, delay := range []float64{0, .22, 1.4} {
		q := (t - delay) / .65
		if q < 0 || q >= 1 {
			continue
		}
		e := easeOut(q)
		scale := lerp(.25, 1.35, e)
		opacity := .95 * (1 - e)
		radius := (size*1.09/2 - 1.5) * scale
		width := 3 * scale
		sigma := 9 * scale
		extent := radius + 3*sigma
		maxIdx := min(len(lut)-1, int(math.Ceil(extent*2))+1)
		invOuter := 1.0 / (math.Sqrt2 * sigma)
		invInner := 1.0 / (math.Sqrt2 * 6 * scale)
		for i := 0; i <= maxIdx; i++ {
			rad := float64(i) * 0.5
			glow := 0.0
			if rad >= radius+width/2 {
				glow = .5 * math.Erfc((rad-radius-width/2)*invOuter)
			} else if rad <= radius-width/2 {
				glow = .5 * math.Erfc((radius-width/2-rad)*invInner)
			}
			a1 := opacity * glow
			a2 := opacity * strokeEdge(rad, radius, width)
			lut[i] = clampByte((a1 + a2*(1-a1)) * 255)
		}
		extI := int(math.Ceil(extent))
		ext2 := extent * extent
		cxi, cyi := s.width/2, s.height/2
		pix, sw, sh := s.pixels, s.width, s.height
		ru, gu, bu := uint32(r), uint32(g), uint32(b)
		for dy := 0; dy <= extI; dy++ {
			fy := float64(dy) + 0.5
			fy2 := fy * fy
			yPos, yNeg := cyi+dy, cyi-1-dy
			for dx := 0; dx <= extI; dx++ {
				fx := float64(dx) + 0.5
				d2 := fx*fx + fy2
				if d2 > ext2 {
					break
				}
				idx := int(math.Sqrt(d2) * 2.0)
				if idx > maxIdx {
					continue
				}
				sa := uint32(lut[idx])
				if sa == 0 {
					continue
				}
				inv := 255 - sa
				sr, sg, sb := (ru*sa)/255, (gu*sa)/255, (bu*sa)/255
				xPos, xNeg := cxi+dx, cxi-1-dx
				if yPos < sh {
					if xPos < sw {
						o := (yPos*sw + xPos) * 4
						pix[o+0] = byte(sb + (uint32(pix[o+0])*inv)/255)
						pix[o+1] = byte(sg + (uint32(pix[o+1])*inv)/255)
						pix[o+2] = byte(sr + (uint32(pix[o+2])*inv)/255)
						pix[o+3] = byte(sa + (uint32(pix[o+3])*inv)/255)
					}
					if xNeg >= 0 {
						o := (yPos*sw + xNeg) * 4
						pix[o+0] = byte(sb + (uint32(pix[o+0])*inv)/255)
						pix[o+1] = byte(sg + (uint32(pix[o+1])*inv)/255)
						pix[o+2] = byte(sr + (uint32(pix[o+2])*inv)/255)
						pix[o+3] = byte(sa + (uint32(pix[o+3])*inv)/255)
					}
				}
				if yNeg >= 0 {
					if xPos < sw {
						o := (yNeg*sw + xPos) * 4
						pix[o+0] = byte(sb + (uint32(pix[o+0])*inv)/255)
						pix[o+1] = byte(sg + (uint32(pix[o+1])*inv)/255)
						pix[o+2] = byte(sr + (uint32(pix[o+2])*inv)/255)
						pix[o+3] = byte(sa + (uint32(pix[o+3])*inv)/255)
					}
					if xNeg >= 0 {
						o := (yNeg*sw + xNeg) * 4
						pix[o+0] = byte(sb + (uint32(pix[o+0])*inv)/255)
						pix[o+1] = byte(sg + (uint32(pix[o+1])*inv)/255)
						pix[o+2] = byte(sr + (uint32(pix[o+2])*inv)/255)
						pix[o+3] = byte(sa + (uint32(pix[o+3])*inv)/255)
					}
				}
			}
		}
	}
	fade := 1 - unit((p-.87)/.13)
	artY := cy - size*.23
	if second != nil {
		slide := cssEase(t/.22, .5, 0, .9, .5)
		disp := size * .64 * (1 - slide)
		y := artY + bobAt(t, .5, 1.6, 5)
		s.blitScaled(getShadow(icon, r, g, b), cx+disp, y-disp, 1, fade*slide)
		s.blitScaled(getShadow(second, r, g, b), cx-disp, y-disp, 1, fade*slide)
	} else {
		ease := func(q float64) float64 { return cssEase(q, .2, 1.6, .4, 1) }
		scale := keyLerp(t, []float64{0, .168, .28}, []float64{.4, 1.15, 1}, ease)
		opacity := keyLerp(t, []float64{0, .168, .28}, []float64{0, 1, 1}, ease)
		s.blitScaled(getShadow(icon, r, g, b), cx, artY+bobAt(t, .3, 1.6, 5), scale, fade*unit(opacity))
	}
}

func openEmote(path string) *emoteAnimation {
	if path == "" {
		return nil
	}
	exe, _ := os.Executable()
	candidates := []string{
		filepath.Join(filepath.Dir(exe), filepath.FromSlash(path)),
		filepath.Join(filepath.Dir(exe), "..", "Resources", filepath.FromSlash(path)),
		path,
	}
	var f *os.File
	var err error
	for _, c := range candidates {
		f, err = os.Open(c)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil
	}
	var file io.ReadSeekCloser = f
	var header [12]byte
	if _, err = io.ReadFull(file, header[:]); err != nil {
		file.Close()
		return nil
	}
	a := &emoteAnimation{
		file:    file,
		current: -1,
		image:   &premultImg{w: int(binary.LittleEndian.Uint32(header[:4])), h: int(binary.LittleEndian.Uint32(header[4:8]))},
	}
	a.image.pix = make([]byte, a.image.w*a.image.h*4)
	for i := uint32(0); i < binary.LittleEndian.Uint32(header[8:]); i++ {
		var entry [8]byte
		if _, err = io.ReadFull(file, entry[:]); err != nil {
			file.Close()
			return nil
		}
		offset, _ := file.Seek(0, io.SeekCurrent)
		size := int64(binary.LittleEndian.Uint32(entry[4:]))
		a.frames = append(a.frames, frameEntry{binary.LittleEndian.Uint32(entry[:4]), offset, size})
		file.Seek(size, io.SeekCurrent)
	}
	return a
}

func (a *emoteAnimation) at(seconds float64) *premultImg {
	idx := len(a.frames) - 1
	for i, frame := range a.frames {
		if uint32(seconds*1000) < frame.endMS {
			idx = i
			break
		}
	}
	if idx != a.current && idx >= 0 {
		f := a.frames[idx]
		a.file.Seek(f.offset, io.SeekStart)
		reader, err := zlib.NewReader(io.LimitReader(a.file, f.size))
		if err == nil {
			_, err = io.ReadFull(reader, a.image.pix)
			reader.Close()
		}
		if err != nil {
			clear(a.image.pix)
		}
		a.current = idx
	}
	return a.image
}

func playSound(path string, volume float32) {
	b := mustAsset(path)
	if len(b) > 0 {
		C.mac_play_wav(unsafe.Pointer(&b[0]), C.int(len(b)), C.float(volume))
	}
}

func sectorFromDelta(dx, dy float64) int {
	if math.Hypot(dx, dy) < hubRadius {
		return -1
	}
	ang := math.Mod(math.Atan2(dy, dx)*180.0/math.Pi+90.0+22.5+360.0, 360.0)
	return int(ang/45.0) % 8
}

func loadMacSlots() {
	mapp.pingCenter = &wheelSlot{
		icon:    decodePNG(mustAsset("assets/textures/pingwheel_basicpingrender.png"), 40, 40, false),
		popIcon: decodePNG(mustAsset("assets/textures/pingwheel_basicpingrender.png"), 110, 110, false),
		sound:   "assets/sounds/Base.wav",
		r:       46, g: 139, b: 255,
	}
	pingDefs := []struct {
		label, tex, snd string
		r, g, b         uint8
	}{
		{"Danger", "assets/textures/pingwheel_retreatrender.png", "assets/sounds/SRP_12.wav", 255, 59, 85},
		{"Push", "assets/textures/pingwheel_pushrender.png", "assets/sounds/SRP_9.wav", 53, 210, 122},
		{"On My Way", "assets/textures/omw_cyan.png", "assets/sounds/OnMyWay.wav", 46, 139, 255},
		{"All In", "assets/textures/png_all_in.png", "assets/sounds/SRP_4.wav", 242, 179, 32},
		{"Assist Me", "assets/textures/png_assist_me_128x128.png", "assets/sounds/ComeHere.wav", 53, 210, 122},
		{"Need Vision", "assets/textures/mt11_18_vision.png", "assets/sounds/SRP_7.wav", 53, 210, 122},
		{"Missing", "assets/textures/png_enemymissing_128x128.png", "assets/sounds/MIA.wav", 242, 179, 32},
		{"Enemy Vision", "assets/textures/pingwheel_enemyvisionrender.png", "assets/sounds/AreaIsWarded.wav", 255, 59, 85},
	}
	for _, d := range pingDefs {
		raw := mustAsset(d.tex)
		icon := decodePNG(raw, 48, 48, false)
		sl := &wheelSlot{
			label:   d.label,
			icon:    icon,
			tinted:  tintPing(icon),
			popIcon: decodePNG(raw, 110, 110, false),
			sound:   d.snd,
			r:       d.r, g: d.g, b: d.b,
		}
		if d.label == "All In" {
			sl.popIcon = decodePNG(mustAsset("assets/textures/pingwheel_allin_render_01.png"), 110, 110, false)
			sl.popSecond = decodePNG(mustAsset("assets/textures/pingwheel_allin_render_02.png"), 110, 110, false)
		}
		mapp.pingSlots = append(mapp.pingSlots, sl)
		getShadow(sl.popIcon, sl.r, sl.g, sl.b)
		if sl.popSecond != nil {
			getShadow(sl.popSecond, sl.r, sl.g, sl.b)
		}
	}
	getShadow(mapp.pingCenter.popIcon, mapp.pingCenter.r, mapp.pingCenter.g, mapp.pingCenter.b)

	emoteDefs := []struct{ label, base string }{
		{"Bee Happy", "bee_happy"},
		{"Bee Mad", "bee_mad"},
		{"Angry Kitty", "angry_kitty"},
		{"Cheeky Poro", "cheeky_poro"},
		{"Facepalm", "facepalm"},
		{"GG Heart", "gg_heart"},
		{"Poro Snax", "poro_snax"},
		{"Sad Kitten", "sad_kitten"},
	}
	for _, d := range emoteDefs {
		raw := mustAsset("assets/emotes/" + d.base + ".png")
		mapp.emoteSlots = append(mapp.emoteSlots, &wheelSlot{
			label:   d.label,
			frames:  "assets/emotes/" + d.base + ".frames",
			icon:    roundIcon(decodePNG(raw, 48, 48, false)),
			popIcon: decodePNG(raw, 150, 150, false),
			sound:   "assets/emotes/" + d.base + ".wav",
			r:       255, g: 255, b: 255,
		})
	}
	mapp.emoteCenter = mapp.emoteSlots[0]
}

func acquireMacPopSlot() *macPopSlot {
	for _, ps := range mapp.popPool {
		if !ps.inUse {
			ps.inUse = true
			return ps
		}
	}
	ps := &macPopSlot{surf: newMacSurface(popSize, popSize), inUse: true}
	mapp.popPool = append(mapp.popPool, ps)
	return ps
}

func startMacEffect(slot *wheelSlot, emote bool) {
	pingCount := 0
	live := mapp.effects[:0]
	for _, e := range mapp.effects {
		if e.emote && emote {
			if e.animation != nil {
				e.animation.file.Close()
			}
			e.poolSlot.surf.hide()
			e.poolSlot.inUse = false
		} else {
			live = append(live, e)
			if !e.emote {
				pingCount++
			}
		}
	}
	mapp.effects = live
	if !emote && pingCount >= maxConcurrentPings {
		for i, e := range mapp.effects {
			if !e.emote {
				e.poolSlot.surf.hide()
				e.poolSlot.inUse = false
				mapp.effects = append(mapp.effects[:i], mapp.effects[i+1:]...)
				break
			}
		}
	}
	ps := acquireMacPopSlot()
	e := &macPopEffect{
		poolSlot: ps,
		slot:     slot,
		emote:    emote,
		start:    time.Now(),
		x:        mapp.anchorX - popSize/2,
		y:        mapp.anchorY - popSize/2,
	}
	if emote {
		e.animation = openEmote(slot.frames)
	}
	mapp.effects = append(mapp.effects, e)
	if e.emote && e.animation != nil {
		ps.surf.clear()
		ps.surf.blitScaled(e.animation.at(0), popSize/2, popSize/2, 187.5/188, 1)
	} else {
		ps.surf.drawPop(slot.popIcon, 0, slot.r, slot.g, slot.b, emote, slot.popSecond)
	}
	ps.surf.present(e.x, e.y, 1.0)
	C.mac_set_animating(1)
}

func renderMacWheel() {
	now := time.Now()
	if mapp.hoverIdx != mapp.activeIdx {
		mapp.hoverFrom = mapp.hover
		mapp.wedgeFrom = mapp.wedge
		mapp.hoverStart = now
		mapp.hoverIdx = mapp.activeIdx
		if mapp.activeIdx >= 0 && mapp.dragging {
			playSound("assets/sounds/button.wav", 0.35)
		}
	}
	u := cssEase(now.Sub(mapp.hoverStart).Seconds()/.1, .25, .1, .25, 1)
	uw := cssEase(now.Sub(mapp.hoverStart).Seconds()/.08, .25, .1, .25, 1)
	for i := 0; i < 8; i++ {
		target := 0.0
		if i == mapp.activeIdx {
			target = 1
		}
		mapp.hover[i] = lerp(mapp.hoverFrom[i], target, u)
		mapp.wedge[i] = lerp(mapp.wedgeFrom[i], target, uw)
	}
	mapp.wheelSurf.drawWheel()
	t := cssEase(now.Sub(mapp.wheelTransition).Seconds()/.12, 0, 0, .58, 1)
	opacity := t
	if mapp.wheelClosing {
		opacity = mapp.wheelOpacity * (1 - t)
	}
	mapp.wheelSurf.present(mapp.anchorX-wheelSize/2, mapp.anchorY-wheelSize/2, opacity)
	if mapp.wheelClosing && t >= 1 {
		mapp.wheelClosing = false
		mapp.wheelSurf.hide()
	}
}

//export goOnMouseDown
func goOnMouseDown(x, y, emoteMode C.int) {
	mapp.dragging = true
	mapp.emoteMode = emoteMode != 0
	mapp.wheelClosing = false
	mapp.wheelTransition = time.Now()
	mapp.anchorX = int(x)
	mapp.anchorY = int(y)
	mapp.activeIdx = -1
	mapp.hoverIdx = -1
	mapp.hover = [8]float64{}
	mapp.wedge = [8]float64{}
	mapp.hoverFrom = mapp.hover
	mapp.wedgeFrom = mapp.wedge
	mapp.wheelDirty = true
	renderMacWheel()
	C.mac_set_animating(1)
}

//export goOnMouseMove
func goOnMouseMove(x, y C.int) {
	if !mapp.dragging {
		return
	}
	sec := sectorFromDelta(float64(int(x)-mapp.anchorX), float64(int(y)-mapp.anchorY))
	if sec != mapp.activeIdx {
		mapp.activeIdx = sec
		mapp.wheelDirty = true
	}
}

//export goOnMouseUp
func goOnMouseUp(x, y C.int) {
	if !mapp.dragging {
		return
	}
	mapp.dragging = false
	mapp.activeIdx = sectorFromDelta(float64(int(x)-mapp.anchorX), float64(int(y)-mapp.anchorY))
	var slot *wheelSlot
	if mapp.emoteMode {
		if mapp.activeIdx >= 0 && mapp.activeIdx < len(mapp.emoteSlots) {
			slot = mapp.emoteSlots[mapp.activeIdx]
		} else {
			slot = mapp.emoteCenter
		}
	} else {
		if mapp.activeIdx >= 0 && mapp.activeIdx < len(mapp.pingSlots) {
			slot = mapp.pingSlots[mapp.activeIdx]
		} else {
			slot = mapp.pingCenter
		}
	}
	mapp.wheelOpacity = cssEase(time.Since(mapp.wheelTransition).Seconds()/.12, 0, 0, .58, 1)
	mapp.wheelClosing = true
	mapp.wheelTransition = time.Now()
	mapp.activeIdx = -1
	playSound(slot.sound, 0.7)
	startMacEffect(slot, mapp.emoteMode)
}

//export goOnRightCancel
func goOnRightCancel() {
	if !mapp.dragging {
		return
	}
	mapp.dragging = false
	mapp.activeIdx = -1
	mapp.wheelOpacity = cssEase(time.Since(mapp.wheelTransition).Seconds()/.12, 0, 0, .58, 1)
	mapp.wheelClosing = true
	mapp.wheelTransition = time.Now()
	C.mac_set_animating(1)
}

//export goOnFrameTick
func goOnFrameTick() {
	if mapp.dragging || mapp.wheelClosing {
		needDraw := mapp.wheelClosing || mapp.wheelDirty || mapp.hoverIdx != mapp.activeIdx ||
			time.Since(mapp.hoverStart) <= 130*time.Millisecond ||
			time.Since(mapp.wheelTransition) <= 150*time.Millisecond
		if needDraw {
			mapp.wheelDirty = false
			renderMacWheel()
		}
	}
	if len(mapp.effects) > 0 {
		now := time.Now()
		live := mapp.effects[:0]
		var elapsedList [maxConcurrentPings + 2]float64
		for _, e := range mapp.effects {
			el := now.Sub(e.start).Seconds()
			dur := 3.2
			if e.emote {
				dur = 3.0
			}
			if el >= dur+.15 {
				if e.animation != nil {
					e.animation.file.Close()
				}
				e.poolSlot.surf.hide()
				e.poolSlot.inUse = false
			} else {
				elapsedList[len(live)] = el
				live = append(live, e)
			}
		}
		mapp.effects = live
		if len(live) > 1 {
			var wg sync.WaitGroup
			wg.Add(len(live))
			for i, e := range live {
				el := elapsedList[i]
				go func(eff *macPopEffect, t float64) {
					if eff.emote && eff.animation != nil {
						eff.poolSlot.surf.clear()
						eff.poolSlot.surf.blitScaled(eff.animation.at(t), popSize/2, popSize/2, 187.5/188, 1)
					} else {
						dur := 3.2
						if eff.emote {
							dur = 3.0
						}
						eff.poolSlot.surf.drawPop(eff.slot.popIcon, t/dur, eff.slot.r, eff.slot.g, eff.slot.b, eff.emote, eff.slot.popSecond)
					}
					wg.Done()
				}(e, el)
			}
			wg.Wait()
		} else if len(live) == 1 {
			e, el := live[0], elapsedList[0]
			if e.emote && e.animation != nil {
				e.poolSlot.surf.clear()
				e.poolSlot.surf.blitScaled(e.animation.at(el), popSize/2, popSize/2, 187.5/188, 1)
			} else {
				dur := 3.2
				if e.emote {
					dur = 3.0
				}
				e.poolSlot.surf.drawPop(e.slot.popIcon, el/dur, e.slot.r, e.slot.g, e.slot.b, e.emote, e.slot.popSecond)
			}
		}
		for _, e := range live {
			e.poolSlot.surf.present(e.x, e.y, 1.0)
		}
	}
	if !mapp.dragging && !mapp.wheelClosing && len(mapp.effects) == 0 {
		C.mac_set_animating(0)
	}
}

//export goOnTrayToggle
func goOnTrayToggle() {
	mapp.enabled = !mapp.enabled
	if !mapp.enabled {
		mapp.dragging = false
		mapp.wheelSurf.hide()
		C.mac_set_enabled_check(0)
	} else {
		C.mac_set_enabled_check(1)
	}
}

//export goOnTrayTest
func goOnTrayTest() {
	mapp.anchorX = 640
	mapp.anchorY = 400
	playSound(mapp.pingCenter.sound, 0.7)
	startMacEffect(mapp.pingCenter, false)
}

//export goOnTrayQuit
func goOnTrayQuit() {
	C.mac_quit_app()
}

func main() {
	runtime.LockOSThread()
	debug.SetMemoryLimit(48 << 20)

	iconPng := mustAsset("assets/textures/pingwheel_basicpingrender.png")
	C.mac_init_app(unsafe.Pointer(&iconPng[0]), C.int(len(iconPng)))

	mapp = &macAppState{
		enabled:   true,
		activeIdx: -1,
		hoverIdx:  -1,
		wheelSurf: newMacSurface(wheelSize, wheelSize),
	}
	loadMacSlots()
	mapp.wheelSurf.buildWheelBase()

	C.mac_run_loop()
}
