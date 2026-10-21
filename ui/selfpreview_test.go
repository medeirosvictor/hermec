package ui

import (
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/video"
)

// selfGame is shareGame where we (fp "mefp") are sharing screen 1 and the
// preview is not yet open.
func selfGame(t *testing.T) (*game, *session, *recorder, *time.Time) {
	t.Helper()
	g, s := shareGame(t)
	g.fp = "mefp"
	s.st.Voice["voice"] = append(s.st.Voice["voice"], proto.VoiceMember{Fingerprint: "mefp", Name: "me", Sharing: true})
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	now := time.Unix(1000, 0)
	g.shareOps.now = func() time.Time { return now }
	g.openShareMenu(g.geom())
	g.menu.items[0].act()
	settle(t, g)
	if !g.share.on {
		t.Fatal("share did not start")
	}
	return g, s, rec, &now
}

func TestSelfLiveOpensPreviewWithoutWatch(t *testing.T) {
	g, _, rec, _ := selfGame(t)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "mefp"})
	settle(t, g)
	if !g.videoActive() || !g.watch.self {
		t.Fatalf("preview not open: active=%v watch=%+v", g.videoActive(), g.watch)
	}
	if len(rec.watches) != 0 {
		t.Fatalf("Watch called for self: %v", rec.watches)
	}
	g.stopWatching() // closing the preview sends no Watch("") either
	settle(t, g)
	if len(rec.watches) != 0 || g.videoActive() || !g.share.on {
		t.Fatalf("close: watches=%v active=%v share=%v", rec.watches, g.videoActive(), g.share.on)
	}
}

func TestSelfLiveWithoutShareDoesNothing(t *testing.T) {
	g, s := shareGame(t)
	g.fp = "mefp"
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "mefp"})
	if g.watch.fp != "" || s.st.Status == "" {
		t.Fatalf("watch=%+v status=%q", g.watch, s.st.Status)
	}
}

func TestSelfPreviewFramesComeFromTap(t *testing.T) {
	g, _, rec, _ := selfGame(t)
	src := g.share.src
	f := video.NewFrame(4, 2)
	// Preview closed: the tap is silent.
	if err := src.Next(f); err != nil {
		t.Fatal(err)
	}
	if _, got := g.vsink.take(); got != nil {
		t.Fatal("tap fed the sink with no preview open")
	}
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "mefp"})
	g.stepVideo() // the tick turns the tap on
	if err := src.Next(f); err != nil {
		t.Fatal(err)
	}
	g.stepVideo()
	if !g.vid.pending || g.vid.w != 4 || g.vid.h != 2 {
		t.Fatalf("frame not staged: pending=%v %dx%d", g.vid.pending, g.vid.w, g.vid.h)
	}
	// The consumed frame is recycled: the next tap frame reuses its planes.
	spare := g.vsink.spare(4, 2)
	g.vsink.recycle(spare)
	if err := src.Next(f); err != nil {
		t.Fatal(err)
	}
	if _, got := g.vsink.take(); got != spare {
		t.Fatal("tap did not reuse the recycled frame")
	}
	if len(rec.watches) != 0 {
		t.Fatal("network watch used for the preview")
	}
}

func TestSelfPreviewEnds(t *testing.T) {
	ends := map[string]func(g *game, s *session){
		"stop share":     func(g *game, s *session) { g.stopShare() },
		"leave voice":    func(g *game, s *session) { s.st.SetInCall(""); g.stepShare() },
		"capture failed": func(g *game, s *session) { g.share = shareState{}; g.stepShare() },
	}
	for name, end := range ends {
		t.Run(name, func(t *testing.T) {
			g, s, _, _ := selfGame(t)
			g.clickLive(state.Hit{Kind: state.HitLive, Key: "mefp"})
			g.fullscreen = true
			src := g.share.src
			end(g, s)
			g.syncFullscreen()
			if g.watch.fp != "" || g.videoActive() || g.fullscreen {
				t.Fatalf("preview alive: watch=%+v fullscreen=%v", g.watch, g.fullscreen)
			}
			// A late frame from the ended share must not reach a later view.
			g.previewOn.Store(true)
			_ = src.Next(video.NewFrame(4, 2))
			if name == "stop share" {
				if _, got := g.vsink.take(); got != nil {
					t.Fatal("stale frame reached the sink")
				}
			}
		})
	}
}

func TestSelfStripButtons(t *testing.T) {
	g, _, rec, now := selfGame(t)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "mefp"})
	p := g.videoPane(g.geom())
	g.trackVideoMouse(p.x0+5, p.y0+5, *now)
	g.buildHits()
	r := g.stripLayout(g.geom())
	for k, b := range map[state.HitKind]rectF{state.HitVideoFull: r.full, state.HitVideoStop: r.stop, state.HitVideoChange: r.change} {
		if h, ok := g.hits.At((b.x0+b.x1)/2, (b.y0+b.y1)/2); !ok || h.Kind != k {
			t.Fatalf("hit for %v = %+v", k, h)
		}
	}
	if g.stopLabel() != "stop sharing" {
		t.Fatalf("stop label = %q", g.stopLabel())
	}

	// change screen opens the monitor menu; choosing keeps the preview.
	press(g, (r.change.x0+r.change.x1)/2, (r.change.y0+r.change.y1)/2, *now)
	if !g.menu.open || len(g.menu.items) != 2 {
		t.Fatalf("menu open=%v items=%d", g.menu.open, len(g.menu.items))
	}
	g.vsink.WriteVideoFrame("mefp", video.NewFrame(4, 2))
	g.menu.items[1].act()
	settle(t, g)
	if !g.watch.self || !g.videoActive() || !g.share.on || g.share.mon != "screen 2 (1920×1080)" {
		t.Fatalf("after change: watch=%+v mon=%q", g.watch, g.share.mon)
	}
	g.stepVideo()
	if g.vid.pending {
		t.Fatal("old screen's frame survived the change")
	}
	if rec.stops != 1 || len(rec.started) != 2 {
		t.Fatalf("stops=%d started=%d", rec.stops, len(rec.started))
	}

	// stop sharing ends the share and the preview.
	g.trackVideoMouse(p.x0+5, p.y0+5, *now)
	g.buildHits()
	r = g.stripLayout(g.geom())
	press(g, (r.stop.x0+r.stop.x1)/2, (r.stop.y0+r.stop.y1)/2, *now)
	settle(t, g)
	if g.share.on || g.videoActive() || rec.stops != 2 {
		t.Fatalf("share=%v active=%v stops=%d", g.share.on, g.videoActive(), rec.stops)
	}
}

// A frame already past the old source's checks when the screen changes must
// not land in the new preview.
func TestSelfChangeScreenDropsInFlightFrame(t *testing.T) {
	g, _, _, _ := selfGame(t)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "mefp"})
	g.stepVideo()
	oldTap := g.share.src.tap
	g.pickScreen(twoMonitors().mons[1])
	settle(t, g)
	g.stepVideo() // previewOn follows the restored preview
	oldTap(video.NewFrame(4, 2))
	g.stepVideo()
	if g.vid.pending {
		t.Fatal("in-flight frame of the old screen reached the new preview")
	}
	g.share.src.tap(video.NewFrame(4, 2)) // the new source still works
	g.stepVideo()
	if !g.vid.pending {
		t.Fatal("new screen's frame was dropped")
	}
}

func TestSelfPreviewEscCloses(t *testing.T) {
	g, _, _, _ := selfGame(t)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "mefp"})
	g.videoEsc(true)
	if g.videoActive() || !g.share.on {
		t.Fatalf("Esc: active=%v share=%v", g.videoActive(), g.share.on)
	}
}
