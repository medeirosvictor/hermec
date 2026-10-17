package ui

import (
	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/state"
)

// fpTipID is the hover id of the fingerprint line, beyond any rail index.
const fpTipID = 1 << 20

// fingerprintTip explains the fingerprint on hover.
const fingerprintTip = "Your fingerprint is a short code made from your identity key. It stays the same even if you change your name, so friends and servers can tell it's really you. To be sure, compare it with a friend out loud."

// fpTipCols is the widest the fingerprint tooltip gets, in columns.
const fpTipCols = 40

// tooltipLines wraps s to cols runes per line (the font is monospace).
func tooltipLines(s string, cols int) []string { return state.Wrap(s, cols) }

// addFingerprintHit registers the "fingerprint: <fp>" line drawn at y by the
// connect and settings scenes, as wide as its text.
func (g *game) addFingerprintHit(y float64) {
	if g.fp == "" {
		return
	}
	x0 := connectX + g.railW()
	w := g.glyphW() * float64(len([]rune("fingerprint: "+g.fp)))
	g.hits.Add(state.Hit{X0: x0, Y0: y, X1: x0 + w, Y1: y + g.lineH(), Kind: state.HitFingerprint})
}

// drawFingerprintTip draws the wrapped fingerprint tooltip beside the line.
func (g *game) drawFingerprintTip(screen *ebiten.Image, s string) {
	adv, lh := g.glyphW(), g.lineH()
	cols := max(10, min(fpTipCols, int((float64(g.w)-2*pad)/adv)-2))
	lines := tooltipLines(s, cols)
	w, h := float64(cols)*adv+pad, float64(len(lines))*lh+pad/2
	a := g.tip.anchor
	x, y := state.TooltipOrigin(a.X1, a.Y0, a.Y1, w, h, float64(g.w), float64(g.h))
	g.drawPanelFade(screen, x, y, x+w, y+h, g.th.Panel, 4)
	for i, l := range lines {
		g.drawText(screen, l, x+pad/2, y+pad/4+float64(i)*lh, g.th.Bright)
	}
}
