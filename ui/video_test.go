package ui

import (
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
