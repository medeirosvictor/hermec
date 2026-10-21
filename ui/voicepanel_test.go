package ui

import (
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"testing"

	"github.com/medeirosvictor/hermec/ui/state"
)

// Clicking the voice channel you are in does nothing: it never leaves.
func TestClickOwnVoiceChannelKeepsCall(t *testing.T) {
	g, s := shareGame(t)
	s.c = nil // a leave would drop the call locally; it must not even try
	g.clickVoice("voice")
	if g.callKey != s.key || s.st.InCall != "voice" || g.vc.busy {
		t.Fatalf("own-channel click changed the call: callKey %q InCall %q busy %v", g.callKey, s.st.InCall, g.vc.busy)
	}
}

// With a join or leave in flight a voice-row click does nothing at all.
func TestClickVoiceWhileBusyDoesNothing(t *testing.T) {
	g, s := shareGame(t)
	g.vc.busy, g.vc.opKey = true, s.key
	g.clickVoice("voice")
	g.clickVoice("other")
	if g.vc.joining != "" || g.callKey != s.key || s.st.InCall != "voice" {
		t.Fatalf("busy click acted: joining %q callKey %q InCall %q", g.vc.joining, g.callKey, s.st.InCall)
	}
}

func TestVoicePanelOnlyInCall(t *testing.T) {
	g := mainGame(t)
	if g.voicePanelShown() || hasKind(&g.hits, g, state.HitVoiceLeave) {
		t.Fatal("panel shown outside a call")
	}
	setCall(g, "lounge")
	g.buildHits()
	if !g.voicePanelShown() || !hasKind(&g.hits, g, state.HitVoiceLeave) || !hasKind(&g.hits, g, state.HitVoiceShare) {
		t.Fatal("panel buttons missing in a call")
	}
}

// The panel sits inside the channel pane and no channel row reaches it.
func TestGeomVoicePanelClearsChannelRows(t *testing.T) {
	g := mainGame(t)
	plain := g.geom()
	setCall(g, "lounge")
	gm := g.geom()
	v := gm.voiceR
	if v.y1 <= v.y0 || v.x0 < gm.chanR.x0 || v.x1 > gm.chanR.x1 || v.y1 > gm.chanR.y1 || v.y0 < gm.paneY0 {
		t.Fatalf("panel %+v not inside channel pane %+v", v, gm.chanR)
	}
	if gm.chanY1 > v.y0 || gm.chanY1 >= plain.chanY1 {
		t.Fatalf("chanY1 %v must end above the panel %v and shrink from %v", gm.chanY1, v.y0, plain.chanY1)
	}
	// Even in a window too short for all rows, none is hit inside the panel.
	g.h = 300
	gm = g.geom()
	g.buildHits()
	for y := gm.voiceR.y0; y < gm.voiceR.y1; y += 2 {
		if h, ok := g.hits.At(gm.chanR.x0+1, y); ok && (h.Kind == state.HitChannel || h.Kind == state.HitVoice) {
			t.Fatalf("channel row hit %+v inside the panel at y=%v", h, y)
		}
	}
}

func TestVoicePanelButtons(t *testing.T) {
	g, s := shareGame(t)
	s.c = nil
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	g.buildHits()
	l := g.voiceLayout(g.geom())

	// share opens the same monitor menu as the call bar.
	h := hitCenter(t, g, state.HitVoiceShare, l.shareX0, l.shareY, l.shareX1, l.shareY+l.btnH)
	g.clickBar(h)
	if !g.menu.open || len(g.menu.items) != 2 {
		t.Fatalf("share did not open the menu: open=%v", g.menu.open)
	}
	g.closeMenu()

	// disconnect is the existing leave.
	h = hitCenter(t, g, state.HitVoiceLeave, l.leaveX0, l.leaveY, l.leaveX1, l.leaveY+l.btnH)
	g.clickBar(h)
	if g.callKey != "" || s.st.InCall != "" {
		t.Fatalf("disconnect left the call up: callKey %q InCall %q", g.callKey, s.st.InCall)
	}
}

// The panel names the call's channel and server.
func TestVoicePanelLineNamesCallServer(t *testing.T) {
	g, s := shareGame(t)
	g.servers = []state.ServerEntry{{URL: s.key, Label: "primos"}}
	if got := g.voicePanelLine(); got != "voice / primos" {
		t.Fatalf("line %q", got)
	}
}

// Labels are measured at the real font: whatever the layout (side by side or
// stacked), each fits its button, and the stacked panel is taller.
func TestVoicePanelLabelsFit(t *testing.T) {
	g, _ := shareGame(t)
	for _, on := range []bool{false, true} {
		g.share.on = on
		gm := g.geom()
		l := g.voiceLayout(gm)
		if w := text.Advance(disconnectLabel, g.face) + pad/2; w > l.leaveX1-l.leaveX0 {
			t.Errorf("share.on=%v: disconnect needs %v, button %v", on, w, l.leaveX1-l.leaveX0)
		}
		if w := text.Advance(g.shareLabel(), g.face) + pad/2; w > l.shareX1-l.shareX0 {
			t.Errorf("share.on=%v: %q needs %v, button %v", on, g.shareLabel(), w, l.shareX1-l.shareX0)
		}
		if l.shareX1 > gm.voiceR.x1 || l.shareY+l.btnH > gm.voiceR.y1 {
			t.Errorf("share.on=%v: buttons leave the panel: %+v in %+v", on, l, gm.voiceR)
		}
	}
	// A narrow content width forces the stacked layout, which is taller.
	if !g.voiceStacked(10) || g.voicePanelH(10, 10) <= g.voicePanelH(10, 1e6) {
		t.Fatal("stacked panel should be taller than side by side")
	}
}

// The panel keeps its place (same rect) while a join or leave is in flight, and
// its buttons are inert.
func TestVoicePanelStaysWhileBusy(t *testing.T) {
	g, s := shareGame(t)
	settled := g.geom()
	g.callKey, g.vc.busy, g.vc.joining = "", true, "other"
	s.st.SetInCall("")
	busy := g.geom()
	if busy.voiceR != settled.voiceR || busy.chanY1 != settled.chanY1 {
		t.Fatalf("panel moved while busy: %+v -> %+v", settled.voiceR, busy.voiceR)
	}
	g.buildHits()
	if hasKind(&g.hits, g, state.HitVoiceLeave) || hasKind(&g.hits, g, state.HitVoiceShare) {
		t.Fatal("buttons live while busy")
	}
	if got := g.voicePanelLine(); got != "joining other..." {
		t.Fatalf("line %q", got)
	}
}

// Opened from the panel, the monitor menu sits above the panel's share button.
func TestVoicePanelShareMenuAnchor(t *testing.T) {
	g, _ := shareGame(t)
	g.shareOps = (&recorder{}).ops(twoMonitors(), true)
	g.buildHits()
	l := g.voiceLayout(g.geom())
	h := hitCenter(t, g, state.HitVoiceShare, l.shareX0, l.shareY, l.shareX1, l.shareY+l.btnH)
	g.clickBar(h)
	_, mh := g.menuSize(g.menu.items)
	if !g.menu.open || g.menu.x != l.shareX0 || g.menu.y+mh > l.shareY {
		t.Fatalf("menu at (%v,%v) h=%v, want x=%v and bottom above y=%v", g.menu.x, g.menu.y, mh, l.shareX0, l.shareY)
	}
}

// The viewed server is not the call's: the panel and its hits still show, and
// the line names the call's server, not the viewed one.
func TestVoicePanelOnOtherViewedServer(t *testing.T) {
	g, a := shareGame(t)
	b := newSession("kb", "ws://kb/", "me")
	b.st.Phase = state.PhaseMain
	b.st.Channels = []string{"general"}
	g.sess[b.key] = b
	g.servers = []state.ServerEntry{{URL: a.key, Label: "primos"}, {URL: b.key, Label: "other"}}
	g.view(b.key)
	if g.callKey != a.key {
		t.Fatal("call moved")
	}
	g.buildHits()
	if !hasKind(&g.hits, g, state.HitVoiceLeave) || !hasKind(&g.hits, g, state.HitVoiceShare) {
		t.Fatal("panel buttons missing on another viewed server")
	}
	if got := g.voicePanelLine(); got != "voice / primos" {
		t.Fatalf("line %q", got)
	}
}

// A window too short for header, two rows and the panel drops the panel and
// leaves the channel pane as it was.
func TestVoicePanelDroppedInShortWindow(t *testing.T) {
	g, _ := shareGame(t)
	if g.geom().voiceR.y1 <= g.geom().voiceR.y0 {
		t.Fatal("panel missing at a normal height")
	}
	g.h = 150
	gm := g.geom()
	if gm.voiceR != (rectF{}) {
		t.Fatalf("panel kept in a short window: %+v", gm.voiceR)
	}
	g.buildHits()
	if hasKind(&g.hits, g, state.HitVoiceLeave) {
		t.Fatal("dropped panel still has hits")
	}
	if gm.chanY1 != gm.chanR.y1-pad {
		t.Fatalf("chanY1 %v not at the pane bottom %v", gm.chanY1, gm.chanR.y1-pad)
	}
}
