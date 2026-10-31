package crt

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/internal/gfxtest"
	"github.com/medeirosvictor/hermec/ui/theme"
)

func TestCRTShaderCompiles(t *testing.T) {
	gfxtest.Run(t, func() {
		if _, err := ebiten.NewShader(shaderSrc); err != nil {
			t.Fatalf("crt.kage: %v", err)
		}
	})
}

// Apply must supply every declared uniform; a mismatch panics in ebiten.
func TestCRTApplyUniforms(t *testing.T) {
	gfxtest.Run(t, func() {
		var c Pass
		th := theme.Default()
		th.Curvature, th.Glow, th.ScanBar = 0.015, 0.1, true
		th.ScanBarPeriod = 8
		c.Target(64, 48)
		c.Apply(ebiten.NewImage(64, 48), th, 1.5)
		if c.shader == nil {
			t.Fatal("embedded shader failed to compile")
		}
	})
}

// After warm-up, Apply (glow off: the glow pass allocates inside ebiten, 8/frame) with unchanged inputs must not allocate (uniforms are
// reused, only re-boxed when a value changes).
func TestCRTApplyNoAlloc(t *testing.T) {
	if raceBuild {
		t.Skip("AllocsPerRun is unreliable under -race")
	}
	gfxtest.Run(t, func() {
		var c Pass
		th := theme.Default()
		th.Curvature, th.Glow, th.ScanBar = 0.015, 0, true
		th.ScanBarPeriod = 8
		c.Target(64, 48)
		screen := ebiten.NewImage(64, 48)
		c.Apply(screen, th, 1.5) // warm: compile, scratch, uniforms
		c.Apply(screen, th, 1.5)
		if a := testing.AllocsPerRun(20, func() { c.Apply(screen, th, 1.5) }); a != 0 {
			t.Errorf("Pass.Apply allocates %v per frame", a)
		}
	})
}

const validKage = `//kage:unit pixels

package main

func Fragment(dst vec4, src vec2, color vec4) vec4 {
	return imageSrc0At(src)
}
`

func TestSetSourceBadShaderKeepsOld(t *testing.T) {
	gfxtest.Run(t, func() {
		var c Pass
		if err := c.SetSource([]byte(validKage)); err != nil {
			t.Fatalf("valid source: %v", err)
		}
		first := c.shader
		if first == nil {
			t.Fatal("shader nil after valid setSource")
		}
		if err := c.SetSource([]byte("package main\nfunc Fragment( {")); err == nil {
			t.Fatal("invalid source compiled")
		}
		if c.shader != first {
			t.Fatal("bad source replaced the shader")
		}
		if err := c.SetSource([]byte(validKage)); err != nil {
			t.Fatalf("second valid source: %v", err)
		}
		if c.shader == first || c.shader == nil {
			t.Fatal("valid source did not swap the shader")
		}
	})
}
