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
			if err := c.Watch(fp); errors.As(err, &se) && fp != "" {
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
	if h.Key == "" || h.Key == g.fp {
		return
	}
	if !g.inCall() || g.curKey != g.callKey {
		s.st.Status = "join this voice channel to watch"
		return
	}
	name, ok := s.st.SharerIn(s.st.InCall, h.Key)
	if !ok {
		s.st.Status = "that person is not sharing in your channel"
		return
	}
	g.resetVideo()
	g.watchRefused.Store(nil) // a refusal of an earlier target is moot now
	g.watch = watchState{fp: h.Key, name: name}
	g.watchFn(s.c, h.Key)
}

// stopWatching leaves the watch view and tells the client to stop the stream.
func (g *game) stopWatching() {
	if g.watch.fp == "" {
		return
	}
	g.watch = watchState{}
	g.resetVideo()
	if s := g.callSess(); s != nil && s.c != nil {
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
	stripFullLabel = "fullscreen"
	stripExitLabel = "exit fullscreen"
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

type stripRects struct{ full, stop rectF }

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
	ws := text.Advance(stripStopLabel, g.face) + pad*2
	total := wf + ws + pad
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
	return r
}

// stripHitAt is the strip button under a scene point, if the strip is up.
func (g *game) stripHitAt(x, y float64, now time.Time) (state.HitKind, bool) {
	if !g.stripVisible(now) {
		return 0, false
	}
	r := g.stripLayout(g.geom())
	for _, b := range []struct {
		r rectF
		k state.HitKind
	}{{r.full, state.HitVideoFull}, {r.stop, state.HitVideoStop}} {
		if state.InRect(x, y, b.r.x0, b.r.y0, b.r.x1, b.r.y1) {
			return b.k, true
		}
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
		g.stopWatching() // syncFullscreen leaves fullscreen this tick
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
	if g.vid.rgba == nil || g.vid.rgba.Bounds().Dx() != f.W || g.vid.rgba.Bounds().Dy() != f.H {
		g.vid.rgba = image.NewRGBA(image.Rect(0, 0, f.W, f.H))
	}
	f.RGBA(g.vid.rgba)
	g.vid.w, g.vid.h, g.vid.pending = f.W, f.H, true
}

// updateVideo handles the clicks of the watch view. A flat video is not bent by
// the CRT, so the press is tested against the pane with the unbent position.
func (g *game) updateVideo(inputOK bool) {
	if p := g.watchRefused.Swap(nil); p != nil && g.watch.fp == *p {
		g.stopWatching() // refused: back to the chat; the client keeps no watch
	}
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
		g.hits.Add(state.Hit{X0: r.full.x0, Y0: r.full.y0, X1: r.full.x1, Y1: r.full.y1, Kind: state.HitVideoFull})
		g.hits.Add(state.Hit{X0: r.stop.x0, Y0: r.stop.y0, X1: r.stop.x1, Y1: r.stop.y1, Kind: state.HitVideoStop})
	}
}

// drawStrip draws the on-video controls (flat, like the picture).
func (g *game) drawStrip(screen *ebiten.Image, ox, oy float64) {
	now := g.clock()
	if !g.stripVisible(now) {
		return
	}
	r := g.stripLayout(g.geom())
	hot, _ := g.stripHitAt(g.vid.mx, g.vid.my, now)
	for _, b := range []struct {
		r     rectF
		k     state.HitKind
		label string
	}{{r.full, state.HitVideoFull, g.fullLabel()}, {r.stop, state.HitVideoStop, stripStopLabel}} {
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
	if g.vid.rgba != nil {
		if g.vid.pending {
			if g.vid.img == nil || g.vid.img.Bounds().Dx() != g.vid.w || g.vid.img.Bounds().Dy() != g.vid.h {
				if g.vid.img != nil {
					g.vid.img.Deallocate()
				}
				g.vid.img = ebiten.NewImage(g.vid.w, g.vid.h)
			}
			g.vid.img.WritePixels(g.vid.rgba.Pix)
			g.vid.pending = false
		}
		if g.vid.img != nil {
			x, y, w, h := state.Letterbox(g.vid.w, g.vid.h, p.x0, p.y0, p.x1, p.y1)
			op := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
			op.GeoM.Scale(w/float64(g.vid.w), h/float64(g.vid.h))
			op.GeoM.Translate(x+ox, y+oy)
			screen.DrawImage(g.vid.img, op)
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

func (g *game) drawTextOff(dst *ebiten.Image, s string, x, y float64) {
	g.drawText(dst, s, x, y, g.th.Dim)
}
