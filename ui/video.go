package ui

import (
	"errors"
	"image"
	"image/color"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/video"
)

const doubleClick = 400 * time.Millisecond

// videoSink receives decoded frames from client goroutines (possibly several at
// once during a switch). It only stores the newest and never blocks.
type videoSink struct {
	mu sync.Mutex
	fp string
	f  *video.Frame

	// free is a spare frame for the self-preview tap. A frame is in exactly
	// one place: free, the slot, or the UI; only preview frames are recycled.
	free *video.Frame
}

// spare returns a w x h frame for the tap to fill: the recycled one when the
// size matches, else a new one.
func (v *videoSink) spare(w, h int) *video.Frame {
	v.mu.Lock()
	f := v.free
	v.free = nil
	v.mu.Unlock()
	if f == nil || f.W != w || f.H != h {
		return video.NewFrame(w, h)
	}
	return f
}

// recycle hands back a frame the UI is done with.
func (v *videoSink) recycle(f *video.Frame) {
	v.mu.Lock()
	v.free = f
	v.mu.Unlock()
}

// writeIf stores the frame only if ok() holds while the sink is locked, so a
// reset (which also locks) cannot interleave between the check and the write.
func (v *videoSink) writeIf(fp string, f *video.Frame, ok func() bool) {
	v.mu.Lock()
	if ok() {
		v.fp, v.f = fp, f
	}
	v.mu.Unlock()
}

func (v *videoSink) WriteVideoFrame(fp string, f *video.Frame) {
	v.mu.Lock()
	v.fp, v.f = fp, f
	v.mu.Unlock()
}

// take returns and clears the newest frame.
func (v *videoSink) take() (string, *video.Frame) {
	v.mu.Lock()
	defer v.mu.Unlock()
	fp, f := v.fp, v.f
	v.fp, v.f = "", nil
	return fp, f
}

// watchState is the stream being watched (one at a time).
type watchState struct {
	fp, name string
	self     bool // the preview of our own share: no network watch
}

// videoView is the game-goroutine side of the picture: the newest frame
// converted to RGBA, uploaded to an Ebiten image at draw time.
type videoView struct {
	rgba    *image.RGBA
	img     *ebiten.Image
	pending bool // rgba holds a frame not yet uploaded
	w, h    int  // size of the last frame
	lastClk time.Time
	mx, my  float64   // last mouse position over the video view (scene space)
	moveAt  time.Time // when the mouse last moved
}

func (v *videoView) reset() {
	v.pending, v.w, v.h = false, 0, 0
	v.lastClk = time.Time{}
	v.moveAt = time.Time{}
}

// videoActive reports that the watch view replaces the chat on screen.
func (g *game) videoActive() bool {
	return g.watch.fp != "" && g.callKey != "" && g.callKey == g.curKey &&
		g.cur().st.Phase == state.PhaseMain && !g.settingsOpen
}

// videoPane is the flat rect the picture is letterboxed into: the chat area
// below its header row.
func (g *game) videoPane(gm geometry) rectF {
	return rectF{gm.chatR.x0 + 2, gm.paneY0, gm.chatR.x1 - 2, gm.chatR.y1 - 2}
}

func (g *game) watchFn(c *client.Client, fp string) {
	watch := g.shareOps.watch
	if watch == nil {
		watch = func(c *client.Client, fp string) {
			// Watch waits (at most a few seconds) for the server's answer. The
			// error text reaches the status line as an Err event; here only
			// the view has to follow a refusal.
			var se *client.ServerError
			var err error
			if c.SupportsMediaLanes() {
				err = c.SubscribeScreen(fp)
			} else {
				err = c.Watch(fp)
			}
			if errors.As(err, &se) && fp != "" {
				g.watchRefused.Store(&fp)
			}
		}
	}
	// Queued behind earlier media ops so the last click is the last applied.
	g.media.do(func() { watch(c, fp) })
}

// resetVideo forgets the picture: the staged frame, the sink's slot and the
// full-size texture.
func (g *game) resetVideo() {
	g.vid.reset()
	g.vsink.take()
	g.vid.rgba = nil
	if g.vid.img != nil {
		g.vid.img.Deallocate()
		g.vid.img = nil
	}
}

// clickLive starts (or switches to) watching the sharer h.Key.
func (g *game) clickLive(h state.Hit) {
	s := g.callSess()
	if s == nil {
		s = g.cur()
		s.st.Status = "join voice to watch a screen"
		return
	}
	if h.Key == "" {
		return
	}
	if h.Key == g.fp {
		g.clickSelf(s)
		return
	}
	if !g.inCall() || g.curKey != g.callKey {
		s.st.Status = "join this voice channel to watch"
		return
	}
	_, ok := s.st.SharerIn(s.st.InCall, h.Key)
	if !ok {
		s.st.Status = "that person is not sharing in your channel"
		return
	}
	g.selectScreen(h.Key)
}

// selectScreen changes only the selected presentation. Camera views remain
// populated while the screen subscription switches or closes.
func (g *game) selectScreen(fp string) {
	if fp == "" {
		g.stopWatching()
		return
	}
	s := g.callSess()
	if s == nil || s.c == nil {
		return
	}
	name, _ := s.st.SharerIn(s.st.InCall, fp)
	g.endSelfPreview()
	if g.watch.fp != "" && !g.watch.self && g.watch.fp != fp && s.c.SupportsMediaLanes() {
		g.retireMediaView(mediaViewKey{Kind: proto.MediaScreen, Fingerprint: g.watch.fp})
	}
	if !s.c.SupportsMediaLanes() {
		g.resetVideo()
	}
	g.watchRefused.Store(nil)
	g.watch = watchState{fp: fp, name: name}
	g.watchFn(s.c, fp)
}

// clickSelf opens the preview of our own outgoing stream. It is client-side
// only: the server refuses a self-watch and none is sent.
func (g *game) clickSelf(s *session) {
	if !g.share.on || !g.inCall() || g.curKey != g.callKey {
		s.st.Status = "you are not sharing a screen"
		return
	}
	if g.watch.self {
		return
	}
	g.stopWatching() // a remote watch ends first
	g.resetVideo()
	g.watch = watchState{fp: g.fp, self: true}
}

// endSelfPreview drops the preview of our own share (no client call: none was
// made). Tap frames are ignored until a preview starts again.
func (g *game) endSelfPreview() {
	if g.watch.self {
		g.watch = watchState{}
		g.resetVideo()
	}
}

// stopWatching leaves the watch view and tells the client to stop the stream.
func (g *game) stopWatching() {
	if g.watch.fp == "" {
		return
	}
	self := g.watch.self
	fp := g.watch.fp
	g.watch = watchState{}
	g.resetVideo()
	if self {
		return
	}
	if s := g.callSess(); s != nil && s.c != nil {
		if s.c.SupportsMediaLanes() {
			g.retireMediaView(mediaViewKey{Kind: proto.MediaScreen, Fingerprint: fp})
		}
		g.watchFn(s.c, "")
	}
}

// videoEsc takes an Esc press while watching and returns to the chat.
func (g *game) videoEsc(pressed bool) bool {
	if !pressed || !g.videoActive() {
		return false
	}
	g.escSpent = true
	if g.fullscreen { // first Esc: out of fullscreen, still watching
		g.exitFullscreen()
		return true
	}
	g.stopWatching()
	return true
}

// clock is the video view's time source (a seam for tests).
func (g *game) clock() time.Time {
	if g.shareOps.now != nil {
		return g.shareOps.now()
	}
	return time.Now()
}

// setFullscreen sets the window's fullscreen. Update re-syncs g.fullscreen
// from the window each tick.
func (g *game) setFullscreen(on bool) {
	if g.shareOps.setFullscreen != nil {
		g.shareOps.setFullscreen(on)
	} else {
		ebiten.SetFullscreen(on)
	}
	g.fullscreen = on
}

func (g *game) toggleFullscreen() { g.setFullscreen(!g.fullscreen) }

func (g *game) exitFullscreen() { g.setFullscreen(false) }

// syncFullscreen is the one place that decides whether the window may be
// fullscreen: only while the video view is on. Every way the view can end
// (Esc, sharer stops, refusal, leaving the call, switching server, settings)
// passes through here from the update tick.
func (g *game) syncFullscreen() {
	if g.fullscreen && !g.videoActive() {
		g.exitFullscreen()
	}
}

// videoF11 toggles fullscreen on an F11 press while the video view is on.
func (g *game) videoF11(pressed bool) {
	if pressed && g.videoActive() {
		g.toggleFullscreen()
	}
}

// stripWait is how long the on-video controls stay after the mouse last moved.
const stripWait = 2 * time.Second

// stripBottom is the share of the pane height, at the bottom, where the
// controls stay up while fullscreen.
const stripBottom = 0.15

const (
	stripStopLabel = "close (Esc)"
	stripStopFull  = "close" // in fullscreen Esc only leaves fullscreen
	stripSelfStop  = "stop sharing"
	stripChange    = "change screen"
	stripFullLabel = "fullscreen"
	stripExitLabel = "exit fullscreen (Esc)"
)

// trackVideoMouse records the mouse (scene space) over the video view; a move
// re-arms the idle timer.
func (g *game) trackVideoMouse(x, y float64, now time.Time) {
	if x != g.vid.mx || y != g.vid.my || g.vid.moveAt.IsZero() {
		g.vid.moveAt = now
	}
	g.vid.mx, g.vid.my = x, y
}

// stripVisible reports whether the on-video controls are up: the mouse is over
// the pane and moved in the last stripWait, or (fullscreen) rests in the
// bottom stripBottom of the pane.
func (g *game) stripVisible(now time.Time) bool {
	if !g.videoActive() {
		return false
	}
	p := g.videoPane(g.geom())
	if !state.InRect(g.vid.mx, g.vid.my, p.x0, p.y0, p.x1, p.y1) {
		return false
	}
	if !g.vid.moveAt.IsZero() && now.Sub(g.vid.moveAt) <= stripWait {
		return true
	}
	return g.fullscreen && g.vid.my >= p.y1-(p.y1-p.y0)*stripBottom
}

type stripRects struct{ full, stop, change rectF }

func (g *game) stopLabel() string {
	if g.watch.self {
		return stripSelfStop
	}
	if g.fullscreen {
		return stripStopFull
	}
	return stripStopLabel
}

// videoHeader is the title line of the video view, with the Esc hint that is
// true now (in fullscreen Esc only leaves fullscreen).
func (g *game) videoHeader() string {
	hint := "(Esc: chat, double-click: fullscreen)"
	if g.fullscreen {
		hint = "(Esc: exit fullscreen)"
	}
	if g.watch.self {
		return "your stream (preview)  " + hint
	}
	return "watching " + g.watch.name + "  " + hint
}

func (g *game) fullLabel() string {
	if g.fullscreen {
		return stripExitLabel
	}
	return stripFullLabel
}

// stripLayout is the three buttons, centred at the bottom of the pane. Update,
// hits and drawing all use it.
func (g *game) stripLayout(gm geometry) stripRects {
	p := g.videoPane(gm)
	wf := text.Advance(g.fullLabel(), g.face) + pad*2
	ws := text.Advance(g.stopLabel(), g.face) + pad*2
	total := wf + ws + pad
	wc := 0.0
	if g.watch.self {
		wc = text.Advance(stripChange, g.face) + pad*2
		total += wc + pad
	}
	h := gm.lh + pad
	x := (p.x0+p.x1)/2 - total/2
	if x < p.x0 {
		x = p.x0
	}
	y1 := p.y1 - pad*2
	y0 := y1 - h
	var r stripRects
	r.full = rectF{x, y0, x + wf, y1}
	x += wf + pad
	r.stop = rectF{x, y0, x + ws, y1}
	if g.watch.self {
		x += ws + pad
		r.change = rectF{x, y0, x + wc, y1}
	}
	return r
}

type stripButton struct {
	r     rectF
	k     state.HitKind
	label string
}

// stripButtons lists the buttons that exist now, left to right.
func (g *game) stripButtons(r stripRects) []stripButton {
	b := []stripButton{{r.full, state.HitVideoFull, g.fullLabel()}, {r.stop, state.HitVideoStop, g.stopLabel()}}
	if g.watch.self {
		b = append(b, stripButton{r.change, state.HitVideoChange, stripChange})
	}
	return b
}

// hotStrip is the strip button the mouse is on, by the hit registry (what a
// click would reach), if the strip is up.
func (g *game) hotStrip(now time.Time) (state.HitKind, bool) {
	if !g.stripVisible(now) {
		return 0, false
	}
	h, ok := g.hits.At(g.vid.mx, g.vid.my)
	if !ok {
		return 0, false
	}
	switch h.Kind {
	case state.HitVideoFull, state.HitVideoStop, state.HitVideoChange:
		return h.Kind, true
	}
	return 0, false
}

// videoPress handles a left press at a scene point of the video view: a strip
// button acts (and is no part of a double-click), the picture counts toward the
// double-click.
func (g *game) videoPress(x, y float64, now time.Time) {
	// The registry says what is on top: the quality panel (or anything else
	// registered later) keeps its clicks.
	h, ok := g.hits.At(x, y)
	if !ok {
		return
	}
	switch h.Kind {
	case state.HitVideoStop:
		g.vid.lastClk = time.Time{}
		if g.watch.self {
			g.stopShare() // ends the preview with it
		}
		g.stopWatching() // syncFullscreen leaves fullscreen this tick
	case state.HitVideoChange:
		g.vid.lastClk = time.Time{}
		r := g.stripLayout(g.geom())
		g.openShareMenuAt(r.change.x0, r.change.y0-pad/2)
	case state.HitVideoFull:
		g.vid.lastClk = time.Time{}
		g.toggleFullscreen()
	case state.HitVideo:
		g.videoClick(now)
	}
}

// videoClick counts a press on the video; the second within doubleClick
// toggles window fullscreen.
func (g *game) videoClick(now time.Time) {
	if !g.vid.lastClk.IsZero() && now.Sub(g.vid.lastClk) <= doubleClick {
		g.vid.lastClk = time.Time{}
		g.toggleFullscreen()
		return
	}
	g.vid.lastClk = now
}

// stepVideo moves the newest decoded frame into the RGBA staging image. Frames
// of any sharer but the watched one are dropped.
func (g *game) stepVideo() {
	fp, f := g.vsink.take()
	if f == nil || fp != g.watch.fp || g.watch.fp == "" || f.W <= 0 || f.H <= 0 {
		return
	}
	stageMediaFrame(&g.vid, f)
	if g.watch.self && fp == g.fp {
		g.vsink.recycle(f) // never a remote frame: its decoder may still own it
	}
}

// updateVideo handles the clicks of the watch view. A flat video is not bent by
// the CRT, so the press is tested against the pane with the unbent position.
func (g *game) updateVideo(inputOK bool) {
	if p := g.watchRefused.Swap(nil); p != nil && g.watch.fp == *p {
		g.stopWatching() // refused: back to the chat; the client keeps no watch
	}
	g.stepMediaFrames()
	g.stepVideo()
	if !inputOK || !g.videoActive() {
		return
	}
	now := g.clock()
	rx, ry := rawCursor()
	ry -= g.barH()
	g.trackVideoMouse(rx, ry, now)
	g.videoF11(inpututil.IsKeyJustPressed(ebiten.KeyF11))
	if g.menuSpent || !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}
	g.videoPress(rx, ry, now)
}

// addVideoHit registers the pane so the cursor and tests see it.
func (g *game) addVideoHit(gm geometry) {
	if !g.videoActive() {
		return
	}
	p := g.videoPane(gm)
	g.hits.Add(state.Hit{X0: p.x0, Y0: p.y0, X1: p.x1, Y1: p.y1, Kind: state.HitVideo})
	if g.stripVisible(g.clock()) { // after the pane: the buttons win
		r := g.stripLayout(gm)
		for _, b := range g.stripButtons(r) {
			g.hits.Add(state.Hit{X0: b.r.x0, Y0: b.r.y0, X1: b.r.x1, Y1: b.r.y1, Kind: b.k})
		}
	}
}

// drawStrip draws the on-video controls (flat, like the picture).
func (g *game) drawStrip(screen *ebiten.Image, ox, oy float64) {
	now := g.clock()
	if !g.stripVisible(now) {
		return
	}
	r := g.stripLayout(g.geom())
	hot, _ := g.hotStrip(now)
	for _, b := range g.stripButtons(r) {
		bg, fg := color.RGBA{0, 0, 0, 190}, g.th.FG
		if hot == b.k {
			bg, fg = color.RGBA{0x2E, 0x30, 0x35, 230}, g.th.Bright
		}
		x, y, w, h := float32(b.r.x0+ox), float32(b.r.y0+oy), float32(b.r.x1-b.r.x0), float32(b.r.y1-b.r.y0)
		vector.FillRect(screen, x, y, w, h, bg, false)
		vector.StrokeRect(screen, x, y, w, h, 1, g.th.Dim, false)
		g.drawText(screen, b.label, b.r.x0+pad+ox, b.r.y0+pad/2+oy, fg)
	}
}

// drawVideo draws the watched picture flat, after the CRT pass, so it gets no
// scanlines, curvature or glow. off is where the scene origin sits on screen.
func (g *game) drawVideo(screen *ebiten.Image, off image.Point) {
	if !g.videoActive() || g.dlg.mode != dlgNone {
		return
	}
	p := g.videoPane(g.geom())
	ox, oy := float64(off.X), float64(off.Y)
	vector.FillRect(screen, float32(p.x0+ox), float32(p.y0+oy), float32(p.x1-p.x0), float32(p.y1-p.y0), color.RGBA{0, 0, 0, 255}, false)
	view := g.presentationView()
	if view != nil && view.rgba != nil {
		if view.pending {
			if view.img == nil || view.img.Bounds().Dx() != view.w || view.img.Bounds().Dy() != view.h {
				if view.img != nil {
					view.img.Deallocate()
				}
				view.img = ebiten.NewImage(view.w, view.h)
			}
			view.img.WritePixels(view.rgba.Pix)
			view.pending = false
		}
		if view.img != nil {
			x, y, w, h := state.Letterbox(view.w, view.h, p.x0, p.y0, p.x1, p.y1)
			op := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
			op.GeoM.Scale(w/float64(view.w), h/float64(view.h))
			op.GeoM.Translate(x+ox, y+oy)
			screen.DrawImage(view.img, op)
		}
	} else {
		g.drawTextOff(screen, "waiting for video...", p.x0+pad+ox, p.y0+pad+oy)
	}
	g.drawStrip(screen, ox, oy)
	if g.menu.open { // the menu belongs on top of the flat layer too
		m := g.menu
		g.menu.x += ox
		g.menu.y += oy
		g.drawMenu(screen)
		g.menu = m
	}
}

func (g *game) presentationView() *videoView {
	if !g.watch.self {
		if s := g.callSess(); s != nil && s.c != nil && s.c.SupportsMediaLanes() {
			return g.mediaViews[mediaViewKey{Kind: proto.MediaScreen, Fingerprint: g.watch.fp}]
		}
	}
	return &g.vid
}

func (g *game) drawTextOff(dst *ebiten.Image, s string, x, y float64) {
	g.drawText(dst, s, x, y, g.th.Dim)
}
