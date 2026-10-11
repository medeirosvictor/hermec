package ui

import (
	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/state"
)

// curvedCursor reports whether the CRT barrel distortion is shifting what the
// user sees, so the raw mouse position needs mapping back to scene space.
func (g *game) curvedCursor() bool {
	return g.crtOn && g.th.Curvature > 0
}

// cursor returns the mouse position in scene coordinates: the raw position
// passed through the same barrel formula the CRT shader samples with, so a
// click lands on what is drawn under the pointer. With the CRT off or zero
// curvature it is the raw position. All hit tests use this.
func (g *game) cursor() (float64, float64) {
	x, y := ebiten.CursorPosition()
	fx, fy := float64(x), float64(y)
	if g.curvedCursor() && g.w > 0 && g.h > 0 {
		return state.Barrel(fx, fy, float64(g.w), float64(g.h), g.th.Curvature)
	}
	return fx, fy
}
