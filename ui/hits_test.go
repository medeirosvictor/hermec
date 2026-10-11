package ui

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/update"
)

func mainGame(t *testing.T) *game {
	t.Helper()
	g := testGame(t)
	g.st.SetConnected([]proto.ChannelInfo{{Name: "general"}, {Name: "random"}, {Name: "lounge", Type: "voice"}}, "fp")
	g.buildHits()
	return g
}

func TestCursorShapeMainScene(t *testing.T) {
	g := mainGame(t)
	gm := g.geom()
	row0 := gm.paneY0 + gm.lh/2
	cases := []struct {
		name string
		x, y float64
		want ebiten.CursorShapeType
	}{
		{"text channel row", gm.chanR.x0 + pad, row0, ebiten.CursorShapePointer},
		{"second channel row", gm.chanR.x0 + pad, row0 + gm.lh, ebiten.CursorShapePointer},
		{"voice row", gm.chanR.x0 + pad, row0 + 3*gm.lh, ebiten.CursorShapePointer},
		{"divider row", gm.chanR.x0 + pad, row0 + 2*gm.lh, ebiten.CursorShapeDefault},
		{"chat input", gm.inputR.x0 + pad, gm.inputR.y0 + gm.lh/2, ebiten.CursorShapeText},
		{"empty chat area", gm.chatR.x0 + pad, gm.chatR.y0 + gm.lh, ebiten.CursorShapeDefault},
		{"empty channel pane", gm.chanR.x0 + pad, gm.chanY1 - 2, ebiten.CursorShapeDefault},
	}
	for _, c := range cases {
		if got := shapeAt(&g.hits, c.x, c.y); got != c.want {
			t.Errorf("%s: shape %v, want %v", c.name, got, c.want)
		}
	}
}

func TestHitsRegisterSameAsClickHandlers(t *testing.T) {
	g := mainGame(t)
	gm := g.geom()
	h, ok := g.hits.At(gm.chanR.x0+pad, gm.paneY0+gm.lh+1)
	if !ok || h.Kind != state.HitChannel || h.ID != 1 {
		t.Fatalf("row 1 hit = %+v %v", h, ok)
	}
	h, ok = g.hits.At(gm.chanR.x0+pad, gm.paneY0+3*gm.lh+1)
	if !ok || h.Kind != state.HitVoice || h.Key != "lounge" {
		t.Fatalf("voice hit = %+v %v", h, ok)
	}
}

func TestSignalHitsAndPanelSwallow(t *testing.T) {
	g := mainGame(t)
	gm := g.geom()
	sr := g.signalRect(gm)
	h, ok := g.hits.At(sr.x0+1, sr.y0+1)
	if !ok || h.Kind != state.HitSignal || h.ID != signalBars {
		t.Fatalf("bars hit = %+v %v", h, ok)
	}
	g.q.open = true
	g.stepQuality(1) // settles the ease
	g.buildHits()
	if g.q.rect.y1 <= g.q.rect.y0 {
		t.Fatal("panel has no rect while open")
	}
	h, ok = g.hits.At(g.q.rect.x0+1, g.q.rect.y0+1)
	if !ok || h.Kind != state.HitSignal || h.ID != signalPanel {
		t.Fatalf("panel hit = %+v %v", h, ok)
	}
	g.q.open = false
	g.stepQuality(1)
	if g.q.rect.y1 > g.q.rect.y0 {
		t.Error("panel rect remains after close")
	}
}

// In a call but with the bar hidden (not yet slid in), the bars draw on the
// status line, so the hit target must be there too.
func TestSignalHitFollowsDrawnPlaceBarHidden(t *testing.T) {
	g := mainGame(t)
	g.st.SetInCall("lounge")
	g.barF, g.barInit = 0, true
	g.buildHits()
	gm := g.geom()
	sr := g.signalRect(gm)
	if sr.y0 != gm.statusY {
		t.Fatalf("signal not on status line: %+v", sr)
	}
	if h, ok := g.hits.At(sr.x0+1, sr.y0+1); !ok || h.Kind != state.HitSignal || h.ID != signalBars {
		t.Errorf("no bars hit on status line: %+v %v", h, ok)
	}
	g.barF = 1
	g.buildHits()
	gm = g.geom()
	sr = g.signalRect(gm)
	if h, ok := g.hits.At(sr.x0+1, sr.y0+1); sr.y0 != gm.barY || !ok || h.ID != signalBars {
		t.Errorf("no bars hit on call bar: %+v %v", h, ok)
	}
}

func TestQualityDisconnectWhileOpenInCall(t *testing.T) {
	g := mainGame(t)
	g.st.SetInCall("lounge")
	g.q.open = true
	g.stepQuality(1)
	if g.q.open != true || len(g.q.lines) == 0 {
		t.Fatalf("panel not open before disconnect: %+v", g.q)
	}
	// What Update does when the events channel closes.
	g.st.Apply(client.Event{}, false)
	g.dropVoice()
	g.c = nil
	g.stepQuality(1)
	if g.q.grade != 0 || g.q.open {
		t.Errorf("after disconnect grade=%d open=%v, want 0 and closed", g.q.grade, g.q.open)
	}
}

func TestQualityNoDataInMainWithoutPong(t *testing.T) {
	g := mainGame(t)
	g.q.open = true
	g.stepQuality(1)
	if want := []string{"no data"}; len(g.q.lines) != 1 || g.q.lines[0] != want[0] || g.q.grade != 0 {
		t.Errorf("lines=%q grade=%d", g.q.lines, g.q.grade)
	}
}

func TestSettingsOpenClosesQualityPanel(t *testing.T) {
	g := mainGame(t)
	g.q.open = true
	g.toggleSettings()
	if g.q.open {
		t.Error("panel still open after settings opened")
	}
}

func TestHitsBarOnlyInCall(t *testing.T) {
	g := mainGame(t)
	for _, k := range []state.HitKind{state.HitBarMute, state.HitBarLeave} {
		if hasKind(&g.hits, g, k) {
			t.Fatalf("kind %v registered outside a call", k)
		}
	}
	g.st.SetInCall("lounge")
	g.buildHits()
	gm := g.geom()
	l := g.barLayout(gm)
	if h, ok := g.hits.At(l.muteX0+1, l.y+1); !ok || h.Kind != state.HitBarMute {
		t.Fatalf("mute hit = %+v %v", h, ok)
	}
	if h, ok := g.hits.At(l.leaveX0+1, l.y+1); !ok || h.Kind != state.HitBarLeave {
		t.Fatalf("leave hit = %+v %v", h, ok)
	}
}

func TestHitsRailAndBanner(t *testing.T) {
	g := mainGame(t)
	g.servers = []state.ServerEntry{{URL: "ws://a", Label: "a"}, {URL: "ws://b", Label: "b"}}
	g.buildHits()
	if h, ok := g.hits.At(10, pad+railTileH+1); !ok || h.Kind != state.HitRailTile || h.ID != 1 {
		t.Fatalf("tile 1 = %+v %v", h, ok)
	}
	if h, ok := g.hits.At(10, float64(g.h)-1.5*railTileH); !ok || h.Kind != state.HitRailAdd {
		t.Fatalf("add = %+v %v", h, ok)
	}
	if h, ok := g.hits.At(10, float64(g.h)-5); !ok || h.Kind != state.HitGear {
		t.Fatalf("gear = %+v %v", h, ok)
	}
}

func TestHitsSettingsRows(t *testing.T) {
	g := testGame(t)
	g.settingsOpen = true
	g.buildHits()
	lh := g.lineH()
	for r := 0; r < settingsRows; r++ {
		y := connectFieldsTop(lh) + float64(r)*lh + 1
		if h, ok := g.hits.At(connectX+g.railW()+5, y); !ok || h.Kind != state.HitSettingsRow || h.ID != r {
			t.Fatalf("row %d = %+v %v", r, h, ok)
		}
	}
	if _, ok := g.hits.At(connectX+g.railW()+5, connectFieldsTop(lh)+float64(settingsRows)*lh+1); ok {
		t.Fatal("hit below the last settings row")
	}
}

// A click resolved against a registry built before the discovered list shrank
// must not index past the new list.
func TestConnectClickStaleDiscoveredRow(t *testing.T) {
	g := testGame(t)
	f := &g.connect
	f.disc = make([]state.DiscoveredRow, 3)
	f.name.runes = []rune("me")
	f.sel = -1
	g.buildHits() // PhaseConnect: registers 3 discovered rows
	lh := g.lineH()
	y := f.discTop(lh) + 2*lh + 1 // the last row
	h, ok := g.hits.At(connectX+g.railW()+5, y)
	if !ok || h.Kind != state.HitDiscovered || h.ID != 2 {
		t.Fatalf("setup: hit = %+v %v", h, ok)
	}
	f.disc = f.disc[:1] // probe result shrinks the list after the registry was built
	url, name, submit, done := f.click(h, false)
	if submit || url != "" || name != "" || !done {
		t.Fatalf("stale click acted: %q %q submit=%v done=%v", url, name, submit, done)
	}
	if f.sel != -1 {
		t.Fatalf("stale click changed selection: %d", f.sel)
	}
}

// The banner shift must be on the form for the tick.
func TestBuildHitsSetsConnectBannerShift(t *testing.T) {
	g := testGame(t)
	g.update = &update.Available{Version: "v9", URL: "u"}
	g.buildHits()
	if g.connect.dy != g.bannerH() || g.bannerH() == 0 {
		t.Fatalf("dy = %v, bannerH = %v", g.connect.dy, g.bannerH())
	}
}

func hasKind(h *state.Hits, g *game, k state.HitKind) bool {
	for y := 0.0; y < float64(g.h); y += 2 {
		for x := 0.0; x < float64(g.w); x += 4 {
			if got, ok := h.At(x, y); ok && got.Kind == k {
				return true
			}
		}
	}
	return false
}
