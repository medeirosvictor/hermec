package ui

import (
	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/video"
)

func TestVideoLetterbox(t *testing.T) {
	g, _ := shareGame(t)
	gm := g.geom()
	pane := g.videoPane(gm)
	for _, tc := range []struct{ fw, fh int }{{1280, 720}, {800, 600}, {1920, 1080}, {720, 1280}} {
		x, y, w, h := state.Letterbox(tc.fw, tc.fh, pane.x0, pane.y0, pane.x1, pane.y1)
		if x < pane.x0 || y < pane.y0 || x+w > pane.x1+1e-9 || y+h > pane.y1+1e-9 {
			t.Fatalf("%dx%d draws outside the pane: %v %v %v %v in %+v", tc.fw, tc.fh, x, y, w, h, pane)
		}
		if got, want := w/h, float64(tc.fw)/float64(tc.fh); got < want-0.01 || got > want+0.01 {
			t.Fatalf("%dx%d aspect %v != %v", tc.fw, tc.fh, got, want)
		}
	}
	if pane.y0 < gm.paneY0 || pane.y1 > gm.chatR.y1 || pane.x0 < gm.chatR.x0 || pane.x1 > gm.chatR.x1 {
		t.Fatalf("pane %+v outside the chat area %+v", pane, gm.chatR)
	}
}

func TestVideoSinkKeepsNewest(t *testing.T) {
	var vs videoSink
	vs.WriteVideoFrame("a", video.NewFrame(4, 4))
	f2 := video.NewFrame(8, 8)
	vs.WriteVideoFrame("a", f2)
	fp, f := vs.take()
	if fp != "a" || f != f2 {
		t.Fatalf("take = %q %p, want the newest", fp, f)
	}
	if _, f := vs.take(); f != nil {
		t.Fatal("take twice returned a frame")
	}
}

func TestStepVideoIgnoresOtherSharers(t *testing.T) {
	g, _ := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "annfp"})
	settle(t, g)
	g.vsink.WriteVideoFrame("bobfp", video.NewFrame(8, 8))
	g.stepVideo()
	if g.vid.pending {
		t.Fatal("a frame from an unwatched sharer was accepted")
	}
	g.vsink.WriteVideoFrame("annfp", video.NewFrame(8, 8))
	g.stepVideo()
	if !g.vid.pending || g.vid.rgba.Bounds().Dx() != 8 {
		t.Fatalf("frame not staged: pending=%v", g.vid.pending)
	}
}

func TestVideoDoubleClickTogglesFullscreen(t *testing.T) {
	g, _ := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "annfp"})
	settle(t, g)
	t0 := time.Now()
	g.videoClick(t0)
	if rec.fullscrns != 0 {
		t.Fatal("single click toggled fullscreen")
	}
	g.videoClick(t0.Add(150 * time.Millisecond))
	if rec.fullscrns != 1 {
		t.Fatalf("double click: fullscreen toggles = %d", rec.fullscrns)
	}
	g.videoClick(t0.Add(200 * time.Millisecond)) // a third click is a fresh first click
	if rec.fullscrns != 1 {
		t.Fatalf("triple click toggled again: %d", rec.fullscrns)
	}
	g.videoClick(t0.Add(2 * time.Second))
	if rec.fullscrns != 1 {
		t.Fatalf("slow clicks toggled: %d", rec.fullscrns)
	}
}

// watchingGame is shareGame with ann's stream being watched and a fake clock.
func watchingGame(t *testing.T) (*game, *session, *recorder, *time.Time) {
	t.Helper()
	g, s := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	now := time.Unix(1000, 0)
	g.shareOps.now = func() time.Time { return now }
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "annfp"})
	settle(t, g)
	return g, s, rec, &now
}

func TestEscInFullscreenLeavesFullscreenOnly(t *testing.T) {
	g, _, rec, _ := watchingGame(t)
	g.fullscreen = true
	if !g.videoEsc(true) {
		t.Fatal("Esc not taken")
	}
	if rec.fullscrns != 1 || g.fullscreen {
		t.Fatalf("fullscreen toggles = %d, fullscreen = %v", rec.fullscrns, g.fullscreen)
	}
	if !g.videoActive() || !g.escSpent {
		t.Fatal("first Esc must keep watching and be spent")
	}
	g.escSpent = false
	g.videoEsc(true)
	if g.videoActive() {
		t.Fatal("second Esc did not return to chat")
	}
	if rec.fullscrns != 1 {
		t.Fatalf("second Esc toggled fullscreen: %d", rec.fullscrns)
	}
}

func TestVideoEndWhileFullscreenLeavesFullscreen(t *testing.T) {
	ends := map[string]func(g *game, s *session){
		"stop watching": func(g *game, s *session) { g.stopWatching() },
		"sharer stops": func(g *game, s *session) {
			s.st.Apply(client.Event{Voice: &proto.VoiceState{Channel: "voice", Members: []proto.VoiceMember{
				{Fingerprint: "annfp", Name: "ann"}, {Fingerprint: "bobfp", Name: "bob"},
			}}}, true)
			g.stepShare()
		},
		"leave call":    func(g *game, s *session) { g.endCallMedia(); g.callKey = "" },
		"settings open": func(g *game, s *session) { g.settingsOpen = true },
		"switch server": func(g *game, s *session) { g.curKey = "other" },
	}
	for name, end := range ends {
		t.Run(name, func(t *testing.T) {
			g, s, rec, _ := watchingGame(t)
			g.fullscreen = true
			g.syncFullscreen()
			if rec.fullscrns != 0 {
				t.Fatal("left fullscreen while the video is still on")
			}
			end(g, s)
			g.syncFullscreen()
			if rec.fullscrns != 1 || g.fullscreen {
				t.Fatalf("toggles = %d fullscreen = %v", rec.fullscrns, g.fullscreen)
			}
			g.syncFullscreen()
			if rec.fullscrns != 1 {
				t.Fatal("toggled again when already windowed")
			}
		})
	}
}

func TestF11TogglesOnlyWhileWatching(t *testing.T) {
	g, _, rec, _ := watchingGame(t)
	g.videoF11(true)
	if rec.fullscrns != 1 || !g.fullscreen {
		t.Fatalf("F11: toggles = %d fullscreen = %v", rec.fullscrns, g.fullscreen)
	}
	g.videoF11(false)
	if rec.fullscrns != 1 {
		t.Fatal("toggled without a press")
	}
	g.stopWatching()
	g.videoF11(true)
	if rec.fullscrns != 1 {
		t.Fatal("F11 acted outside the video view")
	}
}

func TestVideoStripVisibility(t *testing.T) {
	g, _, _, now := watchingGame(t)
	p := g.videoPane(g.geom())
	cx, top := (p.x0+p.x1)/2, p.y0+10
	if g.stripVisible(*now) {
		t.Fatal("strip visible before the mouse moved")
	}
	g.trackVideoMouse(cx, top, *now)
	if !g.stripVisible(*now) || !g.stripVisible(now.Add(1900*time.Millisecond)) {
		t.Fatal("strip hidden right after a move")
	}
	if g.stripVisible(now.Add(2100 * time.Millisecond)) {
		t.Fatal("strip still visible after 2 s idle")
	}
	// Fullscreen: stays while the mouse rests in the bottom 15%.
	g.fullscreen = true
	if g.stripVisible(now.Add(5 * time.Second)) {
		t.Fatal("fullscreen: strip visible with the mouse at the top")
	}
	g.trackVideoMouse(cx, p.y1-5, *now)
	if !g.stripVisible(now.Add(time.Minute)) {
		t.Fatal("fullscreen: strip hidden in the bottom 15%")
	}
	g.fullscreen = false
	if g.stripVisible(now.Add(time.Minute)) {
		t.Fatal("windowed: bottom resting kept the strip")
	}
	// Mouse outside the pane: hidden.
	g.trackVideoMouse(p.x0-5, p.y0+5, *now)
	if g.stripVisible(*now) {
		t.Fatal("strip visible with the mouse off the pane")
	}
}

func stripCenter(g *game, kind state.HitKind) (float64, float64) {
	r := g.stripLayout(g.geom())
	var b rectF
	switch kind {
	case state.HitVideoChat:
		b = r.chat
	case state.HitVideoFull:
		b = r.full
	default:
		b = r.stop
	}
	return (b.x0 + b.x1) / 2, (b.y0 + b.y1) / 2
}

func TestVideoStripHitsAndActions(t *testing.T) {
	g, _, rec, now := watchingGame(t)
	g.buildHits()
	cx, cy := stripCenter(g, state.HitVideoStop)
	if h, ok := g.hits.At(cx, cy); !ok || h.Kind != state.HitVideo {
		t.Fatalf("hidden strip registered hits: %+v", h)
	}
	p := g.videoPane(g.geom())
	g.trackVideoMouse(p.x0+5, p.y0+5, *now)
	for _, k := range []state.HitKind{state.HitVideoChat, state.HitVideoFull, state.HitVideoStop} {
		x, y := stripCenter(g, k)
		g.buildHits()
		if h, ok := g.hits.At(x, y); !ok || h.Kind != k {
			t.Fatalf("hit at %v = %+v, want %v", k, h, k)
		}
	}

	// full: toggles fullscreen, keeps watching, and the press is no double-click.
	x, y := stripCenter(g, state.HitVideoFull)
	g.videoPress(x, y, *now)
	g.videoPress(x, y, now.Add(100*time.Millisecond))
	if rec.fullscrns != 2 || !g.videoActive() {
		t.Fatalf("toggles = %d active = %v", rec.fullscrns, g.videoActive())
	}
	g.fullscreen = false
	rec.fullscrns = 0
	g.videoPress(p.x0+5, p.y0+5, *now) // a picture click, then a strip click, then a picture click
	g.videoPress(x, y, *now)
	g.videoPress(p.x0+5, p.y0+5, *now)
	if rec.fullscrns != 1 { // only the strip button toggled
		t.Fatalf("strip press counted toward double-click: %d", rec.fullscrns)
	}

	// stop watching
	g.fullscreen = false
	x, y = stripCenter(g, state.HitVideoStop)
	g.videoPress(x, y, *now)
	if g.videoActive() || g.watch.fp != "" {
		t.Fatal("stop did not end the watch")
	}
}

func TestVideoStripBackToChatLeavesFullscreen(t *testing.T) {
	g, _, rec, now := watchingGame(t)
	g.fullscreen = true
	p := g.videoPane(g.geom())
	g.trackVideoMouse(p.x0+5, p.y1-3, *now)
	x, y := stripCenter(g, state.HitVideoChat)
	g.videoPress(x, y, *now)
	g.syncFullscreen()
	if g.videoActive() || g.fullscreen || rec.fullscrns != 1 {
		t.Fatalf("active %v fullscreen %v toggles %d", g.videoActive(), g.fullscreen, rec.fullscrns)
	}
}

func TestTitleBarNotShownFullscreen(t *testing.T) {
	g, _ := shareGame(t)
	g.fullscreen = true
	if g.titleBarShown() || g.barH() != 0 {
		t.Fatal("title bar shown in fullscreen")
	}
}
