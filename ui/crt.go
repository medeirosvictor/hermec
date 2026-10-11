package ui

import (
	_ "embed"
	"image"
	"log"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/theme"
)

//go:embed crt.kage
var crtShaderSrc []byte

// crt holds the post-process pipeline state: the offscreen scene target and
// the lazily compiled CRT shader.
type crt struct {
	off    *ebiten.Image
	tmp    *ebiten.Image // glow scratch
	w, h   int
	once   sync.Once
	shader *ebiten.Shader

	// Reused per frame so apply boxes a uniform only when it changes.
	op     ebiten.DrawRectShaderOptions
	blitOp ebiten.DrawImageOptions
	glowOp ebiten.DrawImageOptions
	last   [len(crtScalars)]float32
	size   []float32
	bg     []float32
	primed bool
}

// crtScalars are the float uniforms apply keeps current, in cache order.
var crtScalars = [...]string{"Curvature", "CornerRadius", "Vignette", "ScanlineStrength", "ScanBarStrength", "ScanBarPeriod", "Time", "ScanBar"}

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

// setSource compiles src and, only on success, replaces the active shader (the
// previous one is deallocated). On error the current shader is left untouched
// and the compile error is returned. It also retires the lazy embedded compile
// so a later apply cannot overwrite the swapped-in shader. Used by dev
// live-reload; call from the game goroutine only.
func (c *crt) setSource(src []byte) error {
	s, err := ebiten.NewShader(src)
	if err != nil {
		return err
	}
	c.once.Do(func() {})
	if c.shader != nil {
		c.shader.Deallocate()
	}
	c.shader = s
	return nil
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

// post returns the scratch image used for the glow pass, sized w x h.
func (c *crt) post(w, h int) *ebiten.Image {
	if c.tmp == nil || c.tmp.Bounds().Dx() != w || c.tmp.Bounds().Dy() != h {
		if c.tmp != nil {
			c.tmp.Deallocate()
		}
		c.tmp = ebiten.NewImage(w, h)
	}
	return c.tmp
}

// blit draws src at dst's own top-left corner. Drawing into a sub-image keeps
// the parent's coordinates, so the offset is applied explicitly.
func blit(dst, src *ebiten.Image) {
	m := dst.Bounds().Min
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(m.X), float64(m.Y))
	dst.DrawImage(src, op)
}

// apply post-processes the offscreen scene onto screen. t is seconds since
// start (drives the scan bar). Tunables come from th.
func (c *crt) apply(screen *ebiten.Image, th theme.Theme, t float64) {
	c.compile()
	if c.shader == nil {
		blit(screen, c.off)
		return
	}
	dst := screen
	if th.Glow > 0 {
		// Render the curved frame aside so the glow ghost follows the curve.
		dst = c.post(c.w, c.h)
		dst.Clear()
	}
	bar := 0.0
	if th.ScanBar {
		bar = 1
	}
	op := &c.op
	op.GeoM.Reset()
	// Drawing into a sub-image keeps the parent's coordinates, so move the
	// rect to the sub-image's own top-left (zero for whole images).
	m := screen.Bounds().Min
	if dst != screen {
		m = image.Point{} // the scratch is a whole image
	}
	op.GeoM.Translate(float64(m.X), float64(m.Y))
	op.Images[0] = c.off
	if op.Uniforms == nil {
		c.size, c.bg = make([]float32, 2), make([]float32, 4)
		op.Uniforms = map[string]any{"Size": c.size, "BG": c.bg}
	}
	c.size[0], c.size[1] = float32(c.w), float32(c.h)
	c.bg[0], c.bg[1], c.bg[2], c.bg[3] = float32(th.BG.R)/255, float32(th.BG.G)/255, float32(th.BG.B)/255, 1
	vals := [len(crtScalars)]float32{
		float32(th.Curvature), float32(th.CornerRadius), float32(th.Vignette),
		float32(th.ScanlineStrength), float32(th.ScanBarStrength), float32(th.ScanBarPeriod),
		float32(t), float32(bar),
	}
	for i, v := range vals {
		if !c.primed || v != c.last[i] {
			c.last[i] = v
			op.Uniforms[crtScalars[i]] = v
		}
	}
	c.primed = true
	dst.DrawRectShader(c.w, c.h, c.shader, op)
	if th.Glow > 0 {
		sm := screen.Bounds().Min
		c.blitOp.GeoM.Reset()
		c.blitOp.GeoM.Translate(float64(sm.X), float64(sm.Y))
		screen.DrawImage(dst, &c.blitOp)
		glow := &c.glowOp
		glow.Blend = ebiten.BlendLighter
		glow.GeoM.Reset()
		glow.GeoM.Translate(float64(sm.X)+1, float64(sm.Y))
		glow.ColorScale.Reset()
		glow.ColorScale.ScaleAlpha(float32(th.Glow))
		screen.DrawImage(dst, glow)
	}
}
