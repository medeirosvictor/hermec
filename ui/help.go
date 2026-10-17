package ui

import (
	_ "embed"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"

	"github.com/medeirosvictor/hermec/ui/state"
)

// helpSrc is the text of the settings help view. Lines starting with "# " are
// headings; a blank line is a gap; every other line is one paragraph line.
//
//go:embed help.txt
var helpSrc string

// helpLine is one line of the help view: a heading, text, or (empty) a gap.
type helpLine struct {
	Text    string
	Heading bool
}

// parseHelp splits src into help lines.
func parseHelp(src string) []helpLine {
	var out []helpLine
	for _, l := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		l = strings.TrimRight(l, " \t")
		if t, ok := strings.CutPrefix(l, "# "); ok {
			out = append(out, helpLine{Text: t, Heading: true})
		} else {
			out = append(out, helpLine{Text: l})
		}
	}
	// A trailing newline leaves one empty element; drop it.
	for len(out) > 0 && out[len(out)-1].Text == "" && !out[len(out)-1].Heading {
		out = out[:len(out)-1]
	}
	return out
}

// wrapHelp wraps every line to cols runes (the font is monospace), keeping
// gaps and the heading flag.
func wrapHelp(lines []helpLine, cols int) []helpLine {
	var out []helpLine
	for _, l := range lines {
		if l.Text == "" {
			out = append(out, l)
			continue
		}
		for _, w := range state.Wrap(l.Text, cols) {
			out = append(out, helpLine{Text: w, Heading: l.Heading})
		}
	}
	return out
}

var helpParsed = parseHelp(helpSrc)

// helpTop is the y of the first help text line; the back row sits above it.
func helpTop(lh float64) float64 { return connectFieldsTop(lh) + 2*lh }

// helpView is the help text's width in columns and how many lines fit.
func (g *game) helpView() (cols, visible int) {
	lh := g.lineH()
	cols = int((float64(g.w) - (connectX + g.railW()) - connectX) / g.glyphW())
	visible = int((float64(g.h) - helpTop(lh) - 2*lh) / lh)
	return max(cols, 10), max(visible, 1)
}

func (g *game) helpWrapped() []helpLine {
	cols, _ := g.helpView()
	return wrapHelp(helpParsed, cols)
}

// helpMax is the largest scroll offset: the wrapped lines minus the window.
func (g *game) helpMax() int {
	_, visible := g.helpView()
	return max(0, len(g.helpWrapped())-visible)
}

func (g *game) openHelp() {
	g.helpOpen, g.helpScroll, g.helpCarry = true, 0, 0
}

// helpBack returns from the help view to the settings rows.
func (g *game) helpBack() {
	g.helpOpen, g.helpScroll, g.helpCarry = false, 0, 0
}

// scrollHelp moves the view by delta lines (positive is down), clamped to the
// text.
func (g *game) scrollHelp(delta int) {
	g.helpScroll = min(max(g.helpScroll+delta, 0), g.helpMax())
}

// heldKey is true on the press and then repeats while the key is held.
func heldKey(k ebiten.Key) bool {
	if inpututil.IsKeyJustPressed(k) {
		return true
	}
	d := inpututil.KeyPressDuration(k)
	return d > 30 && d%3 == 0
}

// updateHelp handles input while the help view is open (Esc is handled by
// updateSettings).
func (g *game) updateHelp() {
	_, visible := g.helpView()
	switch {
	case heldKey(ebiten.KeyArrowDown):
		g.scrollHelp(1)
	case heldKey(ebiten.KeyArrowUp):
		g.scrollHelp(-1)
	case heldKey(ebiten.KeyPageDown):
		g.scrollHelp(visible - 1)
	case heldKey(ebiten.KeyPageUp):
		g.scrollHelp(-(visible - 1))
	case inpututil.IsKeyJustPressed(ebiten.KeyHome):
		g.scrollHelp(-g.helpScroll)
	case inpututil.IsKeyJustPressed(ebiten.KeyEnd):
		g.scrollHelp(g.helpMax())
	}
	if _, wy := ebiten.Wheel(); wy != 0 {
		var n int
		n, g.helpCarry = state.WheelLines(g.helpCarry, -wy, wheelLinesPerNotch)
		g.scrollHelp(n)
	}
	back := enterPressed()
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		if h, ok := g.hitAt(); ok && h.Kind == state.HitHelpBack {
			back = true
		}
	}
	if back {
		g.helpBack()
	}
}

func (g *game) drawHelp(screen *ebiten.Image) {
	th, lh := g.th, g.lineH()
	x := connectX + g.railW()
	g.drawTextF(screen, g.faceT, "HELP", x, connectY, th.Bright)
	g.drawText(screen, "> ‹ back", x, connectFieldsTop(lh), th.FG)
	_, visible := g.helpView()
	lines := g.helpWrapped()
	start := min(max(g.helpScroll, 0), max(0, len(lines)-visible))
	y := helpTop(lh)
	for _, l := range lines[start:min(len(lines), start+visible)] {
		col := th.FG
		if l.Heading {
			col = th.Bright
		}
		g.drawText(screen, l.Text, x, y, col)
		y += lh
	}
	g.drawTextF(screen, g.faceS, "Up/Down PgUp/PgDn wheel scroll   Enter, click back or Esc to return", x, float64(g.h)-lh, th.Dim)
}
