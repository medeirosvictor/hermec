package ui

import (
	_ "embed"
	"log"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
)

//go:embed crt.kage
var crtShaderSrc []byte

// glowAlpha is the strength of the 1px-offset additive phosphor glow.
const glowAlpha = 0.12

// crt holds the post-process pipeline state: the offscreen scene target and
// the lazily compiled scanline shader.
type crt struct {
	off    *ebiten.Image
	w, h   int
	once   sync.Once
	shader *ebiten.Shader
}

// compile builds the shader once; on failure it logs and leaves shader nil,
// which makes apply fall back to a plain blit.
func (c *crt) compile() {
	c.once.Do(func() {
		s, err := ebiten.NewShader(crtShaderSrc)
		if err != nil {
			log.Printf("crt shader: %v", err)
			return
		}
		c.shader = s
	})
}

// target returns the offscreen image sized w x h, recreating it only when the
// size changed.
func (c *crt) target(w, h int) *ebiten.Image {
	if c.off == nil || c.w != w || c.h != h {
		if c.off != nil {
			c.off.Deallocate()
		}
		c.off = ebiten.NewImage(w, h)
		c.w, c.h = w, h
	}
	return c.off
}

// apply post-processes the offscreen scene onto screen.
func (c *crt) apply(screen *ebiten.Image) {
	c.compile()
	if c.shader == nil {
		screen.DrawImage(c.off, nil)
		return
	}
	op := &ebiten.DrawRectShaderOptions{}
	op.Images[0] = c.off
	screen.DrawRectShader(c.w, c.h, c.shader, op)

	glow := &ebiten.DrawImageOptions{Blend: ebiten.BlendLighter}
	glow.GeoM.Translate(1, 0)
	glow.ColorScale.ScaleAlpha(glowAlpha)
	screen.DrawImage(c.off, glow)
}
