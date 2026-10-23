package ui

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/audio"
	"github.com/medeirosvictor/hermec/cameracap"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
)

// Settings scene rows.
const (
	rowName = iota
	rowPalette
	rowFont
	rowSize
	rowScanlines
	rowCurvature
	rowInput
	rowOutput
	rowCamera
	rowShare
	rowUpdate
	rowLogs
	rowHelp
	settingsRows
)

// DefaultSettingsPath returns <user config dir>/hermec/settings.toml.
func DefaultSettingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hermec", "settings.toml"), nil
}

// presetByName finds a built-in palette by name.
func presetByName(name string) (theme.Theme, bool) {
	for _, p := range theme.Presets() {
		if p.Name == name {
			return p, true
		}
	}
	return theme.Theme{}, false
}

// presetName is the preset name of th, or "" for a custom theme file.
func presetName(th theme.Theme) string { return th.Name }

// startupTheme resolves the startup theme and scanline state. An explicit
// -theme flag wins and ignores the saved palette; otherwise the saved theme
// file, then the saved preset, then the default. Problems with saved values
// are returned as a notice and never fail startup.
func startupTheme(flagPath string, s state.Settings) (th theme.Theme, crt bool, notice string, err error) {
	if flagPath != "" {
		th, err = theme.Load(flagPath)
		if err != nil {
			return th, false, "", err
		}
		return th, scanlinesOr(s, th.Scanlines), "", nil
	}
	th = theme.Default()
	loaded := false
	if s.ThemePath != "" {
		t, lerr := theme.Load(s.ThemePath)
		if lerr != nil {
			notice = fmt.Sprintf("settings theme: %v", lerr)
		} else {
			th, loaded = t, true
		}
	}
	// A failed theme file falls back to the saved preset, then the default.
	if !loaded && s.Palette != "" {
		if p, ok := presetByName(s.Palette); ok {
			th = p
		} else {
			if notice == "" {
				notice = fmt.Sprintf("settings: unknown palette %q", s.Palette)
			}
		}
	}
	return th, scanlinesOr(s, th.Scanlines), notice, nil
}

func scanlinesOr(s state.Settings, def bool) bool {
	if s.Scanlines != nil {
		return *s.Scanlines
	}
	return def
}

// loadSettings reads the settings file into g.set. Failures become the
// notice; an unreadable file is moved aside so it is not overwritten.
func (g *game) loadSettings(path string, others ...string) {
	if path == "" {
		p, err := DefaultSettingsPath()
		if err != nil {
			g.notice = fmt.Sprintf("settings: %v", err)
			return
		}
		path = p
	}
	g.settingsPath = path
	s, err := state.LoadOrInitSettings(path, others...) // nothing on disk yet: modern first-run defaults
	if err != nil {
		g.notice = fmt.Sprintf("settings.toml unreadable (%v); moved to .bad", err)
		_ = os.Rename(path, filepath.Clean(path)+".bad")
		return
	}
	g.set = s
}

// persistSettings writes g.set. Called only from event paths, never Draw.
func (g *game) persistSettings() {
	if g.settingsPath == "" {
		return
	}
	if err := state.SaveSettings(g.settingsPath, g.set); err != nil {
		g.notice = fmt.Sprintf("save settings: %v", err)
		g.cur().st.Status = g.notice
	}
}

// setScanlines applies and persists the CRT effect state.
func (g *game) setScanlines(on bool) {
	g.crtOn = on
	g.set.Scanlines = &on
	g.logf("crt effect: %v", on)
	g.persistSettings()
}

// toggleCurvature flips and persists the CRT curvature switch.
func (g *game) toggleCurvature() {
	on := !g.set.CurvatureEnabled()
	g.set.Curvature = &on
	g.logf("crt curvature: %v", on)
	g.persistSettings()
}

// toggleMembers flips and persists whether the member list is shown.
func (g *game) toggleMembers() {
	on := !g.set.MembersShown()
	g.set.Members = &on
	g.persistSettings()
}

// cyclePalette advances to the next preset and persists its name. The saved
// custom theme file (if any) is dropped so the preset wins on next start.
func (g *game) cyclePalette() {
	// Scenes read g.th on every draw, so the swap is live. Face size does
	// not depend on the palette, so no face rebuild is needed.
	g.th = theme.NextPreset(g.th)
	g.set.Palette = presetName(g.th)
	g.set.ThemePath = ""
	g.logf("palette: bg=%v fg=%v", g.th.BG, g.th.FG)
	g.persistSettings()
}

// buildFaces makes the body, small and title faces of one font, at the scales
// used everywhere.
func buildFaces(font string, base float64, pct int) (face, faceS, faceT *text.GoTextFace, err error) {
	size := base * (float64(pct) / 100)
	if face, err = theme.Face(font, size); err != nil {
		return
	}
	if faceS, err = theme.Face(font, size*smallScale); err != nil {
		return
	}
	faceT, err = theme.Face(font, size*titleScale)
	return
}

// cycleFont advances to the next bundled font, rebuilds the faces and persists
// the choice. The scrollback layout and render caches key on the font id, so
// they refresh on the next frame even if the new advance equals the old one.
func (g *game) cycleFont() {
	next := theme.NextFont(g.set.FontID())
	face, faceS, faceT, err := buildFaces(next, g.th.FontSize, g.set.FontScalePct())
	if err != nil {
		g.notice = fmt.Sprintf("font: %v", err)
		return
	}
	g.face, g.faceS, g.faceT = face, faceS, faceT
	g.set.Font = next
	g.logf("font: %s", next)
	g.persistSettings()
}

// fontSize is the effective body text size: the theme size times the saved
// percentage. Everything sized from the font (faces, line height, cursor
// blocks, layout cache keys) goes through it.
func (g *game) fontSize() float64 {
	return g.th.FontSize * (float64(g.set.FontScalePct()) / 100)
}

// cycleSize advances the text size to the next percentage step, rebuilds the
// faces and persists it. The layout caches key on the effective size.
func (g *game) cycleSize() {
	next := state.NextFontScale(g.set.FontScale)
	face, faceS, faceT, err := buildFaces(g.set.FontID(), g.th.FontSize, next)
	if err != nil {
		g.notice = fmt.Sprintf("size: %v", err)
		return
	}
	g.face, g.faceS, g.faceT = face, faceS, faceT
	g.set.FontScale = next
	g.logf("font size: %d%%", next)
	g.persistSettings()
}

// rememberName persists a name the user chose, skipping the unedited startup
// name so a -name flag does not become sticky.
func (g *game) rememberName(name string) {
	if name == "" || name == g.set.Name || name == g.autoName {
		return
	}
	g.set.Name = name
	g.persistSettings()
}

// flushName writes a name edited in the settings field. An emptied field is
// persisted as empty, meaning "use the derived default name on next start".
func (g *game) flushName() {
	if !g.nameDirty {
		return
	}
	g.nameDirty = false
	g.set.Name = g.connect.name.String()
	g.persistSettings()
}

func (g *game) closeSettings() {
	g.flushName()
	g.helpOpen, g.helpScroll = false, 0
	if g.settingsOpen {
		g.settingsOpen = false
		g.cur().st.SetViewed(g.curKey != "") // reads the active channel again
	}
}

func (g *game) toggleSettings() {
	if g.settingsOpen {
		g.closeSettings()
		return
	}
	g.settingsOpen = true
	g.cur().st.SetViewed(false) // Settings covers the chat: nothing is being read
	g.q.open = false            // do not leave the quality panel hidden behind settings
	g.setRow = rowName
	g.refreshDevices()
}

// refreshDevices enumerates audio and camera devices. Called once on settings scene
// entry (never per frame). A failure leaves the lists empty: the rows then
// just show the saved value and cycling selects "default".
func (g *game) refreshDevices() {
	ins, outs, err := audio.ListDevices()
	if err != nil {
		g.logf("list audio devices: %v", err)
		ins, outs = nil, nil
	}
	g.devIn, g.devOut = ins, outs
	devices := g.cameraOps.devices
	if devices == nil {
		devices = cameracap.Devices
	}
	g.devCamera, err = devices()
	if err != nil {
		g.logf("list camera devices: %v", err)
		g.devCamera = nil
	}
}

// savedDevicesNotice reports, at startup, saved devices that are no longer
// present (the call itself falls back to the default; see resolveDevices).
// It enumerates only when a device is saved.
func savedDevicesNotice(s state.Settings) string {
	if s.InputDevice == "" && s.OutputDevice == "" {
		return ""
	}
	_, _, n := resolveDevices(s.InputDevice, s.OutputDevice)
	return n
}

// deviceLabel is how a device row shows saved against the available names.
func deviceLabel(saved string, available []string) string {
	if saved == "" {
		return "default"
	}
	for _, d := range available {
		if d == saved {
			return saved
		}
	}
	return saved + " (missing)"
}

func (g *game) cycleInput() {
	g.set.InputDevice = state.NextDevice(g.set.InputDevice, g.devIn)
	g.logf("input device: %q", g.set.InputDevice)
	g.persistSettings()
}

func (g *game) cycleOutput() {
	g.set.OutputDevice = state.NextDevice(g.set.OutputDevice, g.devOut)
	g.logf("output device: %q", g.set.OutputDevice)
	g.persistSettings()
}

func (g *game) cameraDeviceLabel() string {
	if !g.cameraSupported() {
		return "unsupported"
	}
	if g.set.CameraDevice == "" {
		return "default"
	}
	for _, device := range g.devCamera {
		if device.ID == g.set.CameraDevice {
			return device.Name
		}
	}
	return g.set.CameraDevice + " (missing)"
}

// settingsTailLines is the height, in lines, of what follows the rows: a blank
// line, the fingerprint, the backup line, a gap and the hint, plus one line
// kept for the notice at the bottom of the window.
const settingsTailLines = 6

// settingsWindow is the slice of settings rows that fits in a window h tall
// when the rows start at top with line height lh: rows first..first+n-1. When
// not all of total fit, the window scrolls just far enough to keep sel in view.
func settingsWindow(h, top, lh float64, total, sel int) (first, n int) {
	n = int((h-top)/lh+1e-9) - settingsTailLines
	n = max(1, min(n, total))
	if sel >= n {
		first = min(sel-n+1, total-n)
	}
	return first, n
}

// settingsRowsWindow is settingsWindow for the current window and selection.
func (g *game) settingsRowsWindow() (first, n int) {
	lh := g.lineH()
	return settingsWindow(float64(g.h), connectFieldsTop(lh), lh, settingsRows, g.setRow)
}

// addSettingsHits registers one target per visible settings row, from the same
// origin and row height drawSettings uses.
func (g *game) addSettingsHits() {
	lh := g.lineH()
	x0 := connectX + g.railW()
	if g.helpOpen {
		y := connectFieldsTop(lh)
		g.hits.Add(state.Hit{X0: x0, Y0: y, X1: float64(g.w), Y1: y + lh, Kind: state.HitHelpBack})
		return
	}
	first, n := g.settingsRowsWindow()
	for r := first; r < first+n; r++ {
		y := connectFieldsTop(lh) + float64(r-first)*lh
		g.hits.Add(state.Hit{X0: x0, Y0: y, X1: float64(g.w), Y1: y + lh, Kind: state.HitSettingsRow, ID: r})
	}
	g.addFingerprintHit(connectFieldsTop(lh) + float64(n+1)*lh)
}

// updateSettings handles input for the settings scene. Every change saves.
func (g *game) updateSettings() {
	if g.escPressed() {
		g.escSpent = true
		if g.helpOpen {
			g.helpBack()
		} else {
			g.closeSettings()
		}
		return
	}
	if g.helpOpen {
		g.updateHelp()
		return
	}
	prev := g.setRow
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) || inpututil.IsKeyJustPressed(ebiten.KeyArrowDown) {
		g.setRow = (g.setRow + 1) % settingsRows
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowUp) {
		g.setRow = (g.setRow + settingsRows - 1) % settingsRows
	}
	if _, wy := ebiten.Wheel(); wy != 0 { // the wheel moves the selection, so the window follows
		if wy > 0 {
			g.setRow = max(g.setRow-1, 0)
		} else {
			g.setRow = min(g.setRow+1, settingsRows-1)
		}
	}
	if g.setRow != prev {
		g.flushName()
	}
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		if h, ok := g.hitAt(); ok && h.Kind == state.HitSettingsRow {
			r := h.ID
			g.setRow = r
			if r != rowName {
				g.flushName()
			}
			g.activateSetting(r)
		}
	}
	switch g.setRow {
	case rowName:
		f := &g.connect.name
		if f.edit(readEditKeys(), ebiten.AppendInputChars(nil), maxFieldRunes, nil, false) {
			g.nameDirty = true
		}
	default:
		if enterPressed() {
			g.activateSetting(g.setRow)
		}
	}
}

func (g *game) activateSetting(row int) {
	switch row {
	case rowPalette:
		g.cyclePalette()
	case rowFont:
		g.cycleFont()
	case rowCurvature:
		g.toggleCurvature()
	case rowScanlines:
		g.setScanlines(!g.crtOn)
	case rowInput:
		g.cycleInput()
	case rowOutput:
		g.cycleOutput()
	case rowCamera:
		g.cycleCameraDevice()
	case rowSize:
		g.cycleSize()
	case rowShare:
		g.set.ShareQuality = state.NextShareQuality(g.set.ShareQuality)
		g.logf("share quality: %s", g.set.ShareQuality)
		g.persistSettings()
		if g.share.on { // the running stream keeps its profile
			g.cur().st.Status = "applies to your next share"
		}
	case rowUpdate:
		g.activateUpdateRow()
	case rowLogs:
		g.openLogFolder()
	case rowHelp:
		g.openHelp()
	}
}

func (g *game) drawSettings(screen *ebiten.Image) {
	th := g.th
	lh := g.lineH()
	g.drawContentPanel(screen, 0)
	if g.helpOpen {
		g.drawHelp(screen)
		return
	}
	x, y := connectX+g.railW(), connectY

	g.drawTextF(screen, g.faceT, "SETTINGS", x, y, th.Bright)
	y += lh * 2.5

	pal := presetName(th)
	if pal == "" {
		pal = "custom"
	}
	scan := "off"
	if g.crtOn {
		scan = "on"
	}
	curv := "off"
	if g.set.CurvatureEnabled() {
		curv = "on"
	}
	nameVal := g.connect.name.String()
	rows := [settingsRows]string{
		"name:      " + nameVal,
		"palette:   " + pal,
		"font:      " + theme.FontLabel(g.set.FontID()),
		"size:      " + strconv.Itoa(g.set.FontScalePct()) + "%",
		"scanlines: " + scan,
		"curvature: " + curv,
		"input:     " + deviceLabel(g.set.InputDevice, g.devIn),
		"output:    " + deviceLabel(g.set.OutputDevice, g.devOut),
		"camera:    " + g.cameraDeviceLabel(),
		"share quality: " + g.set.ShareQualityID(),
		updateRowLabel(g.updStatus, g.update, g.version()),
		"logs:      open folder ›",
		"help:      how hermec works ›",
	}
	first, n := g.settingsRowsWindow()
	for i := first; i < first+n; i++ {
		s := rows[i]
		marker := "  "
		if i == g.setRow {
			marker = "> "
		}
		g.drawText(screen, marker+s, x, y, th.FG)
		if i == g.setRow && i == rowName {
			nm := &g.connect.name
			g.drawCaret(screen, x, y, marker+"name:      "+string(nm.runes[:nm.caret()]))
		}
		y += lh
	}
	y += lh
	g.drawText(screen, "fingerprint: "+g.fp, x, y, th.FG)
	y += lh
	g.drawTextF(screen, g.faceS, "back up "+g.keyAt, x, y, th.Dim)
	y += lh * 2
	g.drawTextF(screen, g.faceS, "Tab/Up/Down move   Enter or click toggles   Esc back", x, y, th.Dim)
	if g.notice != "" {
		g.drawTextF(screen, g.faceS, g.notice, x, float64(g.h)-lh, th.Bright)
	}
}

// drawGear strokes a small gear centered at (cx, cy).
func drawGear(dst *ebiten.Image, cx, cy, r float32, col color.RGBA) {
	vector.StrokeCircle(dst, cx, cy, r*0.6, 2, col, false)
	vector.StrokeCircle(dst, cx, cy, r*0.22, 1, col, false)
	for i := 0; i < 8; i++ {
		a := float64(i) * math.Pi / 4
		s, co := float32(math.Sin(a)), float32(math.Cos(a))
		vector.StrokeLine(dst, cx+co*r*0.6, cy+s*r*0.6, cx+co*r, cy+s*r, 3, col, false)
	}
}
