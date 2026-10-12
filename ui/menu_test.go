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

func TestEscClosesMenuAndKeepsDraft(t *testing.T) {
	g := menuGame(t, true)
	s := g.sess["k"]
	g.view("k")
	s.st.Input.AppendRunes([]rune("half-typed"))
	g.openTileMenu("k", 10, 70)

	g.escSpent = false
	g.closeMenuOnEsc(true) // what Update does first each tick
	g.mainEsc(s, true)     // then the main scene sees the same press

	if g.menu.open {
		t.Fatal("menu still open after Esc")
	}
	if got := s.st.Input.String(); got != "half-typed" {
		t.Fatalf("draft = %q, want it untouched", got)
	}
	// With no menu open the same press still submits (clears) the draft.
	g.escSpent = false
	g.closeMenuOnEsc(true)
	g.mainEsc(s, true)
	if got := s.st.Input.String(); got != "" {
		t.Fatalf("draft = %q, want cleared by a plain Esc", got)
	}
}

func TestMenuDismissClickIsSwallowed(t *testing.T) {
	g := menuGame(t, true)
	g.openTileMenu("k", 10, 70)
	g.buildHits()
	g.hits.Add(state.Hit{X0: 400, Y0: 200, X1: 600, Y1: 230, Kind: state.HitVoice, Key: "voice"})
	if _, ok := g.hits.At(500, 215); !ok {
		t.Fatal("setup: voice row not hit")
	}
	g.menuPress(500, 215)
	if g.menu.open {
		t.Fatal("menu still open after an outside click")
	}
	if h, ok := g.hits.At(500, 215); ok {
		t.Fatalf("hit %+v survives the dismissing press; later handlers would act on it", h)
	}
	if g.vc.busy {
		t.Fatal("a voice join started from the dismissing click")
	}
}
