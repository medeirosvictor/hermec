package ui

import (
	"fmt"
	"image/color"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"github.com/medeirosvictor/hermec/ui/state"
)

const maxFieldRunes = 128

// Connect scene layout origin; the title takes two line heights, then the
// name and server rows follow.
const connectX, connectY = 48.0, 48.0

func connectFieldsTop(lh float64) float64 {
	// Must match drawConnect's title advance (y += lh * 2.5)
	return connectY + 2.5*lh
}

type field struct {
	runes []rune
}

func (f *field) String() string { return string(f.runes) }

func (f *field) backspace() {
	if len(f.runes) > 0 {
		f.runes = f.runes[:len(f.runes)-1]
	}
}

// connectForm is the input state of the connect scene.
type connectForm struct {
	name, server field
	focus        int // 0 name, 1 server
	local        bool
	dy           float64 // banner shift applied to the whole layout

	disc      []state.DiscoveredRow // discovered servers, empty = section hidden
	sel       int                   // selected discovered row, -1 = fields have focus
	hintUntil time.Time             // "enter a name first" shown until then
	refresh   bool                  // set by update when the user asked for a re-probe
}

// discTop is the y of the first discovered row (below the DISCOVERED header).
// It must match drawConnect's layout.
func (f *connectForm) discTop(lh float64) float64 {
	y := connectFieldsTop(lh) + f.dy + 2*lh
	if f.local {
		y += lh
	}
	return y + lh + lh // blank line, then the header
}

func newConnectForm(name, url string, local bool) connectForm {
	return connectForm{
		name:   field{runes: []rune(name)},
		server: field{runes: []rune(url)},
		local:  local,
		sel:    -1,
	}
}

// addHits registers the name/server rows (text cursor) and the discovered
// rows. Positions come from connectFieldsTop and discTop, the same sources the
// drawing uses; f.dy must already hold the banner shift.
func (f *connectForm) addHits(h *state.Hits, lh, x0, x1 float64) {
	top := connectFieldsTop(lh) + f.dy
	for i := 0; i < 2; i++ {
		y := top + float64(i)*lh
		h.Add(state.Hit{X0: x0, Y0: y, X1: x1, Y1: y + lh, Kind: state.HitText, ID: i})
	}
	for i := range f.disc {
		y := f.discTop(lh) + float64(i)*lh
		h.Add(state.Hit{X0: x0, Y0: y, X1: x1, Y1: y + lh, Kind: state.HitDiscovered, ID: i})
	}
}

func (g *game) addConnectHits() {
	g.connect.addHits(&g.hits, g.lineH(), connectX+g.railW(), float64(g.w))
}

// click applies a left click on target h. done means update should return
// the results immediately (a discovered row was clicked).
func (f *connectForm) click(h state.Hit, dialing bool) (url, name string, submit, done bool) {
	switch h.Kind {
	case state.HitDiscovered:
		i := h.ID
		if i < 0 || i >= len(f.disc) {
			return "", "", false, true // stale target: the list changed since the registry was built
		}
		f.server.runes = []rune(f.disc[i].URL)
		if n := f.name.String(); n != "" && !dialing {
			f.sel = i
			return f.disc[i].URL, n, true, true // one click = join
		}
		if !dialing {
			f.sel = -1
			f.focus = 0
			f.hintUntil = time.Now().Add(2 * time.Second)
		}
		return "", "", false, true
	case state.HitText:
		switch h.ID {
		case 0:
			f.sel = -1 // back to typing
			f.focus = 0
		case 1:
			f.sel = -1
			if !f.local {
				f.focus = 1
			}
		}
	}
	return "", "", false, false
}

// update consumes keyboard input. It reports submit when Enter is pressed
// and no dial is in flight. at returns the click target under the mouse.
func (f *connectForm) update(dialing bool, at func() (state.Hit, bool)) (url, name string, submit bool) {
	f.refresh = false
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
		f.sel = -1
		if !f.local {
			f.focus = 1 - f.focus
		}
	}
	if len(f.disc) > 0 {
		if inpututil.IsKeyJustPressed(ebiten.KeyArrowDown) {
			f.sel = state.MoveSel(f.sel, 1, len(f.disc))
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyArrowUp) {
			f.sel = state.MoveSel(f.sel, -1, len(f.disc))
		}
	} else {
		f.sel = -1
	}
	ctrl := ebiten.IsKeyPressed(ebiten.KeyControl)
	if inpututil.IsKeyJustPressed(ebiten.KeyR) && (ctrl || f.sel >= 0) {
		f.refresh = true // plain R is a letter while a field has focus
	}
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		if h, ok := at(); ok {
			if url, name, submit, done := f.click(h, dialing); done {
				return url, name, submit
			}
		}
	}
	cur := &f.name
	if f.focus == 1 && !f.local {
		cur = &f.server
	}
	if !dialing && f.sel < 0 {
		for _, r := range ebiten.AppendInputChars(nil) {
			if r >= ' ' && r != 0x7f && len(cur.runes) < maxFieldRunes {
				cur.runes = append(cur.runes, r)
			}
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) {
			cur.backspace()
		} else if d := inpututil.KeyPressDuration(ebiten.KeyBackspace); d > 30 && d%3 == 0 {
			cur.backspace()
		}
	}
	if !dialing && (inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter)) {
		if f.sel >= 0 && f.sel < len(f.disc) {
			f.server.runes = []rune(f.disc[f.sel].URL)
		}
		if n := f.name.String(); n != "" && f.server.String() != "" {
			return f.server.String(), n, true
		}
	}
	return "", "", false
}

func (g *game) lineH() float64 { return g.th.FontSize * 1.6 }

func (g *game) drawText(dst *ebiten.Image, s string, x, y float64, c color.RGBA) {
	g.drawTextF(dst, g.face, s, x, y, c)
}

func (g *game) drawTextF(dst *ebiten.Image, f *text.GoTextFace, s string, x, y float64, c color.RGBA) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(c)
	text.Draw(dst, s, f, op)
}

func (g *game) drawConnect(screen *ebiten.Image) {
	th, f := g.th, &g.connect
	lh := g.lineH()
	g.drawContentPanel(screen, g.bannerH())
	x, y := connectX+g.railW(), connectY+g.bannerH()

	g.drawTextF(screen, g.faceT, "HERMEC", x, y, th.Bright)
	y += lh * 2.5

	cursor := ""
	if (g.frame/30)%2 == 0 {
		cursor = "_"
	}
	row := func(label string, fl *field, active, disabled bool) {
		col := th.FG
		if disabled {
			col = th.Dim
		}
		val := fl.String()
		if active && !g.dialing {
			val += cursor
		}
		marker := "  "
		if active {
			marker = "> "
		}
		g.drawText(screen, marker+label+val, x, y, col)
		y += lh
	}
	row("name:   ", &f.name, f.focus == 0, false)
	row("server: ", &f.server, f.focus == 1 && !f.local, f.local)
	if f.local {
		g.drawText(screen, "  (local embedded server)", x, y, th.Dim)
		y += lh
	}
	y += lh

	if len(f.disc) > 0 {
		y = f.discTop(lh) - lh // single layout source, shared with hit-testing
		g.drawTextF(screen, g.faceS, "DISCOVERED", x, y, th.Dim)
		y += lh
		hover := -1
		if h, ok := g.hitAt(); ok && h.Kind == state.HitDiscovered {
			hover = h.ID
		}
		for i, r := range f.disc {
			col, marker := th.FG, "  "
			if i == f.sel || i == hover {
				col = th.Bright
			}
			if i == f.sel {
				marker = "> "
			}
			g.drawText(screen, fmt.Sprintf("%s%s  (%s)  %s", marker, r.Name, r.Source, r.Addr), x, y, col)
			y += lh
		}
		y += lh
	}

	g.drawText(screen, "fingerprint: "+g.fp, x, y, th.FG)
	y += lh
	g.drawTextF(screen, g.faceS, "back up "+g.keyAt, x, y, th.Dim)
	y += lh * 2

	if g.notice != "" {
		g.drawTextF(screen, g.faceS, g.notice, x, float64(g.h)-lh, th.Bright)
	}
	switch {
	case g.dialing:
		g.drawText(screen, "connecting...", x, y, th.FG)
	case g.st.ConnectErr != "":
		g.drawText(screen, "error: "+g.st.ConnectErr, x, y, th.Bright)
		y += lh
		g.drawText(screen, "Enter to retry", x, y, th.Dim)
	default:
		hint := "Enter to connect"
		if time.Now().Before(f.hintUntil) {
			hint = "enter a name first"
		}
		if !f.local {
			hint += "   Tab to switch field"
		}
		if len(f.disc) > 0 {
			hint += "   Up/Down pick discovered   Ctrl+R refresh"
		}
		g.drawTextF(screen, g.faceS, hint, x, y, th.Dim)
	}
}
