package ui

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"github.com/medeirosvictor/hermec/ui/state"
)

const maxFieldRunes = 128

// Connect scene layout origin; the title takes two line heights, then the
// name and server rows follow.
const connectX, connectY = 48.0, 48.0

func connectFieldsTop(lh float64) float64 { return connectY + 2*lh }

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
}

func newConnectForm(name, url string, local bool) connectForm {
	return connectForm{
		name:   field{runes: []rune(name)},
		server: field{runes: []rune(url)},
		local:  local,
	}
}

// update consumes keyboard input. It reports submit when Enter is pressed
// and no dial is in flight.
func (f *connectForm) update(dialing bool, lh float64, screenW int) (url, name string, submit bool) {
	if inpututil.IsKeyJustPressed(ebiten.KeyTab) && !f.local {
		f.focus = 1 - f.focus
	}
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		cx, cy := ebiten.CursorPosition()
		if state.InRect(float64(cx), float64(cy), connectX, 0, float64(screenW), 1e9) {
			switch state.RowAt(float64(cy), connectFieldsTop(lh), lh, 2) {
			case 0:
				f.focus = 0
			case 1:
				if !f.local {
					f.focus = 1
				}
			}
		}
	}
	cur := &f.name
	if f.focus == 1 && !f.local {
		cur = &f.server
	}
	if !dialing {
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
	x, y := connectX, connectY

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

	g.drawText(screen, "fingerprint: "+g.fp, x, y, th.FG)
	y += lh
	g.drawTextF(screen, g.faceS, "back up "+g.keyAt, x, y, th.Dim)
	y += lh * 2

	switch {
	case g.dialing:
		g.drawText(screen, "connecting...", x, y, th.FG)
	case g.st.ConnectErr != "":
		g.drawText(screen, "error: "+g.st.ConnectErr, x, y, th.Bright)
		y += lh
		g.drawText(screen, "Enter to retry", x, y, th.Dim)
	default:
		hint := "Enter to connect"
		if !f.local {
			hint += "   Tab to switch field"
		}
		g.drawTextF(screen, g.faceS, hint, x, y, th.Dim)
	}
}
