package ui

import (
	"errors"
	"image"
	"image/color"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
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
}

func (v *videoView) reset() {
	v.pending, v.w, v.h = false, 0, 0
	v.lastClk = time.Time{}
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
	g.stopWatching()
	return true
}

// videoClick counts a press on the video; the second within doubleClick
// toggles window fullscreen.
func (g *game) videoClick(now time.Time) {
	if !g.vid.lastClk.IsZero() && now.Sub(g.vid.lastClk) <= doubleClick {
		g.vid.lastClk = time.Time{}
		if g.shareOps.fullscreen != nil {
			g.shareOps.fullscreen()
		} else {
			ebiten.SetFullscreen(!ebiten.IsFullscreen())
			g.fullscreen = !g.fullscreen // Update re-syncs it from the window
		}
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
	if !inputOK || !g.videoActive() || g.menuSpent || !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}
	rx, ry := rawCursor()
	ry -= g.barH()
	p := g.videoPane(g.geom())
	if state.InRect(rx, ry, p.x0, p.y0, p.x1, p.y1) {
		g.videoClick(time.Now())
	}
}

// addVideoHit registers the pane so the cursor and tests see it.
func (g *game) addVideoHit(gm geometry) {
	if !g.videoActive() {
		return
	}
	p := g.videoPane(gm)
	g.hits.Add(state.Hit{X0: p.x0, Y0: p.y0, X1: p.x1, Y1: p.y1, Kind: state.HitVideo})
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
