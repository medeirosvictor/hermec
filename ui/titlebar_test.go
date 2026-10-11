package ui

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
)

// With a title bar, the scene sits barH below the screen top, so the raw
// screen position is shifted up by barH before the barrel correction.
func TestCursorSubtractsBar(t *testing.T) {
	g := &game{th: theme.Default(), w: 960, h: 600 - int(titleBarH)}
	g.crtOn = false
	x, y := g.sceneAt(100, 132)
	wantY := 100.5
	if !hasCustomBar() {
		wantY = 132.5
	}
	if x != 100.5 || y != wantY {
		t.Fatalf("sceneAt(100,132) = %v,%v, want 100.5,%v", x, y, wantY)
	}
}

// The barrel correction runs on the scene-relative point, with the scene's
// own size.
func TestCursorBarrelUsesSceneSize(t *testing.T) {
	g := &game{th: theme.Default(), w: 960, h: 568}
	g.th.Curvature = 0.015
	g.crtOn = true
	x, y := g.sceneAt(40, 40+int(g.barH()))
	wx, wy := state.Barrel(40.5, 40.5, 960, 568, 0.015)
	if x != wx || y != wy {
		t.Fatalf("got %v,%v want %v,%v", x, y, wx, wy)
	}
}

func TestShapeForZone(t *testing.T) {
	cases := map[state.Zone]ebiten.CursorShapeType{
		state.ZoneLeft: ebiten.CursorShapeEWResize, state.ZoneRight: ebiten.CursorShapeEWResize,
		state.ZoneTop: ebiten.CursorShapeNSResize, state.ZoneBottom: ebiten.CursorShapeNSResize,
		state.ZoneTopLeft: ebiten.CursorShapeNWSEResize, state.ZoneBottomRight: ebiten.CursorShapeNWSEResize,
		state.ZoneTopRight: ebiten.CursorShapeNESWResize, state.ZoneBottomLeft: ebiten.CursorShapeNESWResize,
		state.ZoneMin: ebiten.CursorShapePointer, state.ZoneMax: ebiten.CursorShapePointer,
		state.ZoneClose: ebiten.CursorShapePointer, state.ZoneCaption: ebiten.CursorShapeDefault,
	}
	for z, want := range cases {
		if got := shapeForZone(z); got != want {
			t.Errorf("zone %v: got %v want %v", z, got, want)
		}
	}
}

// Title-bar zones win over scene hits, so a rail tile under the left resize
// edge is not clickable there.
func TestTitleZoneBlocksSceneHits(t *testing.T) {
	g := &game{th: theme.Default(), w: 960, h: 568}
	g.hits.Add(state.Hit{X0: 0, Y0: 0, X1: 60, Y1: 60, Kind: state.HitRailTile})
	g.tzone = state.ZoneLeft
	if _, ok := g.hitFor(2.5, 10.5); ok {
		t.Fatal("scene hit reachable through a resize edge")
	}
	g.tzone = state.ZoneNone
	if _, ok := g.hitFor(2.5, 10.5); !ok {
		t.Fatal("scene hit lost with no title zone")
	}
}
