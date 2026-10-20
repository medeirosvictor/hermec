package ui

import (
	"math"
	"reflect"
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

func menuLabels(g *game) []string {
	var out []string
	for _, it := range g.menu.items {
		out = append(out, it.label)
	}
	return out
}

func TestTileMenuItems(t *testing.T) {
	for _, tc := range []struct {
		withSession bool
		want        []string
	}{
		{false, []string{"Rename", "Leave Server"}},
		{true, []string{"Rename", "Disconnect", "Leave Server"}},
	} {
		g := menuGame(t, tc.withSession)
		g.openTileMenu("k", 10, 70)
		if !g.menu.open || !reflect.DeepEqual(menuLabels(g), tc.want) {
			t.Fatalf("session=%v: items = %v (open %v), want %v", tc.withSession, menuLabels(g), g.menu.open, tc.want)
		}
		last := len(g.menu.items) - 1
		for i, it := range g.menu.items {
			if want := i == last; it.sep != want || it.warn != want {
				t.Fatalf("item %d %q: sep=%v warn=%v, want both %v", i, it.label, it.sep, it.warn, want)
			}
		}
	}
}

func TestMenuItemRectsNoOverlap(t *testing.T) {
	g := menuGame(t, true)
	g.openTileMenu("k", 10, 70)
	w, h := g.menuSize(g.menu.items)
	m := g.menu
	for i := range m.items {
		r := g.menuItemRect(i)
		if r.x0 < m.x || r.y0 < m.y || r.x1 > m.x+w+1e-9 || r.y1 > m.y+h+1e-9 {
			t.Fatalf("item %d %+v outside menu box %v,%v %vx%v", i, r, m.x, m.y, w, h)
		}
		if i > 0 && r.y0 < g.menuItemRect(i-1).y1 {
			t.Fatalf("item %d overlaps item %d", i, i-1)
		}
	}
	// the separator takes room above the Leave row
	if gap := g.menuItemRect(2).y0 - g.menuItemRect(1).y1; math.Abs(float64(gap-sepH)) > 1e-9 {
		t.Fatalf("separator gap = %v, want %v", gap, sepH)
	}
}

func TestMenuDisconnectItem(t *testing.T) {
	g := menuGame(t, true)
	g.openTileMenu("k", 10, 70)
	for _, it := range g.menu.items {
		if it.label == "Disconnect" {
			it.act()
		}
	}
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

func TestMenuRetryItem(t *testing.T) {
	g := menuGame(t, true)
	g.openTileMenu("k", 10, 70)
	if got := menuLabels(g); got[0] == "Retry" {
		t.Fatalf("live key has Retry: %v", got)
	}
	g.sess["k"].failed = true
	g.openTileMenu("k", 10, 70)
	if got := menuLabels(g); got[0] != "Retry" {
		t.Fatalf("failed key items = %v, want Retry first", got)
	}
}
