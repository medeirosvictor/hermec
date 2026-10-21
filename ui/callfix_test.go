package ui

import (
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/ui/state"
)

// Changing screen swaps the monitor inside the running source: the share is
// neither stopped nor restarted, so viewers keep watching.
func TestChangeScreenKeepsShareRunning(t *testing.T) {
	g, _, rec, _ := selfGame(t)
	src := g.share.src
	other := twoMonitors().mons[1]
	g.pickScreen(other)
	settle(t, g)
	if rec.stops != 0 || len(rec.started) != 1 || rec.keyframes != 1 {
		t.Fatalf("stops=%d started=%d keyframes=%d, want 0/1/1", rec.stops, len(rec.started), rec.keyframes)
	}
	if g.share.src != src || !g.share.on || g.share.mon != other.Name {
		t.Fatalf("share = %+v", g.share)
	}
	src.mu.Lock()
	mon := src.mon
	src.mu.Unlock()
	if mon != other {
		t.Fatalf("capturing %v, want %v", mon, other)
	}
}

func TestSwitchToClosedSourceClosesNew(t *testing.T) {
	old, nw := twoMonitors(), twoMonitors()
	cs := newCaptureSource(old, old.mons[0])
	_ = cs.Close()
	cs.switchTo(nw, nw.mons[1], nil)
	if !nw.closed.Load() {
		t.Fatal("new source leaked into a closed capture")
	}
}

func TestFullscreenLabels(t *testing.T) {
	g, _, _, _ := watchingGame(t)
	if g.fullLabel() != "fullscreen" || g.stopLabel() != "close (Esc)" {
		t.Fatalf("windowed labels %q %q", g.fullLabel(), g.stopLabel())
	}
	if !strings.Contains(g.videoHeader(), "Esc: chat") {
		t.Fatalf("windowed header %q", g.videoHeader())
	}
	g.fullscreen = true
	if g.fullLabel() != "exit fullscreen (Esc)" || g.stopLabel() != "close" {
		t.Fatalf("fullscreen labels %q %q", g.fullLabel(), g.stopLabel())
	}
	if h := g.videoHeader(); !strings.Contains(h, "Esc: exit fullscreen") || strings.Contains(h, "Esc: chat") {
		t.Fatalf("fullscreen header %q", h)
	}
}

func TestPreviewOnlyWhileOnScreen(t *testing.T) {
	g, _, _, _ := selfGame(t)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "mefp"})
	g.stepShare()
	if !g.previewOn.Load() {
		t.Fatal("tap off with the preview on screen")
	}
	g.settingsOpen = true // updateVideo does not run, stepShare does
	g.stepShare()
	if g.previewOn.Load() {
		t.Fatal("tap still copying with settings open")
	}
	g.settingsOpen = false
	g.stepShare()
	if !g.previewOn.Load() {
		t.Fatal("tap not back after settings closed")
	}
}

func TestChangeMenuAnchoredAtStripButton(t *testing.T) {
	g, _, _, now := selfGame(t)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "mefp"})
	p := g.videoPane(g.geom())
	g.trackVideoMouse(p.x0+5, p.y0+5, *now)
	g.buildHits()
	r := g.stripLayout(g.geom())
	press(g, (r.change.x0+r.change.x1)/2, (r.change.y0+r.change.y1)/2, *now)
	if !g.menu.open {
		t.Fatal("menu not open")
	}
	_, h := g.menuSize(g.menu.items)
	if g.menu.x != r.change.x0 || g.menu.y+h > r.change.y0 {
		t.Fatalf("menu at (%v,%v) h=%v, strip button at (%v,%v)", g.menu.x, g.menu.y, h, r.change.x0, r.change.y0)
	}
}

// The quality panel is covered by the video: it takes no clicks there, and the
// strip highlight follows the hit that wins.
func TestHiddenQualityPanelTakesNoClicks(t *testing.T) {
	g, _, _, now := watchingGame(t)
	g.q.open = true
	gm := g.geom()
	pane := g.videoPane(gm)
	g.q.rect = rectF{pane.x1 - 200, pane.y1 - 120, pane.x1, pane.y1}
	g.trackVideoMouse(pane.x1-50, pane.y1-50, *now)
	g.buildHits()
	h, ok := g.hits.At(pane.x1-50, pane.y1-50)
	if !ok || h.Kind == state.HitSignal {
		t.Fatalf("click under the video hit %+v ok=%v", h, ok)
	}
	// A strip button the panel would cover still wins and lights up.
	r := g.stripLayout(gm)
	cx, cy := (r.stop.x0+r.stop.x1)/2, (r.stop.y0+r.stop.y1)/2
	g.q.rect = rectF{r.stop.x0 - 5, r.stop.y0 - 5, r.stop.x1 + 5, r.stop.y1 + 5}
	g.trackVideoMouse(cx, cy, *now)
	g.buildHits()
	if h, ok := g.hits.At(cx, cy); !ok || h.Kind != state.HitVideoStop {
		t.Fatalf("strip button lost to the hidden panel: %+v", h)
	}
	if k, ok := g.hotStrip(*now); !ok || k != state.HitVideoStop {
		t.Fatalf("hot = %v ok=%v", k, ok)
	}
}

// Off the video view the open panel still wins.
func TestQualityPanelTakesClicksOutsideVideo(t *testing.T) {
	g, _ := shareGame(t)
	g.q.open = true
	g.q.rect = rectF{100, 100, 300, 200}
	g.buildHits()
	if h, ok := g.hits.At(150, 150); !ok || h.Kind != state.HitSignal || h.ID != signalPanel {
		t.Fatalf("panel hit = %+v ok=%v", h, ok)
	}
}

func TestVoicePanelSignalBarsOpenQuality(t *testing.T) {
	g, _ := shareGame(t)
	gm := g.geom()
	if gm.voiceR.y1 <= gm.voiceR.y0 {
		t.Skip("no voice panel at this size")
	}
	l := g.voiceLayout(gm)
	g.buildHits()
	h, ok := g.hits.At(l.signalX+signalW(gm.lh)/2, l.line1Y+gm.lh/2)
	if !ok || h.Kind != state.HitSignal || h.ID != signalBars {
		t.Fatalf("panel bars hit = %+v ok=%v", h, ok)
	}
}
