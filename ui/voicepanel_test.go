package ui

import (
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
	h := hitCenter(t, g, state.HitVoiceShare, l.shareX0, l.btnY, l.shareX1, l.btnY+l.btnH)
	g.clickBar(h)
	if !g.menu.open || len(g.menu.items) != 2 {
		t.Fatalf("share did not open the menu: open=%v", g.menu.open)
	}
	g.closeMenu()

	// disconnect is the existing leave.
	h = hitCenter(t, g, state.HitVoiceLeave, l.leaveX0, l.btnY, l.leaveX1, l.btnY+l.btnH)
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
