package ui

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
)

// Settings scene rows.
const (
	rowName = iota
	rowPalette
	rowScanlines
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
	ps := theme.Presets()
	for i, n := range theme.PresetNames() {
		if n == name && i < len(ps) {
			return ps[i], true
		}
	}
	return theme.Theme{}, false
}

// presetName is the name of the preset whose palette matches th, or "" for a
// custom palette.
func presetName(th theme.Theme) string {
	ps := theme.Presets()
	for i, n := range theme.PresetNames() {
		if i < len(ps) && ps[i].BG == th.BG && ps[i].FG == th.FG && ps[i].Dim == th.Dim && ps[i].Bright == th.Bright {
			return n
		}
	}
	return ""
}

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
	if s.ThemePath != "" {
		t, lerr := theme.Load(s.ThemePath)
		if lerr != nil {
			notice = fmt.Sprintf("settings theme: %v", lerr)
		} else {
			th = t
		}
	} else if s.Palette != "" {
		if p, ok := presetByName(s.Palette); ok {
			th = p
		} else {
			notice = fmt.Sprintf("settings: unknown palette %q", s.Palette)
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
func (g *game) loadSettings(path string) {
	if path == "" {
		p, err := DefaultSettingsPath()
		if err != nil {
			g.notice = fmt.Sprintf("settings: %v", err)
			return
		}
		path = p
	}
	g.settingsPath = path
	s, err := state.LoadSettings(path)
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
		g.st.Status = g.notice
	}
}

// setScanlines applies and persists the CRT effect state.
func (g *game) setScanlines(on bool) {
	g.crtOn = on
	g.set.Scanlines = &on
	g.logf("crt effect: %v", on)
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

// rememberName persists a name the user chose, skipping the unedited startup
// name so a -name flag does not become sticky.
func (g *game) rememberName(name string) {
	if name == "" || name == g.set.Name || (name == g.autoName && g.set.Name == "") {
		return
	}
	g.set.Name = name
	g.persistSettings()
}

func (g *game) toggleSettings() {
	g.settingsOpen = !g.settingsOpen
	g.setRow = rowName
}

// updateSettings handles input for the settings scene. Every change saves.
func (g *game) updateSettings() {
	if inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.settingsOpen = false
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) || inpututil.IsKeyJustPressed(ebiten.KeyArrowDown) {
		g.setRow = (g.setRow + 1) % settingsRows
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyArrowUp) {
		g.setRow = (g.setRow + settingsRows - 1) % settingsRows
	}
	lh := g.lineH()
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		cx, cy := ebiten.CursorPosition()
		if state.InRect(float64(cx), float64(cy), connectX+g.railW(), 0, float64(g.w), 1e9) {
			if r := state.RowAt(float64(cy), connectFieldsTop(lh), lh, settingsRows); r >= 0 {
				g.setRow = r
				g.activateSetting(r)
			}
		}
	}
	switch g.setRow {
	case rowName:
		f := &g.connect.name
		changed := false
		for _, r := range ebiten.AppendInputChars(nil) {
			if r >= ' ' && r != 0x7f && len(f.runes) < maxFieldRunes {
				f.runes = append(f.runes, r)
				changed = true
			}
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) {
			f.backspace()
			changed = true
		} else if d := inpututil.KeyPressDuration(ebiten.KeyBackspace); d > 30 && d%3 == 0 {
			f.backspace()
			changed = true
		}
		if changed {
			g.rememberName(f.String())
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
	case rowScanlines:
		g.setScanlines(!g.crtOn)
	}
}

func (g *game) drawSettings(screen *ebiten.Image) {
	th := g.th
	lh := g.lineH()
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
	cursor := ""
	if (g.frame/30)%2 == 0 {
		cursor = "_"
	}
	nameVal := g.connect.name.String()
	if g.setRow == rowName {
		nameVal += cursor
	}
	rows := [settingsRows]string{
		"name:      " + nameVal,
		"palette:   " + pal,
		"scanlines: " + scan,
	}
	for i, s := range rows {
		marker := "  "
		if i == g.setRow {
			marker = "> "
		}
		g.drawText(screen, marker+s, x, y, th.FG)
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
