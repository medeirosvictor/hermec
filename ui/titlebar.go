package ui

import (
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/ui/icon"
	"github.com/medeirosvictor/hermec/ui/state"
)

const (
	titleBarH = state.TitleBarH // height of the custom title bar
	titleEdge = state.TitleEdge // resize band width
	titleIcon = 16              // icon square, px
)

// Fixed title-bar colours (not themed, like Discord's app bar).
var (
	barBG       = color.RGBA{0x1E, 0x1F, 0x22, 0xFF}
	barText     = color.RGBA{0xDB, 0xDE, 0xE1, 0xFF}
	barHover    = color.RGBA{0x2E, 0x30, 0x35, 0xFF}
	barCloseHot = color.RGBA{0xDA, 0x37, 0x3C, 0xFF}
)

// barH is the title bar height, 0 where the OS frame is used.
func (g *game) barH() float64 {
	if !hasCustomBar() {
		return 0
	}
	return titleBarH
}

// shapeForZone is the cursor shape over a title-bar zone.
func shapeForZone(z state.Zone) ebiten.CursorShapeType {
	switch z {
	case state.ZoneLeft, state.ZoneRight:
		return ebiten.CursorShapeEWResize
	case state.ZoneTop, state.ZoneBottom:
		return ebiten.CursorShapeNSResize
	case state.ZoneTopLeft, state.ZoneBottomRight:
		return ebiten.CursorShapeNWSEResize
	case state.ZoneTopRight, state.ZoneBottomLeft:
		return ebiten.CursorShapeNESWResize
	case state.ZoneMin, state.ZoneMax, state.ZoneClose:
		return ebiten.CursorShapePointer
	}
	return ebiten.CursorShapeDefault
}

// rawCursor is the mouse position in screen space (pixel centre).
func rawCursor() (float64, float64) {
	x, y := ebiten.CursorPosition()
	return float64(x) + 0.5, float64(y) + 0.5
}

// updateTitleBar tracks the zone under the mouse and acts on a left press on a
// caption button. Caption drag, snap, resize and double-click maximize are
// native: the window's WM_NCHITTEST (winbar_windows.go) answers HTCAPTION and
// the edge codes, so those presses never reach Ebiten. It reports true when
// the app should quit.
func (g *game) updateTitleBar() (quit bool) {
	if !hasCustomBar() {
		return false
	}
	g.maxed = ebiten.IsWindowMaximized()
	if z, ok := ncZone(); ok {
		g.tzone = z // set by the hit test, which also sees non-client moves
	} else {
		x, y := rawCursor()
		g.tzone = state.TitleZone(x, y, float64(g.sw), float64(g.sh), g.barH(), titleEdge, g.maxed)
	}
	if !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return false
	}
	switch g.tzone {
	case state.ZoneClose:
		return true
	case state.ZoneMin:
		ebiten.MinimizeWindow()
	case state.ZoneMax:
		g.toggleMaximize()
	}
	return false
}

func (g *game) toggleMaximize() {
	if ebiten.IsWindowMaximized() {
		ebiten.RestoreWindow()
	} else {
		ebiten.MaximizeWindow()
	}
}

// serverTitle is the label the rail tile shows for the server saved under
// key (its label, else its host), or "" when there is none.
func serverTitle(servers []state.ServerEntry, key string) string {
	for _, e := range servers {
		if e.URL == key {
			if e.Label != "" {
				return e.Label
			}
			return state.HostPort(e.URL)
		}
	}
	return ""
}

// barTitle is the caption text: the app name, plus the server when connected.
func (g *game) barTitle() string {
	s := "HERMEC"
	if g.st.Phase == state.PhaseMain {
		if n := serverTitle(g.servers, g.curKey); n != "" {
			s += " — " + n
		}
	}
	return s
}

// drawTitleBar paints the flat bar over the top of the screen.
func (g *game) drawTitleBar(screen *ebiten.Image) {
	if !hasCustomBar() {
		return
	}
	w := float32(g.sw)
	bh := float32(titleBarH)
	vector.FillRect(screen, 0, 0, w, bh, barBG, false)

	f := g.faceS
	title := g.barTitle()
	tw, th := text.Measure(title, f, 0)
	groupW := titleIcon + 8 + tw
	x := (float64(g.sw) - groupW) / 2
	if min := 12.0; x < min {
		x = min
	}
	if g.barIcon == nil {
		g.barIcon = ebiten.NewImageFromImage(icon.Render(titleIcon))
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(math.Round(x), math.Round((float64(bh)-titleIcon)/2))
	screen.DrawImage(g.barIcon, op)
	g.drawTextF(screen, f, title, x+titleIcon+8, (titleBarH-th)/2, barText)

	// Caption buttons: minimise, maximise/restore, close.
	for i, z := range []state.Zone{state.ZoneMin, state.ZoneMax, state.ZoneClose} {
		x0 := w - float32(3-i)*state.TitleBtnW
		if g.tzone == z {
			c := barHover
			if z == state.ZoneClose {
				c = barCloseHot
			}
			vector.FillRect(screen, x0, 0, state.TitleBtnW, bh, c, false)
		}
		g.drawBarGlyph(screen, z, x0+state.TitleBtnW/2, bh/2)
	}
}

func (g *game) drawBarGlyph(screen *ebiten.Image, z state.Zone, cx, cy float32) {
	c := barText
	switch z {
	case state.ZoneMin:
		vector.StrokeLine(screen, cx-5, cy+0.5, cx+5, cy+0.5, 1, c, false)
	case state.ZoneMax:
		if g.maxed {
			vector.StrokeRect(screen, cx-5, cy-2.5, 7, 7, 1, c, false)
			vector.StrokeLine(screen, cx-2, cy-2.5, cx-2, cy-5, 1, c, false)
			vector.StrokeLine(screen, cx-2, cy-4.5, cx+5, cy-4.5, 1, c, false)
			vector.StrokeLine(screen, cx+4.5, cy-5, cx+4.5, cy+2, 1, c, false)
			vector.StrokeLine(screen, cx+2, cy+1.5, cx+5, cy+1.5, 1, c, false)
		} else {
			vector.StrokeRect(screen, cx-5, cy-5, 10, 10, 1, c, false)
		}
	case state.ZoneClose:
		vector.StrokeLine(screen, cx-5, cy-5, cx+5, cy+5, 1, c, true)
		vector.StrokeLine(screen, cx-5, cy+5, cx+5, cy-5, 1, c, true)
	}
}
