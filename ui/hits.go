package ui

import (
	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/state"
)

// shapeAt is the cursor shape for a point: text over input areas, the hand
// over any other click target, the arrow elsewhere.
func shapeAt(h *state.Hits, x, y float64) ebiten.CursorShapeType {
	t, ok := h.At(x, y)
	switch {
	case !ok:
		return ebiten.CursorShapeDefault
	case t.Kind == state.HitText:
		return ebiten.CursorShapeText
	default:
		return ebiten.CursorShapePointer
	}
}

// hitAt is the click target under the (barrel-corrected) mouse.
func (g *game) hitAt() (state.Hit, bool) { return g.hitFor(g.cursor()) }

// hitFor is the scene click target at a scene point. Title-bar zones (caption,
// buttons, resize edges) win: while the mouse is over one, no scene target is
// reachable.
func (g *game) hitFor(x, y float64) (state.Hit, bool) {
	if g.tzone != state.ZoneNone {
		return state.Hit{}, false
	}
	return g.hits.At(x, y)
}

// updateCursorShape sets the shape for the title-bar zone under the mouse, or
// else for the scene hit under it, calling Ebiten only when the shape changes.
func (g *game) updateCursorShape() {
	var s ebiten.CursorShapeType
	if g.tzone != state.ZoneNone {
		s = shapeForZone(g.tzone)
	} else {
		x, y := g.cursor()
		s = shapeAt(&g.hits, x, y)
	}
	if s != g.shape {
		g.shape = s
		ebiten.SetCursorShape(s)
	}
}

// buildHits rebuilds the click-target registry for the active scene. Click
// handlers and the cursor shape both read it, and every rect comes from the
// same geometry the drawing code uses.
func (g *game) buildHits() {
	g.hits.Reset()
	defer g.addDialogHits() // very last: an open dialog wins over everything, menu included
	defer g.addMenuHits()   // runs before the dialog hits: the open menu wins over the scene
	g.addRailHits()
	switch {
	case g.settingsOpen:
		g.addSettingsHits()
		return
	case g.cur().st.Phase == state.PhaseConnect:
		g.connect.dy = g.bannerH()
		g.addConnectHits()
		g.addBarHits(g.geom()) // the call bar of a call on any server
	case g.cur().st.Phase == state.PhaseMain:
		g.addMainHits(g.geom())
	default: // disconnected view
		g.addBarHits(g.geom())
	}
	g.addBannerHit()
}
