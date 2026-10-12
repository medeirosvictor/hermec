package ui

import (
	"testing"

	"github.com/medeirosvictor/hermec/ui/state"
)

// menuGame has one saved server "k" that is on the rail, with or without a
// session.
func menuGame(t *testing.T, withSession bool) *game {
	t.Helper()
	g := newTestGame(t)
	withLayout(t, g)
	g.servers = []state.ServerEntry{{URL: "k", Label: "k"}}
	if withSession {
		g.sess["k"] = newSession("k", "k", "me")
	}
	return g
}

func TestTileMenuOnlyWithSession(t *testing.T) {
	g := menuGame(t, false)
	g.openTileMenu("k", 10, 70)
	if g.menu.open {
		t.Fatal("menu opened for a tile without a session")
	}
	g = menuGame(t, true)
	g.openTileMenu("k", 10, 70)
	if !g.menu.open || len(g.menu.items) != 1 || g.menu.items[0].label != "Disconnect" {
		t.Fatalf("menu = %+v, want one Disconnect item", g.menu)
	}
}

func TestMenuDisconnectItem(t *testing.T) {
	g := menuGame(t, true)
	g.openTileMenu("k", 10, 70)
	g.menu.items[0].act()
	if _, ok := g.sess["k"]; ok {
		t.Fatal("session still present after Disconnect")
	}
	if g.menu.open {
		t.Fatal("menu still open after picking an item")
	}
}

func TestMenuHitsWinOverRail(t *testing.T) {
	g := menuGame(t, true)
	g.openTileMenu("k", 10, 70) // over the first rail tile
	g.buildHits()
	r := g.menuItemRect(0)
	h, ok := g.hits.At((r.x0+r.x1)/2, (r.y0+r.y1)/2)
	if !ok || h.Kind != state.HitMenuItem || h.ID != 0 {
		t.Fatalf("hit = %+v ok=%v, want HitMenuItem 0", h, ok)
	}
	g.closeMenu()
	g.buildHits()
	if h, ok := g.hits.At(20, 40); !ok || h.Kind != state.HitRailTile {
		t.Fatalf("closed menu: hit = %+v ok=%v, want rail tile", h, ok)
	}
}
