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

	// Reused per draw so drawPanel allocates nothing in steady state.
	op           ebiten.DrawRectShaderOptions
	rect, fill   []float32
	radius, fade float32
}

func (p *panelShader) init() {
	if p.op.Uniforms != nil {
		return
	}
	p.rect, p.fill = make([]float32, 4), []float32{0, 0, 0, 1}
	p.radius, p.fade = -1, -1 // force the first boxing
	p.op.Uniforms = map[string]any{"Rect": p.rect, "Fill": p.fill, "Radius": float32(0), "Fade": float32(0)}
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
	p := &g.panel
	p.init()
	p.rect[0], p.rect[1], p.rect[2], p.rect[3] = float32(x0), float32(y0), float32(x1), float32(y1)
	p.fill[0], p.fill[1], p.fill[2] = float32(fill.R)/255, float32(fill.G)/255, float32(fill.B)/255
	// Scalars are re-boxed only when they change (rarely), so a steady-state
	// draw allocates nothing.
	if r := float32(g.th.Radius); r != p.radius {
		p.radius = r
		p.op.Uniforms["Radius"] = r
	}
	if f := float32(fade); f != p.fade {
		p.fade = f
		p.op.Uniforms["Fade"] = f
	}
	p.op.GeoM.Reset()
	p.op.GeoM.Translate(x0-ex, y0-ex)
	dst.DrawRectShader(w, h, p.shader, &p.op)
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
