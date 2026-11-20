//go:build windows

package main

import (
	"encoding/json"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

type AppConfig struct {
	PingKey    string    `json:"ping_key"`
	EmoteKey   string    `json:"emote_key"`
	Volume     int       `json:"volume"`
	Scale      int       `json:"scale"`
	AutoStart  bool      `json:"auto_start"`
	EmoteSlots [9]string `json:"emote_slots"`
}

var defaultEmoteSlugs = [9]string{
	"unworthy_cn",
	"poro_snax",
	"sad_kitten",
	"cheeky_poro",
	"angry_kitty",
	"gg_heart",
	"bee_happy",
	"bee_mad",
	"facepalm",
}

func isBuiltInAnimated(slug string) bool {
	if strings.HasPrefix(slug, "file:") {
		return false
	}
	_, err := embeddedAssets.ReadFile("assets/emotes/" + slug + ".wav")
	return err == nil
}

type paintStruct struct {
	hdc         uintptr
	fErase      int32
	rcPaint     [4]int32
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

var (
	advapi32         = syscall.NewLazyDLL("advapi32.dll")
	pRegCreateKeyExW = advapi32.NewProc("RegCreateKeyExW")
	pRegSetValueExW  = advapi32.NewProc("RegSetValueExW")
	pRegDeleteValueW = advapi32.NewProc("RegDeleteValueW")
	pRegQueryValueEx = advapi32.NewProc("RegQueryValueExW")
	pRegCloseKey     = advapi32.NewProc("RegCloseKey")

	comdlg32          = syscall.NewLazyDLL("comdlg32.dll")
	pGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")

	pSendMessageW   = user32.NewProc("SendMessageW")
	pIsWindow       = user32.NewProc("IsWindow")
	pSetWindowTxt   = user32.NewProc("SetWindowTextW")
	pBeginPaint     = user32.NewProc("BeginPaint")
	pEndPaint       = user32.NewProc("EndPaint")
	pInvalidateRect = user32.NewProc("InvalidateRect")
	pBitBlt         = gdi32.NewProc("BitBlt")

	cfg = AppConfig{
		PingKey:    "alt_or_g",
		EmoteKey:   "ctrl",
		Volume:     70,
		Scale:      100,
		EmoteSlots: defaultEmoteSlugs,
	}

	settingsHwnd     uintptr
	settingsFont     uintptr
	cbPingHwnd       uintptr
	cbEmoteHwnd      uintptr
	cbVolumeHwnd     uintptr
	cbScaleHwnd      uintptr
	chkAutoStartHwnd uintptr
	lblStatusHwnd    uintptr
	settingsClassReg bool

	draftEmotes     [9]string
	selectedSlot    int
	hoverGridIdx    = -1
	allEmoteChoices []keyOption
	emoteThumbs     map[string]*premultImg
	settingsCanvas  *layeredSurface
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

	wmPaint      = 0x000F
	wmSetFont    = 0x0030
	wmClose      = 0x0010
	cbAddString  = 0x0143
	cbSetCurSel  = 0x014E
	cbGetCurSel  = 0x0147
	bmGetCheck   = 0x00F0
	bmSetCheck   = 0x00F1
	bstChecked   = 1
	bstUnchecked = 0
	srcCopy      = 0x00CC0020

	ofnFileMustExist = 0x00001000
	ofnPathMustExist = 0x00000800
	ofnNoChangeDir   = 0x00000008

	vkShift = 0x10
	vkT     = 0x54
	vkV     = 0x56
	vkY     = 0x59

	idBtnBrowse  = 2012
	idBtnResetEm = 2013
	idBtnSave    = 2001
	idBtnTest    = 2002
	idBtnClose   = 2003

	// Visual picker canvas geometry inside the settings window
	canvasX = 18
	canvasY = 128
	canvasW = 734
	canvasH = 304

	wheelCX = 132
	wheelCY = 152
	wheelR0 = 38
	wheelR1 = 114
	wheelIR = 78

	gridX    = 280
	gridY    = 10
	gridCols = 8
	cellW    = 55
	cellH    = 47
)

type keyOption struct {
	id    string
	label string
}

type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
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

var scaleOptions = []struct {
	val   int
	label string
}{
	{75, "75% (Compact)"},
	{85, "85%"},
	{100, "100% (Default)"},
	{115, "115%"},
	{125, "125% (Large)"},
}

var emoteSlotNames = [9]string{
	"Top (N)",
	"Top-Right (NE)",
	"Right (E)",
	"Bottom-Right (SE)",
	"Bottom (S)",
	"Bottom-Left (SW)",
	"Left (W)",
	"Top-Left (NW)",
	"Center",
}

func currentWheelSize() int {
	sc := cfg.Scale
	if sc < 50 || sc > 200 {
		sc = 100
	}
	return int(math.Round(float64(wheelSize) * float64(sc) / 100.0))
}

func currentPopSize() int {
	sc := cfg.Scale
	if sc < 50 || sc > 200 {
		sc = 100
	}
	return int(math.Round(float64(popSize) * float64(sc) / 100.0))
}

func prettyEmoteLabel(slug string) string {
	switch slug {
	case "unworthy_cn":
		return "Unworthy (CN)"
	case "gg_heart":
		return "GG <3"
	}
	parts := strings.Split(slug, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func ensureEmoteChoices() []keyOption {
	if len(allEmoteChoices) > 0 {
		return allEmoteChoices
	}
	var slugs []string
	entries, err := fs.ReadDir(embeddedAssets, "assets/emotes")
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".png") {
				slugs = append(slugs, strings.TrimSuffix(e.Name(), ".png"))
			}
		}
	}
	sort.Strings(slugs)
	for _, sl := range slugs {
		lbl := prettyEmoteLabel(sl)
		if isBuiltInAnimated(sl) {
			lbl += " [Animated]"
		}
		allEmoteChoices = append(allEmoteChoices, keyOption{id: sl, label: lbl})
	}
	return allEmoteChoices
}

func getEmoteThumb(spec string) *premultImg {
	if emoteThumbs == nil {
		emoteThumbs = make(map[string]*premultImg)
	}
	if im := emoteThumbs[spec]; im != nil {
		return im
	}
	var raw []byte
	var err error
	if strings.HasPrefix(spec, "file:") {
		raw, err = os.ReadFile(strings.TrimPrefix(spec, "file:"))
	} else {
		raw, err = embeddedAssets.ReadFile("assets/emotes/" + spec + ".png")
	}
	if err != nil || len(raw) == 0 {
		return nil
	}
	im := roundIcon(decodePNG(raw, 40, 40, strings.HasPrefix(spec, "file:")))
	emoteThumbs[spec] = im
	return im
}

func buildEmoteSlot(spec, fallbackSlug string) *wheelSlot {
	if strings.HasPrefix(spec, "file:") {
		filePath := strings.TrimPrefix(spec, "file:")
		if raw, err := os.ReadFile(filePath); err == nil {
			base := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
			return &wheelSlot{
				icon:    roundIcon(decodePNG(raw, 48, 48, true)),
				popIcon: decodePNG(raw, 150, 150, true),
				label:   base,
				sound:   "assets/sounds/button.wav",
			}
		}
		spec = fallbackSlug
	}
	pngPath := "assets/emotes/" + spec + ".png"
	raw, err := embeddedAssets.ReadFile(pngPath)
	if err != nil {
		spec = fallbackSlug
		raw = mustAsset("assets/emotes/" + spec + ".png")
	}
	snd := "assets/sounds/button.wav"
	frames := ""
	if isBuiltInAnimated(spec) {
		snd = "assets/emotes/" + spec + ".wav"
		frames = "assets/emotes/" + spec + ".frames"
	}
	return &wheelSlot{
		icon:    roundIcon(decodePNG(raw, 48, 48, false)),
		popIcon: decodePNG(raw, 150, 150, false),
		label:   prettyEmoteLabel(spec),
		sound:   snd,
		frames:  frames,
	}
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
		if loaded.Scale >= 50 && loaded.Scale <= 200 {
			cfg.Scale = loaded.Scale
		}
		for i := 0; i < 9; i++ {
			if loaded.EmoteSlots[i] != "" {
				cfg.EmoteSlots[i] = loaded.EmoteSlots[i]
			}
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
	pRegSetValueExW.Call(hKey, uintptr(unsafe.Pointer(valName)), 0, regSZ, uintptr(unsafe.Pointer(&cmdUTF16[0])), uintptr(len(cmdUTF16)*2))
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

func pingVolume() float64  { return float64(cfg.Volume) / 100.0 }
func hoverVolume() float64 { return float64(cfg.Volume) / 200.0 }

func browseImageFile(owner uintptr) string {
	var buf [520]uint16
	filter := []uint16{'I', 'm', 'a', 'g', 'e', 's', ' ', '(', '*', '.', 'p', 'n', 'g', ';', '*', '.', 'j', 'p', 'g', ')', 0,
		'*', '.', 'p', 'n', 'g', ';', '*', '.', 'j', 'p', 'g', ';', '*', '.', 'j', 'p', 'e', 'g', 0, 0}
	title, _ := syscall.UTF16PtrFromString("Select Custom Emote Image (PNG / JPG)")
	ofn := openFileNameW{
		lStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		hwndOwner:   owner,
		lpstrFilter: &filter[0],
		lpstrFile:   &buf[0],
		nMaxFile:    uint32(len(buf)),
		lpstrTitle:  title,
		flags:       ofnFileMustExist | ofnPathMustExist | ofnNoChangeDir,
	}
	ok, _, _ := pGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:])
}

func fillRect(s *layeredSurface, x0, y0, w, h int, r, g, b uint8) {
	x1, y1 := min(s.width, x0+w), min(s.height, y0+h)
	x0, y0 = max(0, x0), max(0, y0)
	pix := s.pixels
	for y := y0; y < y1; y++ {
		o := (y*s.width + x0) * 4
		for x := x0; x < x1; x++ {
			pix[o+0], pix[o+1], pix[o+2], pix[o+3] = b, g, r, 255
			o += 4
		}
	}
}

func strokeRect(s *layeredSurface, x0, y0, w, h, thick int, r, g, b uint8) {
	fillRect(s, x0, y0, w, thick, r, g, b)
	fillRect(s, x0, y0+h-thick, w, thick, r, g, b)
	fillRect(s, x0, y0, thick, h, r, g, b)
	fillRect(s, x0+w-thick, y0, thick, h, r, g, b)
}

func specDisplayName(spec string) string {
	if strings.HasPrefix(spec, "file:") {
		return "Custom: " + filepath.Base(strings.TrimPrefix(spec, "file:"))
	}
	lbl := prettyEmoteLabel(spec)
	if isBuiltInAnimated(spec) {
		lbl += " [Animated]"
	}
	return lbl
}

func updateStatusLabel() {
	if lblStatusHwnd == 0 {
		return
	}
	txt := "Selected Slot: " + emoteSlotNames[selectedSlot] + "  ->  " + specDisplayName(draftEmotes[selectedSlot])
	if hoverGridIdx >= 0 && hoverGridIdx < len(allEmoteChoices) {
		txt += "   |   Hover: " + allEmoteChoices[hoverGridIdx].label
	}
	u, _ := syscall.UTF16PtrFromString(txt)
	pSetWindowTxt.Call(lblStatusHwnd, uintptr(unsafe.Pointer(u)))
}

func renderVisualPicker() {
	if settingsCanvas == nil {
		return
	}
	s := settingsCanvas
	// Dark Hextech backdrop
	fillRect(s, 0, 0, s.width, s.height, 16, 23, 33)
	strokeRect(s, 0, 0, s.width, s.height, 1, 70, 82, 98)
	// Divider between wheel preview and emote gallery
	fillRect(s, 266, 10, 1, s.height-20, 48, 60, 74)

	// 1. Left panel: Interactive 9-slot Emote Wheel
	for y := wheelCY - wheelR1 - 4; y <= wheelCY+wheelR1+4; y++ {
		dy := float64(y - wheelCY)
		for x := wheelCX - wheelR1 - 4; x <= wheelCX+wheelR1+4; x++ {
			dx := float64(x - wheelCX)
			d := math.Hypot(dx, dy)
			if d > float64(wheelR1)+2 {
				continue
			}
			if d <= float64(wheelR0) {
				if d >= float64(wheelR0)-2.5 {
					if selectedSlot == 8 {
						s.paint(x, y, 245, 208, 110, 1)
					} else {
						s.paint(x, y, 200, 170, 110, .95)
					}
				} else if selectedSlot == 8 {
					s.paint(x, y, 42, 110, 118, 1)
				} else {
					s.paint(x, y, 22, 52, 56, 1)
				}
				continue
			}
			ang := math.Mod(math.Atan2(dy, dx)*180/math.Pi+90+22.5+360, 360)
			sec := int(ang/45) % 8
			rem := math.Mod(ang, 45)
			line := math.Sin(math.Min(rem, 45-rem)*math.Pi/180) * d
			if d >= float64(wheelR1)-2 {
				if sec == selectedSlot {
					s.paint(x, y, 245, 208, 110, 1)
				} else {
					s.paint(x, y, 120, 110, 88, .9)
				}
			} else if line < 0.9 {
				s.paint(x, y, 160, 138, 92, .65)
			} else if sec == selectedSlot {
				s.paint(x, y, 92, 102, 114, 1)
			} else {
				s.paint(x, y, 46, 52, 60, 1)
			}
		}
	}
	// Draw the 8 outer emote icons + center icon
	for i := 0; i < 8; i++ {
		a := (float64(i)*45 - 90) * math.Pi / 180
		ix := int(math.Round(float64(wheelCX) + float64(wheelIR)*math.Cos(a)))
		iy := int(math.Round(float64(wheelCY) + float64(wheelIR)*math.Sin(a)))
		if im := getEmoteThumb(draftEmotes[i]); im != nil {
			s.blitPremult(im, ix, iy, 1)
		}
	}
	if im := getEmoteThumb(draftEmotes[8]); im != nil {
		s.blitPremult(im, wheelCX, wheelCY, 1)
	}

	// 2. Right panel: All 44 Emotes visual grid (8 cols x 6 rows)
	choices := ensureEmoteChoices()
	activeSpec := draftEmotes[selectedSlot]
	for idx, ch := range choices {
		col := idx % gridCols
		row := idx / gridCols
		cx := gridX + col*cellW
		cy := gridY + row*cellH
		if cy+cellH > s.height-4 {
			break
		}
		if ch.id == activeSpec {
			fillRect(s, cx, cy, cellW-4, cellH-4, 52, 74, 92)
			strokeRect(s, cx, cy, cellW-4, cellH-4, 2, 230, 195, 105)
		} else if idx == hoverGridIdx {
			fillRect(s, cx, cy, cellW-4, cellH-4, 38, 50, 66)
			strokeRect(s, cx, cy, cellW-4, cellH-4, 1, 130, 160, 190)
		} else {
			fillRect(s, cx, cy, cellW-4, cellH-4, 24, 32, 44)
		}
		if im := getEmoteThumb(ch.id); im != nil {
			s.blitPremult(im, cx+(cellW-4)/2, cy+(cellH-4)/2, 1)
		}
		// Cyan dot badge on animated emotes
		if isBuiltInAnimated(ch.id) {
			fillRect(s, cx+cellW-12, cy+4, 5, 5, 35, 215, 225)
		}
	}
	updateStatusLabel()
}

func hitTestCanvas(wx, wy int) (wheelSlotHit int, gridHit int) {
	wheelSlotHit, gridHit = -1, -1
	lx, ly := wx-canvasX, wy-canvasY
	if lx < 0 || ly < 0 || lx >= canvasW || ly >= canvasH {
		return
	}
	// Check wheel hit
	dx, dy := float64(lx-wheelCX), float64(ly-wheelCY)
	d := math.Hypot(dx, dy)
	if d <= float64(wheelR0) {
		wheelSlotHit = 8
		return
	}
	if d <= float64(wheelR1)+4 {
		ang := math.Mod(math.Atan2(dy, dx)*180/math.Pi+90+22.5+360, 360)
		wheelSlotHit = int(ang/45) % 8
		return
	}
	// Check grid hit
	gx, gy := lx-gridX, ly-gridY
	if gx >= 0 && gy >= 0 {
		col := gx / cellW
		row := gy / cellH
		if col >= 0 && col < gridCols {
			idx := row*gridCols + col
			if idx >= 0 && idx < len(allEmoteChoices) {
				gridHit = idx
			}
		}
	}
	return
}

func settingsWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmPaint:
		var ps paintStruct
		hdc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if settingsCanvas != nil && hdc != 0 {
			pBitBlt.Call(hdc, canvasX, canvasY, canvasW, canvasH, settingsCanvas.hdcMem, 0, 0, srcCopy)
		}
		pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case WM_MOUSEMOVE:
		mx := int(int16(lParam & 0xFFFF))
		my := int(int16((lParam >> 16) & 0xFFFF))
		_, gHit := hitTestCanvas(mx, my)
		if gHit != hoverGridIdx {
			hoverGridIdx = gHit
			renderVisualPicker()
			pInvalidateRect.Call(hwnd, 0, 0)
		}
		return 0
	case WM_LBUTTONDOWN:
		mx := int(int16(lParam & 0xFFFF))
		my := int(int16((lParam >> 16) & 0xFFFF))
		wHit, gHit := hitTestCanvas(mx, my)
		if wHit >= 0 {
			selectedSlot = wHit
			renderVisualPicker()
			pInvalidateRect.Call(hwnd, 0, 0)
			return 0
		}
		if gHit >= 0 && gHit < len(allEmoteChoices) {
			draftEmotes[selectedSlot] = allEmoteChoices[gHit].id
			renderVisualPicker()
			pInvalidateRect.Call(hwnd, 0, 0)
			return 0
		}
	case WM_COMMAND:
		switch uint32(wParam & 0xFFFF) {
		case idBtnBrowse:
			if fp := browseImageFile(hwnd); fp != "" {
				draftEmotes[selectedSlot] = "file:" + fp
				renderVisualPicker()
				pInvalidateRect.Call(hwnd, 0, 0)
			}
			return 0
		case idBtnResetEm:
			draftEmotes = defaultEmoteSlugs
			renderVisualPicker()
			pInvalidateRect.Call(hwnd, 0, 0)
			return 0
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
			if idx, _, _ := pSendMessageW.Call(cbScaleHwnd, cbGetCurSel, 0, 0); int(idx) >= 0 && int(idx) < len(scaleOptions) {
				cfg.Scale = scaleOptions[idx].val
			}
			chk, _, _ := pSendMessageW.Call(chkAutoStartHwnd, bmGetCheck, 0, 0)
			cfg.AutoStart = chk == bstChecked
			cfg.EmoteSlots = draftEmotes
			saveConfig()
			applyScaleChange()
			reloadEmoteSlots()
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
		if settingsCanvas != nil {
			settingsCanvas.destroy()
			settingsCanvas = nil
		}
		emoteThumbs = nil
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

	draftEmotes = cfg.EmoteSlots
	selectedSlot = 0
	hoverGridIdx = -1
	ensureEmoteChoices()

	clsName, _ := syscall.UTF16PtrFromString("lolPingLiteSettingsClass")
	if !settingsClassReg {
		wc := WNDCLASSEXW{
			CbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
			LpfnWndProc:   syscall.NewCallback(settingsWndProc),
			HInstance:     app.hInstance,
			HIcon:         app.trayIcon,
			HIconSm:       app.trayIcon,
			HbrBackground: 16,
			LpszClassName: clsName,
		}
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		settingsClassReg = true
	}

	const winW, winH = 786, 550
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
	settingsFont, _, _ = pCreateFontW.Call(uintptr(^uintptr(14)+1), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))

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

	// Row 1: Ping Hotkey + Emote Hotkey
	addCtrl(clsStatic, "Ping Hotkey:", wsChild|wsVisible, 18, 16, 95, 22, 0)
	cbPingHwnd = addCtrl(clsCombo, "", wsChild|wsVisible|wsTabStop|cbsDropDownL, 115, 12, 245, 160, 0)
	pingSel := 0
	for i, o := range pingKeyOptions {
		s, _ := syscall.UTF16PtrFromString(o.label)
		pSendMessageW.Call(cbPingHwnd, cbAddString, 0, uintptr(unsafe.Pointer(s)))
		if o.id == cfg.PingKey {
			pingSel = i
		}
	}
	pSendMessageW.Call(cbPingHwnd, cbSetCurSel, uintptr(pingSel), 0)

	addCtrl(clsStatic, "Emote Hotkey:", wsChild|wsVisible, 388, 16, 100, 22, 0)
	cbEmoteHwnd = addCtrl(clsCombo, "", wsChild|wsVisible|wsTabStop|cbsDropDownL, 492, 12, 260, 160, 0)
	emoteSel := 0
	for i, o := range emoteKeyOptions {
		s, _ := syscall.UTF16PtrFromString(o.label)
		pSendMessageW.Call(cbEmoteHwnd, cbAddString, 0, uintptr(unsafe.Pointer(s)))
		if o.id == cfg.EmoteKey {
			emoteSel = i
		}
	}
	pSendMessageW.Call(cbEmoteHwnd, cbSetCurSel, uintptr(emoteSel), 0)

	// Row 2: Volume + Scale
	addCtrl(clsStatic, "Sound Volume:", wsChild|wsVisible, 18, 50, 95, 22, 0)
	cbVolumeHwnd = addCtrl(clsCombo, "", wsChild|wsVisible|wsTabStop|cbsDropDownL, 115, 46, 245, 160, 0)
	volSel := 3
	for i, o := range volumeOptions {
		s, _ := syscall.UTF16PtrFromString(o.label)
		pSendMessageW.Call(cbVolumeHwnd, cbAddString, 0, uintptr(unsafe.Pointer(s)))
		if o.val == cfg.Volume {
			volSel = i
		}
	}
	pSendMessageW.Call(cbVolumeHwnd, cbSetCurSel, uintptr(volSel), 0)

	addCtrl(clsStatic, "UI Scale:", wsChild|wsVisible, 388, 50, 100, 22, 0)
	cbScaleHwnd = addCtrl(clsCombo, "", wsChild|wsVisible|wsTabStop|cbsDropDownL, 492, 46, 260, 160, 0)
	scaleSel := 2
	for i, o := range scaleOptions {
		s, _ := syscall.UTF16PtrFromString(o.label)
		pSendMessageW.Call(cbScaleHwnd, cbAddString, 0, uintptr(unsafe.Pointer(s)))
		if o.val == cfg.Scale {
			scaleSel = i
		}
	}
	pSendMessageW.Call(cbScaleHwnd, cbSetCurSel, uintptr(scaleSel), 0)

	// Row 3: Auto-start + hint
	chkAutoStartHwnd = addCtrl(clsBtn, "Start lolPing Lite with Windows", wsChild|wsVisible|wsTabStop|bsAutoCheck, 18, 82, 240, 22, 0)
	if cfg.AutoStart {
		pSendMessageW.Call(chkAutoStartHwnd, bmSetCheck, bstChecked, 0)
	}
	addCtrl(clsStatic, "Click a slice on the wheel (left), then click any emote icon (right). Cyan dot = Animated.", wsChild|wsVisible, 265, 84, 490, 20, 0)

	// Status label below canvas
	lblStatusHwnd = addCtrl(clsStatic, "", wsChild|wsVisible, 18, canvasY+canvasH+8, 734, 20, 0)

	// Bottom buttons
	addCtrl(clsBtn, "Browse PNG/JPG...", wsChild|wsVisible|wsTabStop|bsPushBtn, 18, 468, 140, 32, idBtnBrowse)
	addCtrl(clsBtn, "Reset Emotes", wsChild|wsVisible|wsTabStop|bsPushBtn, 168, 468, 115, 32, idBtnResetEm)
	addCtrl(clsBtn, "Test Ping", wsChild|wsVisible|wsTabStop|bsPushBtn, 415, 468, 100, 32, idBtnTest)
	addCtrl(clsBtn, "Save & Apply", wsChild|wsVisible|wsTabStop|bsDefPushBtn, 525, 468, 120, 32, idBtnSave)
	addCtrl(clsBtn, "Close", wsChild|wsVisible|wsTabStop|bsPushBtn, 655, 468, 97, 32, idBtnClose)

	settingsCanvas = newLayeredSurface(canvasW, canvasH)
	renderVisualPicker()
	pInvalidateRect.Call(settingsHwnd, 0, 0)
	pSetForegroundWindow.Call(settingsHwnd)
}
