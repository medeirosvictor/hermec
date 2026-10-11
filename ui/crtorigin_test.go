package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/theme"
)

// pixelOf reads the pixel at (x, y) relative to img's own top-left corner.
func pixelOf(img *ebiten.Image, x, y int) color.RGBA {
	m := img.Bounds().Min
	b := make([]byte, 4)
	img.SubImage(image.Rect(m.X+x, m.Y+y, m.X+x+1, m.Y+y+1)).(*ebiten.Image).ReadPixels(b)
	return color.RGBA{b[0], b[1], b[2], b[3]}
}

// flatTheme has every CRT effect off, so apply is a pure copy and any shift
// of the output is visible.
func flatTheme() theme.Theme {
	th := theme.Default()
	th.Curvature, th.CornerRadius, th.Vignette = 0, 0, 0
	th.ScanlineStrength, th.ScanBarStrength, th.Glow, th.ScanBar = 0, 0, 0, false
	return th
}

// The CRT output must land at the destination's own origin whether the
// destination is a small (atlas-allocated) image or a sub-image of a larger
// one: the shader may not use raw dst coordinates.
func TestCRTOutputNotShifted(t *testing.T) {
	run := func(name string, dst *ebiten.Image) { runWith(t, name, dst, flatTheme()) }
	glowy := flatTheme()
	glowy.Glow = 0.1
	runGlow := func(name string, dst *ebiten.Image) { runWith(t, name, dst, glowy) }
	withGraphics(t, func() {
		run("small image", ebiten.NewImage(64, 48))
		parent := ebiten.NewImage(64, 48+32)
		run("sub-image", parent.SubImage(image.Rect(0, 32, 64, 80)).(*ebiten.Image))
		run("offset sub-image", ebiten.NewImage(200, 200).SubImage(image.Rect(40, 60, 104, 108)).(*ebiten.Image))
		runGlow("glow small image", ebiten.NewImage(64, 48))
		runGlow("glow sub-image", ebiten.NewImage(200, 200).SubImage(image.Rect(40, 60, 104, 108)).(*ebiten.Image))
	})
}

func runWith(t *testing.T, name string, dst *ebiten.Image, th theme.Theme) {
	t.Helper()
	red := color.RGBA{255, 0, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}
	var c crt
	off := c.target(64, 48)
	off.Fill(blue)
	off.SubImage(image.Rect(10, 10, 14, 14)).(*ebiten.Image).Fill(red)
	c.apply(dst, th, 0)
	if c.shader == nil {
		t.Fatal("shader did not compile")
	}
	if got := pixelOf(dst, 11, 11); got != red {
		t.Errorf("%s: marker pixel = %v, want %v (output shifted)", name, got, red)
	}
	if got := pixelOf(dst, 30, 30); got != blue {
		t.Errorf("%s: background pixel = %v, want %v", name, got, blue)
	}
	if got := pixelOf(dst, 2, 2); got != blue {
		t.Errorf("%s: corner pixel = %v, want %v", name, got, blue)
	}
}
