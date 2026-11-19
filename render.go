//go:build windows

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"syscall"
	"unsafe"
)

// Quantized to 1/64 CSS px, keeping the complete polar cache at 4 bytes/pixel.
type wheelPixel uint32

func (p wheelPixel) radius() float64  { return float64(uint32(p)&32767) / 64 }
func (p wheelPixel) divider() float64 { return float64((uint32(p)>>15)&16383) / 64 }
func (p wheelPixel) sector() int      { return int(uint32(p) >> 29) }

type wedgePix struct {
	idx        uint32
	r, g, b, a uint8
}

type layeredSurface struct {
	width, height        int
	hdcMem, hbmp, oldObj uintptr
	pixels               []byte // BGRA, premultiplied alpha (UpdateLayeredWindow contract).
	wheelBase            []byte
	animationPixels      []byte
	shadows              map[shadowKey]*premultImg
	textCache            map[string]*premultImg
	polar                []wheelPixel
	sectorWedges         [8][]wedgePix
}

func newLayeredSurface(w, h int) *layeredSurface {
	dc, _, _ := pGetDC.Call(0)
	mem, _, _ := pCreateCompatibleDC.Call(dc)
	pReleaseDC.Call(0, dc)
	bi := BITMAPINFO{}
	bi.BmiHeader = BITMAPINFOHEADER{BiSize: uint32(unsafe.Sizeof(BITMAPINFOHEADER{})), BiWidth: int32(w), BiHeight: -int32(h), BiPlanes: 1, BiBitCount: 32}
	var bits unsafe.Pointer
	bmp, _, _ := pCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bi)), DIB_RGB_COLORS, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if mem == 0 || bmp == 0 || bits == nil {
		panic("Cannot allocate layered window surface")
	}
	old, _, _ := pSelectObject.Call(mem, bmp)
	return &layeredSurface{width: w, height: h, hdcMem: mem, hbmp: bmp, oldObj: old, pixels: unsafe.Slice((*byte)(bits), w*h*4)}
}

func (s *layeredSurface) destroy() {
	if s.hdcMem != 0 {
		pSelectObject.Call(s.hdcMem, s.oldObj)
		pDeleteObject.Call(s.hbmp)
		pDeleteDC.Call(s.hdcMem)
		s.hdcMem = 0
		s.pixels = nil
	}
}
func (s *layeredSurface) clear() { clear(s.pixels) }
func (s *layeredSurface) present(hwnd uintptr, x, y int32) bool {
	dst := POINT{x, y}
	size := SIZE{int32(s.width), int32(s.height)}
	src := POINT{}
	blend := BLENDFUNCTION{SourceConstantAlpha: 255, AlphaFormat: AC_SRC_ALPHA}
	r, _, _ := pUpdateLayeredWindow.Call(hwnd, 0, uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&size)), s.hdcMem, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ULW_ALPHA)
	return r != 0
}

type premultImg struct {
	w, h int
	pix  []byte
} // RGBA, premultiplied.

// Trim transparent export padding, fit the ink without stretching, and integrate
// source texels over each destination pixel. Box filtering retains fine gold
// outlines when reducing 128/256px exports; sampling premultiplied alpha avoids halos.
func decodeAndResizePNG(data []byte, tw, th int) *premultImg {
	return decodePNG(data, tw, th, true)
}

func decodePNG(data []byte, tw, th int, trim bool) *premultImg {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}
	bounds := src.Bounds()
	ink := image.Rectangle{}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			if a > 512 {
				ink = ink.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	out := &premultImg{tw, th, make([]byte, tw*th*4)}
	if !trim {
		ink = bounds
	}
	if ink.Empty() {
		return out
	}
	scale := math.Min(float64(tw)/float64(ink.Dx()), float64(th)/float64(ink.Dy()))
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
func clampByte(v float64) byte { return byte(math.Max(0, math.Min(255, v)) + 0.5) }
func unit(v float64) float64   { return math.Max(0, math.Min(1, v)) }
func smooth(v float64) float64 { v = unit(v); return v * v * (3 - 2*v) }

func (s *layeredSurface) blendPixel(x, y int, r, g, b, a uint8) {
	if x < 0 || y < 0 || x >= s.width || y >= s.height || a == 0 {
		return
	}
	i := (y*s.width + x) * 4
	inv := 255 - uint32(a)
	s.pixels[i] = byte(uint32(b) + uint32(s.pixels[i])*inv/255)
	s.pixels[i+1] = byte(uint32(g) + uint32(s.pixels[i+1])*inv/255)
	s.pixels[i+2] = byte(uint32(r) + uint32(s.pixels[i+2])*inv/255)
	s.pixels[i+3] = byte(uint32(a) + uint32(s.pixels[i+3])*inv/255)
}

// Paint accepts straight RGB and coverage; every procedural primitive goes
// through this conversion, keeping RGB <= alpha even in the soft glow fringes.
func (s *layeredSurface) paint(x, y int, r, g, b uint8, opacity float64) {
	a := clampByte(255 * unit(opacity))
	f := float64(a) / 255
	s.blendPixel(x, y, clampByte(float64(r)*f), clampByte(float64(g)*f), clampByte(float64(b)*f), a)
}
func (s *layeredSurface) blitPremult(im *premultImg, cx, cy int, alpha float64) {
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
		sx0 = -x0
		x0 = 0
	}
	if y0 < 0 {
		sy0 = -y0
		y0 = 0
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
	src := im.pix
	dst := s.pixels
	sw, dw := im.w, s.width
	if au == 255 {
		for y := 0; y < h; y++ {
			si := ((sy0+y)*sw + sx0) * 4
			di := ((y0+y)*dw + x0) * 4
			for x := 0; x < w; x++ {
				sa := uint32(src[si+3])
				if sa == 255 {
					dst[di+0] = src[si+2]
					dst[di+1] = src[si+1]
					dst[di+2] = src[si+0]
					dst[di+3] = 255
				} else if sa != 0 {
					inv := 255 - sa
					dst[di+0] = byte(uint32(src[si+2]) + (uint32(dst[di+0])*inv)/255)
					dst[di+1] = byte(uint32(src[si+1]) + (uint32(dst[di+1])*inv)/255)
					dst[di+2] = byte(uint32(src[si+0]) + (uint32(dst[di+2])*inv)/255)
					dst[di+3] = byte(sa + (uint32(dst[di+3])*inv)/255)
				}
				si += 4
				di += 4
			}
		}
		return
	}
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

// Fast fixed-point bilinear blit; delegates to integer blitPremult when scale ~ 1.
func (s *layeredSurface) blitScaled(im *premultImg, cx, cy, scale, alpha float64) {
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
	src := im.pix
	dst := s.pixels
	sw, sh, dw := im.w, im.h, s.width

	for y := yStart; y < yEnd; y++ {
		iy := int(syCur >> 16)
		fy := uint32((syCur >> 8) & 0xFF)
		ify := 256 - fy
		syCur += step
		if iy < -1 || iy >= sh {
			continue
		}
		y0Ok := iy >= 0
		y1Ok := iy+1 < sh
		row0 := iy * sw
		row1 := (iy + 1) * sw
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
						o := (row0 + ix) * 4
						w00 := ifx * ify
						r += uint32(src[o+0]) * w00
						g += uint32(src[o+1]) * w00
						b += uint32(src[o+2]) * w00
						a += uint32(src[o+3]) * w00
					}
					if ix+1 < sw {
						o := (row0 + ix + 1) * 4
						w10 := fx * ify
						r += uint32(src[o+0]) * w10
						g += uint32(src[o+1]) * w10
						b += uint32(src[o+2]) * w10
						a += uint32(src[o+3]) * w10
					}
				}
				if y1Ok {
					if ix >= 0 {
						o := (row1 + ix) * 4
						w01 := ifx * fy
						r += uint32(src[o+0]) * w01
						g += uint32(src[o+1]) * w01
						b += uint32(src[o+2]) * w01
						a += uint32(src[o+3]) * w01
					}
					if ix+1 < sw {
						o := (row1 + ix + 1) * 4
						w11 := fx * fy
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

// Constants are the SVG viewBox units from upstream shared/geometry.ts.
const (
	hubRadius  = 86.0
	wheelOuter = 188.0
	wheelExt   = 288.0
	wheelIconR = 137.0
)

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

// CSS cubic-bezier: solve x before evaluating y (y may overshoot).
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
	return [3]uint8{clampByte(lerp(float64(a[0]), float64(b[0]), t)), clampByte(lerp(float64(a[1]), float64(b[1]), t)), clampByte(lerp(float64(a[2]), float64(b[2]), t))}
}
func tintPing(im *premultImg) *premultImg { return tintPingAmount(im, 0) }
func tintPingAmount(im *premultImg, hot float64) *premultImg {
	out := &premultImg{im.w, im.h, make([]byte, len(im.pix))}
	amount := 1 - hot
	for i := 0; i < len(im.pix); i += 4 {
		r, g, b := float64(im.pix[i]), float64(im.pix[i+1]), float64(im.pix[i+2])
		a := float64(im.pix[i+3])
		gray := .2126*r + .7152*g + .0722*b
		r = lerp(r, gray, amount)
		g = lerp(g, gray, amount)
		b = lerp(b, gray, amount)
		sep := .75 * amount
		rr := lerp(r, .393*r+.769*g+.189*b, sep)
		gg := lerp(g, .349*r+.686*g+.168*b, sep)
		bb := lerp(b, .272*r+.534*g+.131*b, sep)
		r = math.Min(a, rr)
		g = math.Min(a, gg)
		b = math.Min(a, bb)
		sat := 1 + .3*amount
		rr = (.213+.787*sat)*r + (.715-.715*sat)*g + (.072-.072*sat)*b
		gg = (.213-.213*sat)*r + (.715+.285*sat)*g + (.072-.072*sat)*b
		bb = (.213-.213*sat)*r + (.715-.715*sat)*g + (.072+.928*sat)*b
		bright := 1 + .15*amount
		out.pix[i] = clampByte(math.Min(a, math.Max(0, rr)) * bright)
		out.pix[i+1] = clampByte(math.Min(a, math.Max(0, gg)) * bright)
		out.pix[i+2] = clampByte(math.Min(a, math.Max(0, bb)) * bright)
		for k := 0; k < 3; k++ {
			if out.pix[i+k] > im.pix[i+3] {
				out.pix[i+k] = im.pix[i+3]
			}
		}
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

// Cache static wheel geometry (metallic ring, flares, dividers, core, BACK label, mouse icon)
// and compact per-sector wedge lists so drawWheel never scans 360k pixels per frame.
func (s *layeredSurface) buildWheelBase() {
	s.clear()
	s.polar = nil
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
			sector := int(a / (math.Pi / 4))
			if sector > 7 {
				sector = 7
			}
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
	// Bake static core rings + BACK text + mouse icon into wheelBase once.
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
	s.text("BACK", cx+6*scale, cy+50*scale, 9*scale, .5*scale, [3]uint8{108, 122, 124})
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

func (s *layeredSurface) drawWheel(icons []*premultImg, center *premultImg, active int, _, _ float64, _ [][3]uint8, _, _, _ uint8) {
	if s.wheelBase == nil {
		s.buildWheelBase()
	}
	copy(s.pixels, s.wheelBase)
	scale := float64(s.width) / 600
	cx, cy := float64(s.width)/2, float64(s.height)/2
	animated := app != nil && s == app.wheelSurf

	// Only blend pixels of active/fading sectors (0 pixels when idle, ~15k when hovered).
	for sec := 0; sec < 8; sec++ {
		hot := 0.0
		if sec == active {
			hot = 1
		}
		if animated {
			hot = app.wedge[sec]
		}
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

	for i, im := range icons {
		hot := 0.0
		if i == active {
			hot = 1
		}
		if animated {
			hot = app.hover[i]
		}
		a := (float64(i)*45 - 90) * math.Pi / 180
		ix := cx + wheelIconR*scale*math.Cos(a)
		iy := cy + wheelIconR*scale*math.Sin(a)
		sc := 48 * scale / float64(im.w) * (1 + .18*hot)
		emote := app != nil && app.emoteMode
		if !emote && hot < 0.99 && app != nil && i < len(app.pingSlots) && im == app.pingSlots[i].icon && app.pingSlots[i].tinted != nil {
			if hot <= 0.01 {
				s.blitScaled(app.pingSlots[i].tinted, ix, iy, sc, 1)
			} else {
				s.blitScaled(app.pingSlots[i].tinted, ix, iy, sc, 1-hot)
				s.blitScaled(im, ix, iy, sc, hot)
			}
		} else {
			s.blitScaled(im, ix, iy, sc, 1)
		}
	}
	s.blitScaled(center, cx, cy-38*scale, 40*scale/float64(max(1, imageWidth(center))), 1)
	label := "PING"
	if app != nil && app.emoteMode {
		label = "EMOTE"
	}
	if app != nil && active >= 0 {
		slots := app.pingSlots
		if app.emoteMode {
			slots = app.emoteSlots
		}
		if active < len(slots) {
			label = slots[active].label
		}
	}
	chars := []rune(label)
	if len(chars) > 14 {
		label = string(chars[:13]) + "\u2026"
	}
	s.text(label, cx, cy+6*scale, 15*scale, scale, [3]uint8{31, 169, 224})
}
func imageWidth(im *premultImg) int {
	if im == nil {
		return 1
	}
	return im.w
}

// GDI rasterizes white text on black with grayscale antialiasing, then the
// luminance mask is composited in the requested color into premultiplied BGRA.
// ClearType is deliberately disabled: its subpixels produce colored fringes on
// transparent layered windows. Baselines and tracking match the SVG text nodes.
func (s *layeredSurface) text(value string, cx, baseline, height, spacing float64, c [3]uint8) {
	key := fmt.Sprintf("%s/%g/%g/%v", value, height, spacing, c)
	if im := s.textCache[key]; im != nil {
		s.blitPremult(im, int(math.Round(cx)), int(math.Round(baseline)), 1)
		return
	}
	w, h := int(height*20), int(height*2+6)
	mask := newLayeredSurface(w, h)
	defer mask.destroy()
	face, _ := syscall.UTF16PtrFromString("Segoe UI Semibold")
	font, _, _ := pCreateFontW.Call(uintptr(-int32(math.Round(height))), 0, 0, 0, 600, 0, 0, 0, 1, 0, 0, 4, 0, uintptr(unsafe.Pointer(face)))
	old, _, _ := pSelectObject.Call(mask.hdcMem, font)
	defer pDeleteObject.Call(font)
	defer pSelectObject.Call(mask.hdcMem, old)
	pSetTextColor.Call(mask.hdcMem, 0xffffff)
	pSetBkMode.Call(mask.hdcMem, 1)
	var metrics TEXTMETRICW
	pGetTextMetricsW.Call(mask.hdcMem, uintptr(unsafe.Pointer(&metrics)))
	// SVG letter-spacing includes a trailing advance in its anchored text chunk.
	chars := []rune(value)
	advances := make([]int, len(chars))
	total := 0
	for i, ch := range chars {
		str, _ := syscall.UTF16PtrFromString(string(ch))
		var size SIZE
		pGetTextExtentPoint32W.Call(mask.hdcMem, uintptr(unsafe.Pointer(str)), uintptr(len(syscall.StringToUTF16(string(ch)))-1), uintptr(unsafe.Pointer(&size)))
		advances[i] = int(size.CX)
		total += int(size.CX)
	}
	left := cx - (float64(total)+spacing*float64(len(chars)))/2
	x := 0.0
	for i, ch := range chars {
		str, _ := syscall.UTF16PtrFromString(string(ch))
		pTextOutW.Call(mask.hdcMem, uintptr(int(x)), 0, uintptr(unsafe.Pointer(str)), uintptr(len(syscall.StringToUTF16(string(ch)))-1))
		x += float64(advances[i]) + spacing
	}
	pGdiFlush.Call()
	im := &premultImg{w: w, h: h * 2, pix: make([]byte, w*h*2*4)}
	for yy := 0; yy < h; yy++ {
		for xx := 0; xx < min(w, int(x)+2); xx++ {
			alpha := mask.pixels[(yy*w+xx)*4]
			tx := int(math.Round(left-cx)) + xx + w/2
			ty := yy - int(metrics.TmAscent) + h
			if tx < 0 || tx >= w || ty < 0 || ty >= 2*h {
				continue
			}
			i := (ty*w + tx) * 4
			im.pix[i] = byte(uint32(c[0]) * uint32(alpha) / 255)
			im.pix[i+1] = byte(uint32(c[1]) * uint32(alpha) / 255)
			im.pix[i+2] = byte(uint32(c[2]) * uint32(alpha) / 255)
			im.pix[i+3] = alpha
		}
	}
	if s.textCache == nil {
		s.textCache = make(map[string]*premultImg)
	}
	s.textCache[key] = im
	s.blitPremult(im, int(math.Round(cx)), int(math.Round(baseline)), 1)
}

// Use Win32 hardware SourceConstantAlpha directly instead of software-resizing
// 600x600 pixels on CPU every frame.
func (s *layeredSurface) presentAnimated(hwnd uintptr, x, y int32, opacity, _ float64) {
	dst := POINT{x, y}
	size := SIZE{int32(s.width), int32(s.height)}
	src := POINT{}
	blend := BLENDFUNCTION{SourceConstantAlpha: clampByte(opacity * 255), AlphaFormat: AC_SRC_ALPHA}
	pUpdateLayeredWindow.Call(hwnd, 0, uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&size)), s.hdcMem, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ULW_ALPHA)
}

// CSS filter drop-shadow is a Gaussian blur of the image's alpha, not a radial
// glow around its bounding box. Three box passes approximate the browser blur.
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
		out.pix[i*4] = byte(uint32(r) * uint32(alpha) / 255)
		out.pix[i*4+1] = byte(uint32(g) * uint32(alpha) / 255)
		out.pix[i*4+2] = byte(uint32(b) * uint32(alpha) / 255)
		out.pix[i*4+3] = alpha
	}
	return out
}
var globalShadows = make(map[shadowKey]*premultImg)

// Pre-composite shadow + icon into ONE premultiplied RGBA image so pingArt
// performs a single blit per frame instead of two.
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

func (s *layeredSurface) pingArt(im *premultImg, cx, cy, scale, alpha float64, r, g, b uint8) {
	if im == nil {
		return
	}
	s.blitScaled(getShadow(im, r, g, b), cx, cy, scale, alpha)
}

type shadowKey struct {
	im      *premultImg
	r, g, b uint8
}

func bobAt(t, delay, period float64, amplitude float64) float64 {
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
func (s *layeredSurface) drawPop(icon *premultImg, p float64, r, g, b uint8, emote bool, second ...*premultImg) {
	s.clear()
	if p < 0 || p >= 1 {
		return
	}
	cx, cy := float64(s.width)/2, float64(s.height)/2
	if emote { // Custom/placeholder envelope; bundled emotes use their baked timeline.
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
		s.pingArt(icon, cx, cy+bob, 150/float64(icon.w)*scale, fade, 255, 255, 255)
		return
	}
	t := p * 3.2
	size := 110.0
	// Ground ellipse: closest-side radial-gradient and per-segment ease-out.
	ground := keyLerp(p, []float64{0, .1, .85, 1}, []float64{0, .75, .55, 0}, easeOut)
	for y := int(cy + size*.2); y < int(cy+size*.44); y++ {
		for x := int(cx - size*.42); x < int(cx+size*.42); x++ {
			d := math.Hypot((float64(x)+.5-cx)/(size*.42), (float64(y)+.5-cy-size*.32)/(size*.12))
			s.paint(x, y, r, g, b, ground*unit(1-d))
		}
	}
	// Exactly three rings, 650ms each, with independent 0/220/1400ms starts.
	// Precompute 1D radial alpha LUT (half-pixel resolution) + 4-quadrant symmetry:
	// ~450x fewer math.Erfc calls and ~4x fewer sqrt calls with identical visuals.
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
			// Combine the two consecutive paint() calls analytically: a = a1 + a2*(1 - a1)
			comb := a1 + a2*(1-a1)
			lut[i] = clampByte(comb * 255)
		}
		extI := int(math.Ceil(extent))
		ext2 := extent * extent
		cxi, cyi := s.width/2, s.height/2
		pix := s.pixels
		sw, sh := s.width, s.height
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
	if len(second) > 0 && second[0] != nil {
		slide := cssEase(t/.22, .5, 0, .9, .5)
		disp := size * .64 * (1 - slide)
		shakeX, shakeY := 0.0, 0.0
		if t >= .22 && t <= .47 {
			q := (t - .22) / .25
			shakeX = keyLerp(q, []float64{0, .25, .5, .75, 1}, []float64{0, -3, 3, -2, 0}, unit)
			shakeY = keyLerp(q, []float64{0, .25, .5, .75, 1}, []float64{0, 2, -2, -1, 0}, unit)
		}
		y := artY + shakeY + bobAt(t, .5, 1.6, 5)
		s.pingArt(icon, cx+disp+shakeX, y-disp, 1, fade*slide, r, g, b)
		s.pingArt(second[0], cx-disp+shakeX, y-disp, 1, fade*slide, r, g, b)
		if t >= .2 && t < .55 {
			q := (t - .2) / .35
			alpha := keyLerp(q, []float64{0, .3, 1}, []float64{0, 1, 0}, easeOut) * fade
			radius := size * .25 * lerp(.3, 2.2, easeOut(q))
			for yy := int(artY - radius); yy <= int(artY+radius); yy++ {
				for xx := int(cx - radius); xx <= int(cx+radius); xx++ {
					d := math.Hypot(float64(xx)+.5-cx, float64(yy)+.5-artY) / radius
					c := [3]uint8{r, g, b}
					if d < .4 {
						c = mixColor([3]uint8{255, 255, 255}, c, d/.4)
					}
					s.paint(xx, yy, c[0], c[1], c[2], alpha*unit((1-d)/.6))
				}
			}
		}
	} else {
		ease := func(q float64) float64 { return cssEase(q, .2, 1.6, .4, 1) }
		scale := keyLerp(t, []float64{0, .168, .28}, []float64{.4, 1.15, 1}, ease)
		opacity := keyLerp(t, []float64{0, .168, .28}, []float64{0, 1, 1}, ease)
		s.pingArt(icon, cx, artY+bobAt(t, .3, 1.6, 5), scale, fade*unit(opacity), r, g, b)
	}
}

// glowFx.ts is a teammate FIELD outline, not a ping beam. Delay is managed by
// the caller; this primitive matches its 2px border, 4px corners and 900ms fade.
func (s *layeredSurface) drawFieldGlow(x, y, w, h, elapsed float64, r, g, b uint8) {
	if elapsed < 0 || elapsed >= .9 {
		return
	}
	alpha := 1 - easeIn(elapsed/.9)
	for yy := max(0, int(y-12)); yy < min(s.height, int(y+h+12)); yy++ {
		for xx := max(0, int(x-12)); xx < min(s.width, int(x+w+12)); xx++ {
			qx := math.Abs(float64(xx)+.5-x-w/2) - (w/2 - 4)
			qy := math.Abs(float64(yy)+.5-y-h/2) - (h/2 - 4)
			d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - 4
			// box-sizing border-box puts the outline inside the supplied rectangle.
			dist := math.Abs(d + 1)
			glow := .5 * (math.Erf((dist+1)/(math.Sqrt2*4)) - math.Erf((dist-1)/(math.Sqrt2*4)))
			s.paint(xx, yy, r, g, b, alpha*glow)
			s.paint(xx, yy, r, g, b, alpha*unit(1.5-dist))
		}
	}
}

func (s *layeredSurface) releaseWheelCache() {
	s.wheelBase = nil
	s.polar = nil
	s.animationPixels = nil
	s.textCache = nil
}
