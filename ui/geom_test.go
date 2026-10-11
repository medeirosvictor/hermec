package ui

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
)

func testGame(t *testing.T) *game {
	t.Helper()
	th := theme.Default()
	face, err := theme.Face(th.FontSize)
	if err != nil {
		t.Skip(err)
	}
	return &game{st: state.New(), th: th, face: face, w: 960, h: 600}
}

// The chat input sits directly under the chat pane in the right column, the
// channel pane runs beside it, and the call bar (when shown) is the bottom
// strip. All of it comes from geom(), which draw and hit-testing share.
func TestGeomInputUnderChatPane(t *testing.T) {
	g := testGame(t)
	gm := g.geom()
	if gm.inputR.y0-gm.chatR.y1 != pad {
		t.Fatalf("input panel not one pad under chat panel: %+v", gm)
	}
	if gm.chanR.y1 != gm.inputR.y1 {
		t.Fatalf("channel pane should end level with the input panel: %v vs %v", gm.chanR.y1, gm.inputR.y1)
	}
	noBar := gm
	g.st.SetInCall("voice")
	gm = g.geom()
	if gm.barR.y1 != float64(g.h)-margin || gm.chanR.y1 != gm.barR.y0-pad {
		t.Fatalf("bar not the bottom strip: %+v", gm)
	}
	if gm.inputR.y0 >= noBar.inputR.y0 || gm.rightX != noBar.rightX {
		t.Fatalf("input should shift up above the bar, same column: %+v vs %+v", gm, noBar)
	}
}

func panelRects(gm geometry, bar bool) map[string]rectF {
	m := map[string]rectF{"status": gm.statusR, "chan": gm.chanR, "chat": gm.chatR, "input": gm.inputR}
	if bar {
		m["bar"] = gm.barR
	}
	return m
}

func TestGeomMargins(t *testing.T) {
	g := testGame(t)
	for _, call := range []bool{false, true} {
		if call {
			g.st.SetInCall("voice")
		}
		gm := g.geom()
		for name, r := range panelRects(gm, call) {
			if r.x0 < margin || r.y0 < margin || r.x1 > float64(g.w)-margin || r.y1 > float64(g.h)-margin {
				t.Errorf("call=%v %s %+v breaks the %v margin", call, name, r, float64(margin))
			}
		}
	}
}

func TestGeomPanelsDontOverlap(t *testing.T) {
	g := testGame(t)
	for _, call := range []bool{false, true} {
		if call {
			g.st.SetInCall("voice")
		}
		rs := panelRects(g.geom(), call)
		for a, ra := range rs {
			for b, rb := range rs {
				if a >= b {
					continue
				}
				// Disjoint with at least pad of background between them.
				gapX := math.Max(ra.x0-rb.x1, rb.x0-ra.x1)
				gapY := math.Max(ra.y0-rb.y1, rb.y0-ra.y1)
				if math.Max(gapX, gapY) < pad {
					t.Errorf("call=%v %s %+v and %s %+v closer than pad", call, a, ra, b, rb)
				}
			}
		}
	}
}

// The panel is opaque in its body, rounded at the corners and fades to
// nothing outside the soft edge.
func TestPanelDrawShape(t *testing.T) {
	withGraphics(t, func() {
		g := &game{th: theme.Default()}
		img := ebiten.NewImage(160, 120)
		fill := color.RGBA{40, 60, 80, 255}
		g.drawPanel(img, 30, 30, 130, 90, fill)
		at := func(x, y int) color.RGBA {
			b := make([]byte, 4)
			img.SubImage(image.Rect(x, y, x+1, y+1)).(*ebiten.Image).ReadPixels(b)
			return color.RGBA{b[0], b[1], b[2], b[3]}
		}
		if c := at(80, 60); c != fill {
			t.Errorf("body pixel = %v, want %v", c, fill)
		}
		if c := at(30, 30); c.A >= 255 || c.A == 0 {
			t.Errorf("rounded corner pixel should be partial, got %v", c)
		}
		if c := at(5, 5); c.A != 0 {
			t.Errorf("far outside should be empty, got %v", c)
		}
		if c := at(25, 60); c.A == 0 || c.A >= 255 {
			t.Errorf("soft edge outside the body should be partial, got %v", c)
		}
	})
}

// After a warm-up draw, drawing a panel allocates nothing (uniform map and
// slices are reused).
func TestPanelDrawNoAllocs(t *testing.T) {
	withGraphics(t, func() {
		g := &game{th: theme.Default()}
		img := ebiten.NewImage(160, 120)
		fill := color.RGBA{40, 60, 80, 255}
		g.drawPanel(img, 30, 30, 130, 90, fill)
		n := testing.AllocsPerRun(100, func() { g.drawPanel(img, 30, 30, 130, 90, fill) })
		if n != 0 {
			t.Errorf("drawPanel allocs/op = %v, want 0", n)
		}
	})
}

func TestPanelShaderCompiles(t *testing.T) {
	withGraphics(t, func() {
		var p panelShader
		p.compile()
		if p.shader == nil {
			t.Fatal("embedded panel shader did not compile")
		}
		if err := p.setSource([]byte("package main\nfunc Fragment( {")); err == nil {
			t.Fatal("invalid source compiled")
		}
	})
}
