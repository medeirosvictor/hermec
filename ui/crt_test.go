package ui

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
)

func TestCRTShaderCompiles(t *testing.T) {
	withGraphics(t, func() {
		if _, err := ebiten.NewShader(crtShaderSrc); err != nil {
			t.Fatalf("crt.kage: %v", err)
		}
	})
}

// apply must supply every declared uniform; a mismatch panics in ebiten.
func TestCRTApplyUniforms(t *testing.T) {
	withGraphics(t, func() {
		var c crt
		th := theme.Default()
		th.Curvature, th.Glow, th.ScanBar = 0.015, 0.1, true
		th.ScanBarPeriod = 8
		c.target(64, 48)
		c.apply(ebiten.NewImage(64, 48), th, 1.5)
		if c.shader == nil {
			t.Fatal("embedded shader failed to compile")
		}
	})
}

// A click where the eye sees a target must map into the target's scene rect.
// The shader samples the scene at Barrel(screen pos), so the screen position
// showing the tile centre is the inverse of Barrel there.
func TestBarrelClickHitsDrawnTarget(t *testing.T) {
	const w, h, k = 960.0, 600.0, 0.015
	// A tile hugging the top-left corner of the scene.
	rx0, ry0, rx1, ry1 := 4.0, 4.0, 60.0, 40.0
	cx, cy := (rx0+rx1)/2, (ry0+ry1)/2
	sx, sy := cx, cy
	for i := 0; i < 100; i++ { // fixed-point inverse of Barrel
		bx, by := state.Barrel(sx, sy, w, h, k)
		sx += cx - bx
		sy += cy - by
	}
	mx, my := state.Barrel(sx, sy, w, h, k)
	if !state.InRect(mx, my, rx0, ry0, rx1, ry1) {
		t.Fatalf("click at %.1f,%.1f maps to %.1f,%.1f outside tile", sx, sy, mx, my)
	}
	if sx == cx && sy == cy {
		t.Fatal("expected the on-screen position to differ from the scene position")
	}
}

func TestCursorCorrectionGating(t *testing.T) {
	g := &game{th: theme.Default(), w: 960, h: 600}
	g.th.Curvature = 0.015
	g.crtOn = false
	if g.curvedCursor() {
		t.Fatal("correction active with CRT off")
	}
	g.crtOn = true
	if !g.curvedCursor() {
		t.Fatal("correction inactive with CRT on and curvature")
	}
	g.th.Curvature = 0
	if g.curvedCursor() {
		t.Fatal("correction active at zero curvature")
	}
}
