package ui

import (
	_ "embed"
	"image/color"
	"log"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

const (
	pad    = 14 // gap between panels and inner padding of a panel
	margin = 16 // outer gap from the window edge to any panel

	panelFade = 10.0 // soft outer edge, px
)

//go:embed panel.kage
var panelShaderSrc []byte

// rectF is a float rectangle: x0,y0 top-left, x1,y1 bottom-right.
type rectF struct{ x0, y0, x1, y1 float64 }

// panelShader lazily compiles the panel shader once and supports the dev
// live-reload swap.
type panelShader struct {
	once   sync.Once
	shader *ebiten.Shader
}

func (p *panelShader) compile() {
	p.once.Do(func() {
		s, err := ebiten.NewShader(panelShaderSrc)
		if err != nil {
			log.Printf("panel shader: %v", err)
			return
		}
		p.shader = s
	})
}

// setSource compiles src and, only on success, swaps it in (compile-first,
// like crt.setSource). Call from the game goroutine only.
func (p *panelShader) setSource(src []byte) error {
	s, err := ebiten.NewShader(src)
	if err != nil {
		return err
	}
	p.once.Do(func() {})
	if p.shader != nil {
		p.shader.Deallocate()
	}
	p.shader = s
	return nil
}

// drawPanel draws a soft rounded panel surface with body (x0,y0)-(x1,y1).
func (g *game) drawPanel(dst *ebiten.Image, x0, y0, x1, y1 float64, fill color.RGBA) {
	g.drawPanelFade(dst, x0, y0, x1, y1, fill, panelFade)
}

func (g *game) drawPanelFade(dst *ebiten.Image, x0, y0, x1, y1 float64, fill color.RGBA, fade float64) {
	if x1 <= x0 || y1 <= y0 {
		return
	}
	g.panel.compile()
	if g.panel.shader == nil { // compile failed: plain flat fill
		vector.FillRect(dst, float32(x0), float32(y0), float32(x1-x0), float32(y1-y0), fill, false)
		return
	}
	ex := fade + 2
	w, h := int(x1-x0+2*ex)+1, int(y1-y0+2*ex)+1
	op := &ebiten.DrawRectShaderOptions{}
	op.GeoM.Translate(x0-ex, y0-ex)
	op.Uniforms = map[string]any{
		"Rect":   []float32{float32(x0), float32(y0), float32(x1), float32(y1)},
		"Radius": float32(g.th.Radius),
		"Fade":   float32(fade),
		"Fill":   []float32{float32(fill.R) / 255, float32(fill.G) / 255, float32(fill.B) / 255, 1},
	}
	dst.DrawRectShader(w, h, g.panel.shader, op)
}

// mixRGBA blends a toward b by t (0..1), opaque.
func mixRGBA(a, b color.RGBA, t float64) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return color.RGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), 0xFF}
}

// drawContentPanel puts the connect/settings scenes on one large panel to the
// right of the rail, below the update banner (if any).
func (g *game) drawContentPanel(screen *ebiten.Image, top float64) {
	g.drawPanelR(screen, rectF{g.railW() + margin, top + margin, float64(g.w) - margin, float64(g.h) - margin})
}

// drawPanelR draws r as a panel in the theme's panel colour.
func (g *game) drawPanelR(dst *ebiten.Image, r rectF) {
	g.drawPanel(dst, r.x0, r.y0, r.x1, r.y1, g.th.Panel)
}
