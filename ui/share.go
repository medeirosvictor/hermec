package ui

import (
	"errors"
	"fmt"
	"image/color"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/screencap"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/video"
)

const (
	shareLabelOff  = "share"
	shareLabelOn   = "stop share"
	shareNoSupport = "screen share from this OS is coming later"

	// shareTipID is the hover id of the share button, beyond any rail index.
	shareTipID = 1<<20 + 1
)

// shareOps are the seams between the UI and the outside world (screen capture,
// the client, the window). A zero field means the real thing.
type shareOps struct {
	newCap     func() screencap.Source
	supported  func() bool
	start      func(c *client.Client, src client.FrameSource, w, h, fps, kbps int) error
	stop       func(c *client.Client) // may block; callers run it off the game goroutine
	watch      func(c *client.Client, fp string)
	fullscreen func()           // toggles the window's fullscreen
	now        func() time.Time // clock of the video view
}

// shareState is the local side of an active screen share.
type shareState struct {
	on  bool
	mon string
	src *captureSource
	c   *client.Client
}

// captureSource adapts a screencap.Source to client.FrameSource. Next is called
// from the client's grab goroutine; Close may come from another, so both hold mu.
type captureSource struct {
	mu     sync.Mutex
	src    screencap.Source
	mon    screencap.Monitor
	buf    screencap.BGRA
	closed bool
	err    error
}

func newCaptureSource(src screencap.Source, mon screencap.Monitor) *captureSource {
	return &captureSource{src: src, mon: mon}
}

// Next captures the monitor into dst (already sized by the client). A capture
// error (monitor unplugged, layout changed) is recorded and returned, which
// ends the share rather than sending black frames.
func (c *captureSource) Next(dst *video.Frame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("capture closed")
	}
	if err := c.src.Capture(c.mon, &c.buf); err != nil {
		c.err = err
		return err
	}
	video.FromBGRA(dst, c.buf.Pix, c.buf.W, c.buf.H, c.buf.Stride)
	return nil
}

// failure is the capture error that ended the source, if any.
func (c *captureSource) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *captureSource) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.src.Close()
}

func (g *game) shareSupported() bool {
	if g.shareOps.supported != nil {
		return g.shareOps.supported()
	}
	return screencap.Supported
}

func (g *game) newCapture() screencap.Source {
	if g.shareOps.newCap != nil {
		return g.shareOps.newCap()
	}
	return screencap.New()
}

func (g *game) shareLabel() string {
	if g.share.on {
		return shareLabelOn
	}
	return shareLabelOff
}

// liveCols is the width, in columns, the LIVE tag takes from an occupant name.
const liveCols = 5

// liveRed is the LIVE tag colour (fixed, not themed: it must read as "on air").
var liveRed = color.RGBA{0xD8, 0x2B, 0x2B, 0xFF}

// liveRect is the LIVE tag of the occupant row drawn at y: at the right end of
// the channel pane, left of the muted-mic icon when there is one.
func (g *game) liveRect(gm geometry, y float64, muted bool) rectF {
	x1 := gm.chanR.x0 + channelPaneW - pad
	if muted {
		x1 -= gm.lh + pad/2
	}
	w := text.Advance("LIVE", g.face) + pad
	return rectF{x1 - w, y, x1, y + gm.lh}
}

// clickBar acts on a press on a call-bar button.
func (g *game) clickBar(h state.Hit) {
	switch h.Kind {
	case state.HitBarMute:
		g.toggleMute()
	case state.HitBarLeave:
		g.leaveCall()
	case state.HitBarShare:
		if g.share.on {
			g.stopShare()
		} else if g.shareSupported() {
			g.openShareMenu(g.geom())
		}
	}
}

// openShareMenu lists the monitors above the share button. Capture is only
// touched once the game loop runs (this is a click handler).
func (g *game) openShareMenu(gm geometry) {
	s := g.callSess()
	if s == nil {
		return
	}
	src := g.newCapture()
	mons, err := src.Monitors()
	_ = src.Close()
	if err != nil || len(mons) == 0 {
		if err == nil {
			err = errors.New("no screens found")
		}
		s.st.Status = fmt.Sprintf("screen share: %v", err)
		return
	}
	g.closeMenu()
	items := make([]menuItem, len(mons))
	for i, m := range mons {
		items[i] = menuItem{label: m.Name, act: func() { g.closeMenu(); g.startShare(m) }}
	}
	l := g.barLayout(gm)
	w, h := g.menuSize(items)
	x, y := state.MenuOrigin(l.shareX0, gm.barR.y0-h-pad/2, w, h, float64(g.w), float64(g.h))
	g.menu = ctxMenu{open: true, key: "share", x: x, y: y, items: items}
}

// startShare begins sharing monitor m on the call's client.
func (g *game) startShare(m screencap.Monitor) {
	s := g.callSess()
	if s == nil || s.c == nil || g.share.on {
		return
	}
	p := state.ShareProfileFor(g.set.ShareQualityID())
	w, h := video.FitSize(m.W, m.H, p.MaxW, p.MaxH)
	src := newCaptureSource(g.newCapture(), m)
	start := g.shareOps.start
	if start == nil {
		start = func(c *client.Client, src client.FrameSource, w, h, fps, kbps int) error {
			return c.StartShare(src, w, h, fps, kbps)
		}
	}
	// Optimistic: the button reads "stop share" at once; the worker does the
	// slow part and a failure rolls this back on a later Update.
	c := s.c
	g.share = shareState{on: true, mon: m.Name, src: src, c: c}
	g.logf("sharing %s at %dx%d %d fps %d kbps", m.Name, w, h, p.FPS, p.KBPS)
	g.media.do(func() {
		if err := start(c, src, w, h, p.FPS, p.KBPS); err != nil {
			_ = src.Close()
			g.media.post(func() {
				if g.share.src == src {
					g.share = shareState{}
					if cs := g.callSess(); cs != nil {
						cs.st.Status = fmt.Sprintf("screen share failed: %v", err)
					}
				}
			})
		}
	})
}

// stopShare ends the local share; the client stop (which waits for its loops)
// and the capture close run in the background.
func (g *game) stopShare() {
	sh := g.share
	g.share = shareState{}
	if !sh.on {
		return
	}
	g.logf("stopped sharing %s", sh.mon)
	stop := g.shareOps.stop
	if stop == nil {
		stop = func(c *client.Client) { _ = c.StopShare() }
	}
	g.media.do(func() {
		if sh.c != nil {
			stop(sh.c)
		}
		_ = sh.src.Close()
	})
}

// endCallMedia ends share and watch because the call is over (no messages: the
// user or the connection ended it).
func (g *game) endCallMedia() {
	g.stopShare()
	g.watch = watchState{}
	g.resetVideo()
}

// stepShare runs every tick: it ends a share whose capture failed, and the
// watch when the call is gone or the sharer stopped sharing or left.
func (g *game) stepShare() {
	g.media.pump()
	s := g.callSess()
	if g.share.on {
		if err := g.share.src.failure(); err != nil {
			if s != nil {
				s.st.Status = fmt.Sprintf("screen share ended: %v", err)
			}
			g.stopShare()
		}
	}
	if g.watch.fp == "" {
		return
	}
	if s == nil || s.st.InCall == "" {
		g.watch = watchState{}
		g.resetVideo()
		return
	}
	if _, ok := s.st.SharerIn(s.st.InCall, g.watch.fp); !ok {
		s.st.Status = g.watch.name + " stopped sharing"
		g.watch = watchState{}
		g.resetVideo()
	}
}

// videoStatsSuffix is the video part of the health line ("" when idle).
func videoStatsSuffix(sharing, watching bool, sendKbps, recvKbps, fps float64) string {
	switch {
	case sharing && watching:
		return fmt.Sprintf("; video send %.0f kbps, recv %.0f kbps %.0f fps", sendKbps, recvKbps, fps)
	case sharing:
		return fmt.Sprintf("; video send %.0f kbps %.0f fps", sendKbps, fps)
	case watching:
		return fmt.Sprintf("; video recv %.0f kbps %.0f fps", recvKbps, fps)
	}
	return ""
}
