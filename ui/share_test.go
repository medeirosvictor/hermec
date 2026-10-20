package ui

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/screencap"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/video"
)

// fakeCap is a screencap.Source with fixed monitors.
type fakeCap struct {
	mons   []screencap.Monitor
	closed bool
	err    error
}

func (f *fakeCap) Monitors() ([]screencap.Monitor, error) { return f.mons, f.err }
func (f *fakeCap) Capture(m screencap.Monitor, dst *screencap.BGRA) error {
	dst.W, dst.H, dst.Stride = 4, 2, 16
	dst.Pix = make([]byte, 32)
	return f.err
}
func (f *fakeCap) Close() error { f.closed = true; return nil }

func twoMonitors() *fakeCap {
	return &fakeCap{mons: []screencap.Monitor{
		{Index: 0, Name: screencap.MonitorName(0, 2560, 1440), W: 2560, H: 1440},
		{Index: 1, Name: screencap.MonitorName(1, 1920, 1080), X: 2560, W: 1920, H: 1080},
	}}
}

// recorder collects what the game asked the client to do.
type recorder struct {
	mu        sync.Mutex
	started   []startCall
	stops     int
	watches   []string
	startErr  error
	fullscrns int
}

type startCall struct {
	src          client.FrameSource
	w, h, fps, k int
}

func (r *recorder) ops(cp *fakeCap, supported bool) shareOps {
	return shareOps{
		newCap:    func() screencap.Source { return cp },
		supported: func() bool { return supported },
		start: func(c *client.Client, src client.FrameSource, w, h, fps, kbps int) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.startErr != nil {
				return r.startErr
			}
			r.started = append(r.started, startCall{src, w, h, fps, kbps})
			return nil
		},
		stop:       func(c *client.Client) { r.mu.Lock(); r.stops++; r.mu.Unlock() },
		watch:      func(c *client.Client, fp string) { r.mu.Lock(); r.watches = append(r.watches, fp); r.mu.Unlock() },
		fullscreen: func() { r.mu.Lock(); r.fullscrns++; r.mu.Unlock() },
	}
}

// shareGame is a main-scene game in a voice call on "voice" with ann sharing.
func shareGame(t *testing.T) (*game, *session) {
	t.Helper()
	g := testGame(t)
	g.sess = map[string]*session{}
	g.none = newSession("", "", "")
	s := newSession("k", "ws://k/", "me")
	s.st.Phase = state.PhaseMain
	s.st.Channels = []string{"general"}
	s.st.VoiceChannels = []string{"voice"}
	s.c = new(client.Client) // opaque: every client call goes through the shareOps fakes
	g.sess[s.key] = s
	g.view(s.key)
	g.callKey = s.key
	s.st.SetInCall("voice")
	g.vc = voiceCall{start: time.Now()}
	s.st.Voice["voice"] = []proto.VoiceMember{
		{Fingerprint: "annfp", Name: "ann", Sharing: true},
		{Fingerprint: "bobfp", Name: "bob"},
	}
	return g, s
}

func hitCenter(t *testing.T, g *game, kind state.HitKind, x0, y0, x1, y1 float64) state.Hit {
	t.Helper()
	g.buildHits()
	h, ok := g.hits.At((x0+x1)/2, (y0+y1)/2)
	if !ok || h.Kind != kind {
		t.Fatalf("hit = %+v ok=%v, want kind %v", h, ok, kind)
	}
	return h
}

func TestShareButtonOpensMonitorMenu(t *testing.T) {
	g, _ := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	l := g.barLayout(g.geom())
	h := hitCenter(t, g, state.HitBarShare, l.shareX0, l.y, l.shareX1, l.y+l.h)
	g.clickBar(h)
	want := []string{"screen 1 (2560×1440)", "screen 2 (1920×1080)"}
	if !g.menu.open || !reflect.DeepEqual(menuLabels(g), want) {
		t.Fatalf("menu open=%v items=%v, want %v", g.menu.open, menuLabels(g), want)
	}
	// Choosing the second monitor starts a share fitted to the 720p profile.
	g.menu.items[1].act()
	if g.menu.open {
		t.Fatal("menu still open after choosing")
	}
	if len(rec.started) != 1 {
		t.Fatalf("started %d shares", len(rec.started))
	}
	sc := rec.started[0]
	if sc.w != 1280 || sc.h != 720 || sc.fps != 15 || sc.k != 1500 {
		t.Fatalf("start = %dx%d @%d %dk, want 1280x720 @15 1500k", sc.w, sc.h, sc.fps, sc.k)
	}
	if !g.share.on {
		t.Fatal("share not marked on")
	}
	if got := g.shareLabel(); got != "stop share" {
		t.Fatalf("label = %q, want stop share", got)
	}
	// With a share active the button stops it instead of opening the menu.
	g.clickBar(h)
	if g.menu.open || g.share.on {
		t.Fatalf("stop click: menu=%v on=%v", g.menu.open, g.share.on)
	}
}

func TestShareQualityPicksProfile(t *testing.T) {
	g, _ := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	g.set.ShareQuality = "1080p"
	g.openShareMenu(g.geom())
	g.menu.items[0].act() // 2560x1440 fits to 1920x1080
	if len(rec.started) != 1 || rec.started[0].w != 1920 || rec.started[0].h != 1080 || rec.started[0].k != 3000 {
		t.Fatalf("started = %+v", rec.started)
	}
}

func TestShareStartFailureReportsAndReleases(t *testing.T) {
	g, s := shareGame(t)
	cp := twoMonitors()
	rec := &recorder{startErr: errors.New("nope")}
	g.shareOps = rec.ops(cp, true)
	g.openShareMenu(g.geom())
	g.menu.items[0].act()
	if g.share.on || s.st.Status == "" {
		t.Fatalf("on=%v status=%q, want off with a message", g.share.on, s.st.Status)
	}
	if !cp.closed {
		t.Fatal("capture source leaked after a failed start")
	}
}

func TestShareButtonDimOffWindows(t *testing.T) {
	g, _ := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), false)
	l := g.barLayout(g.geom())
	h := hitCenter(t, g, state.HitBarShare, l.shareX0, l.y, l.shareX1, l.y+l.h)
	g.clickBar(h)
	if g.menu.open {
		t.Fatal("menu opened on an unsupported OS")
	}
	g.tip = tooltipState{idx: shareTipID, show: true, anchor: h}
	if txt, ok := g.tooltipText(); !ok || txt != "screen share from this OS is coming later" {
		t.Fatalf("tooltip = %q %v", txt, ok)
	}
	g.tip.show = false
	if _, ok := g.tooltipText(); ok {
		t.Fatal("tooltip shown while not hovering")
	}
	// A supported OS has no tooltip on the button.
	g.shareOps = rec.ops(twoMonitors(), true)
	g.tip = tooltipState{idx: shareTipID, show: true, anchor: h}
	if _, ok := g.tooltipText(); ok {
		t.Fatal("tooltip on a supported OS")
	}
}

func liveHit(t *testing.T, g *game) state.Hit {
	t.Helper()
	g.buildHits()
	gm := g.geom()
	y := gm.paneY0
	for _, r := range g.cur().st.Rows() {
		if r.Kind == state.RowOccupant && r.Sharing {
			rc := g.liveRect(gm, y, r.Muted)
			h, ok := g.hits.At((rc.x0+rc.x1)/2, (rc.y0+rc.y1)/2)
			if !ok || h.Kind != state.HitLive || h.Key != r.FP {
				t.Fatalf("hit = %+v ok=%v, want HitLive for %s", h, ok, r.FP)
			}
			return h
		}
		y += gm.lh
	}
	t.Fatal("no sharing occupant row")
	return state.Hit{}
}

func TestLiveTagHit(t *testing.T) {
	g, _ := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	h := liveHit(t, g)
	g.clickLive(h)
	if !reflect.DeepEqual(rec.watches, []string{"annfp"}) {
		t.Fatalf("watches = %v", rec.watches)
	}
	if !g.videoActive() || g.watch.name != "ann" {
		t.Fatalf("view = video? %v watch %+v", g.videoActive(), g.watch)
	}
	// The video pane registers HitVideo over the chat area.
	g.buildHits()
	gm := g.geom()
	if h, ok := g.hits.At(gm.chatR.x0+20, gm.paneY0+20); !ok || h.Kind != state.HitVideo {
		t.Fatalf("video pane hit = %+v ok=%v", h, ok)
	}
}

func TestLiveClickNeedsTheCall(t *testing.T) {
	g, s := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	h := liveHit(t, g)
	setCall(g, "")
	g.clickLive(h)
	if len(rec.watches) != 0 || g.videoActive() || s.st.Status == "" {
		t.Fatalf("watches=%v video=%v status=%q", rec.watches, g.videoActive(), s.st.Status)
	}
}

func TestLiveSwitchesStream(t *testing.T) {
	g, s := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	s.st.Voice["voice"][1].Sharing = true
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "annfp"})
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "bobfp"})
	if !reflect.DeepEqual(rec.watches, []string{"annfp", "bobfp"}) || g.watch.fp != "bobfp" {
		t.Fatalf("watches = %v, now %q", rec.watches, g.watch.fp)
	}
}

func TestEscLeavesVideo(t *testing.T) {
	g, s := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "annfp"})
	s.st.Input.AppendRunes([]rune("draft"))
	g.escSpent = false
	if !g.videoEsc(true) {
		t.Fatal("Esc not taken by the video view")
	}
	if g.videoActive() {
		t.Fatal("still in video view")
	}
	if got := rec.watches; len(got) != 2 || got[1] != "" {
		t.Fatalf("watches = %v, want the last to be \"\" (stop)", got)
	}
	if s.st.Input.String() != "draft" {
		t.Fatal("Esc cleared the draft")
	}
	if g.videoEsc(true) {
		t.Fatal("Esc taken with no video")
	}
}

func TestWatchEndsWhenSharerStops(t *testing.T) {
	g, s := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "annfp"})
	g.stepShare() // still sharing: nothing changes
	if !g.videoActive() {
		t.Fatal("video ended while the sharer still shares")
	}
	s.st.Apply(client.Event{Voice: &proto.VoiceState{Channel: "voice", Members: []proto.VoiceMember{
		{Fingerprint: "annfp", Name: "ann"}, {Fingerprint: "bobfp", Name: "bob"},
	}}}, true)
	g.stepShare()
	if g.videoActive() || g.watch.fp != "" {
		t.Fatalf("still watching: %+v", g.watch)
	}
	if s.st.Status != "ann stopped sharing" {
		t.Fatalf("status = %q", s.st.Status)
	}
}

func TestWatchEndsWhenSharerLeaves(t *testing.T) {
	g, s := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "annfp"})
	s.st.Apply(client.Event{Voice: &proto.VoiceState{Channel: "voice", Members: []proto.VoiceMember{{Fingerprint: "bobfp", Name: "bob"}}}}, true)
	g.stepShare()
	if g.videoActive() || s.st.Status != "ann stopped sharing" {
		t.Fatalf("video=%v status=%q", g.videoActive(), s.st.Status)
	}
}

func TestLeavingCallEndsShareAndWatch(t *testing.T) {
	g, s := shareGame(t)
	rec := &recorder{}
	g.shareOps = rec.ops(twoMonitors(), true)
	g.clickLive(state.Hit{Kind: state.HitLive, Key: "annfp"})
	g.openShareMenu(g.geom())
	g.menu.items[0].act()
	if !g.share.on {
		t.Fatal("setup: not sharing")
	}
	g.dropVoice()
	g.stepShare()
	if g.share.on || g.videoActive() {
		t.Fatalf("share=%v video=%v after the call ended", g.share.on, g.videoActive())
	}
	if s.st.Status == "ann stopped sharing" {
		t.Fatal("our own leaving should not read as the sharer stopping")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec.mu.Lock()
		n := rec.stops
		rec.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stops = %d, want 1", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestShareQualityRowCycles(t *testing.T) {
	g := testGame(t)
	g.sess = map[string]*session{}
	g.none = newSession("", "", "")
	g.settingsPath = t.TempDir() + "/settings.toml"
	if g.set.ShareQualityID() != "720p" {
		t.Fatal("default is not 720p")
	}
	g.activateSetting(rowShare)
	if g.set.ShareQuality != "1080p" {
		t.Fatalf("after one cycle = %q", g.set.ShareQuality)
	}
	got, err := state.LoadSettings(g.settingsPath)
	if err != nil || got.ShareQuality != "1080p" {
		t.Fatalf("persisted = %+v, %v", got, err)
	}
	g.activateSetting(rowShare)
	if g.set.ShareQualityID() != "720p" {
		t.Fatalf("after two cycles = %q", g.set.ShareQuality)
	}
}

func TestCaptureSourceNext(t *testing.T) {
	cp := twoMonitors()
	src := newCaptureSource(cp, cp.mons[0])
	dst := video.NewFrame(4, 2)
	if err := src.Next(dst); err != nil {
		t.Fatal(err)
	}
	cp.err = errors.New("monitor gone")
	if err := src.Next(dst); err == nil {
		t.Fatal("capture error not returned")
	}
	if src.failure() == nil {
		t.Fatal("failure not recorded")
	}
	_ = src.Close()
	if !cp.closed {
		t.Fatal("source not closed")
	}
	if err := src.Next(dst); err == nil {
		t.Fatal("Next after Close should fail")
	}
}

func TestCaptureFailureEndsShare(t *testing.T) {
	g, s := shareGame(t)
	cp := twoMonitors()
	rec := &recorder{}
	g.shareOps = rec.ops(cp, true)
	g.openShareMenu(g.geom())
	g.menu.items[0].act()
	cp.err = errors.New("monitor gone")
	_ = g.share.src.Next(video.NewFrame(4, 2))
	g.stepShare()
	if g.share.on {
		t.Fatal("share still on after a capture failure")
	}
	if s.st.Status == "" {
		t.Fatal("no status for the failure")
	}
}

func TestVideoSuffix(t *testing.T) {
	if got := videoStatsSuffix(false, false, 0, 0, 0); got != "" {
		t.Fatalf("idle = %q", got)
	}
	if got := videoStatsSuffix(true, false, 1480.4, 0, 15); got != "; video send 1480 kbps 15 fps" {
		t.Fatalf("sharing = %q", got)
	}
	if got := videoStatsSuffix(false, true, 0, 900.6, 14.6); got != "; video recv 901 kbps 15 fps" {
		t.Fatalf("watching = %q", got)
	}
	if got := videoStatsSuffix(true, true, 1500, 900, 15); got != "; video send 1500 kbps, recv 900 kbps 15 fps" {
		t.Fatalf("both = %q", got)
	}
}
