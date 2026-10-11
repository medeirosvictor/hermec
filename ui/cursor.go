package ui

import (
	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/state"
)

// curvature is the effective barrel strength: the theme value, or 0 when the
// user switched curvature off (F3 / Settings). It feeds both the shader and
// the click correction so they cannot disagree.
func (g *game) curvature() float64 {
	if !g.set.CurvatureEnabled() {
		return 0
	}
	return g.th.Curvature
}

// curvedCursor reports whether the CRT barrel distortion is shifting what the
// user sees, so the raw mouse position needs mapping back to scene space.
func (g *game) curvedCursor() bool {
	return g.crtOn && g.curvature() > 0
}

// cursor returns the mouse position in scene coordinates. All scene hit tests
// use this; title-bar zones use the raw screen position instead.
func (g *game) cursor() (float64, float64) {
	x, y := ebiten.CursorPosition()
	return g.sceneAt(x, y)
}

// sceneAt maps a screen pixel to scene coordinates: the pixel centre (matching
// the shader's dst pixel centres), moved up by the title bar, then passed
// through the same barrel formula the CRT shader samples with, so a click
// lands on what is drawn under the pointer. With the CRT off or zero
// curvature the barrel step is skipped.
func (g *game) sceneAt(x, y int) (float64, float64) {
	fx, fy := float64(x)+0.5, float64(y)+0.5-g.barH()
	if g.curvedCursor() && g.w > 0 && g.h > 0 {
		return state.Barrel(fx, fy, float64(g.w), float64(g.h), g.curvature())
	}
	return fx, fy
}
