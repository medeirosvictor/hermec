package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"math"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/audio"
	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/ui/state"
)

// speakingLevel is the output/mic RMS above which a member glows.
const speakingLevel = 0.04

// voiceResult is the outcome of a background join or leave.
type voiceResult struct {
	c       *client.Client
	leave   bool // true: the op was a leave (or a leave-then-failed-join)
	channel string
	err     error
	// On a successful join, the opened devices the game must own.
	src, sink io.Closer
	mic       *audio.Meter
	spk       *audio.Meters
	notice    string // device fallback notice, shown on a successful join
}

// voiceCall is the GUI's side of the current call (the protocol side lives in
// the client). Only touched on the Ebiten update goroutine.
type voiceCall struct {
	busy    bool   // a join/leave goroutine is in flight
	joining string // channel being joined while busy
	start   time.Time
	muted   bool
	src     io.Closer
	sink    io.Closer
	mic     *audio.Meter
	spk     *audio.Meters
}

// barVisible reports whether the call bar is shown.
func (g *game) barVisible() bool { return g.st.InCall != "" || g.vc.joining != "" }

func closeAll(cs ...io.Closer) {
	for _, c := range cs {
		if c != nil {
			_ = c.Close()
		}
	}
}

// clickVoice toggles the call: join ch, or leave if it is the current one.
func (g *game) clickVoice(ch string) {
	if g.c == nil || g.vc.busy {
		return
	}
	if g.st.VoiceClickAction(ch) == "leave" {
		g.leaveCall()
		return
	}
	g.joinCall(ch)
}

// joinCall leaves any current call, then opens the devices and joins ch, all
// in one goroutine; the result arrives on voiceCh.
func (g *game) joinCall(ch string) {
	c := g.c
	oldSrc, oldSink := g.vc.src, g.vc.sink
	savedIn, savedOut := g.set.InputDevice, g.set.OutputDevice // read here: g.set belongs to the update goroutine
	g.vc = voiceCall{busy: true, joining: ch}
	g.st.SetInCall("")
	g.st.Status = ""
	g.logf("joining voice %s", ch)
	go func() {
		res := voiceResult{c: c, channel: ch}
		if c.VoiceChannel() != "" || oldSrc != nil || oldSink != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = c.LeaveVoice(ctx)
			cancel()
			closeAll(oldSrc, oldSink)
		}
		inDev, outDev, notice := resolveDevices(savedIn, savedOut)
		src, mic, err := audio.Capture(inDev)
		if err != nil {
			res.err = fmt.Errorf("microphone unavailable: %w", err)
			g.voiceCh <- res
			return
		}
		sink, spk, err := audio.Playback(outDev)
		if err != nil {
			closeAll(asCloser(src))
			res.err = fmt.Errorf("audio output unavailable: %w", err)
			g.voiceCh <- res
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.JoinVoice(ctx, ch, src, sink); err != nil {
			closeAll(asCloser(src), asCloser(sink))
			res.err = err
			g.voiceCh <- res
			return
		}
		res.src, res.sink, res.mic, res.spk = asCloser(src), asCloser(sink), mic, spk
		res.notice = notice
		g.voiceCh <- res
	}()
}

func asCloser(v any) io.Closer {
	c, _ := v.(io.Closer)
	return c
}

// leaveCall leaves the current call in the background and frees the devices.
func (g *game) leaveCall() {
	c := g.c
	src, sink := g.vc.src, g.vc.sink
	g.vc = voiceCall{busy: true}
	g.st.SetInCall("")
	g.logf("leaving voice")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := c.LeaveVoice(ctx)
		closeAll(src, sink)
		g.voiceCh <- voiceResult{c: c, leave: true, err: err}
	}()
}

// dropVoice releases the call's devices without talking to the server (the
// connection is gone or the call already died). Safe with no call.
func (g *game) dropVoice() {
	src, sink := g.vc.src, g.vc.sink
	busy := g.vc.busy
	g.vc = voiceCall{busy: busy} // an in-flight op still reports on voiceCh
	g.st.SetInCall("")
	if src != nil || sink != nil {
		go closeAll(src, sink)
	}
}

// updateVoice consumes background results and notices a dead call.
func (g *game) updateVoice() {
	g.applyMuteFail()
	select {
	case r := <-g.voiceCh:
		g.vc.busy = false
		g.vc.joining = ""
		switch {
		case r.c != g.c: // connection changed underneath: discard
			closeAll(r.src, r.sink)
		case r.leave:
			if r.err != nil {
				g.st.Status = fmt.Sprintf("voice leave failed: %v", r.err)
			}
		case r.err != nil:
			g.logf("voice join failed: %v", r.err)
			g.st.Status = state.VoiceJoinNotice(r.err)
		default:
			g.logf("joined voice %s", r.channel)
			g.vc = voiceCall{start: time.Now(), src: r.src, sink: r.sink, mic: r.mic, spk: r.spk}
			g.st.SetInCall(r.channel)
			if r.notice != "" {
				g.st.Status = r.notice
			}
		}
	default:
	}
	// Poll: the client reports a dead call as VoiceChannel() == "".
	if g.c != nil && !g.vc.busy && g.st.InCall != "" && g.c.VoiceChannel() == "" {
		g.logf("voice call ended")
		g.dropVoice()
		g.st.Status = "voice call ended"
	}
}

// toggleMute flips own mute (Ctrl+M or the bar's mute button).
func (g *game) toggleMute() {
	if g.c == nil || g.vc.busy || g.st.InCall == "" {
		return
	}
	g.vc.muted = !g.vc.muted
	// Single slot, latest wins: drop a pending older value, then queue ours.
	// Only this goroutine fills the slot, so the send cannot block.
	select {
	case <-g.muteCh:
	default:
	}
	g.muteCh <- muteReq{c: g.c, muted: g.vc.muted}
}

// muteReq is the desired mute state for one client.
type muteReq struct {
	c     *client.Client
	muted bool
}

// muteFail reports a SetMuted that did not reach the server.
type muteFail struct {
	muted bool // the value that failed to send
	err   error
}

// muteWorker sends the latest desired mute state, in order, one at a time.
func (g *game) muteWorker() {
	for r := range g.muteCh {
		if err := r.c.SetMuted(r.muted); err != nil && !errors.Is(err, client.ErrNotInVoice) {
			select {
			case g.muteFailCh <- muteFail{r.muted, err}:
			default:
			}
		}
	}
}

// applyMuteFail reverts the local flag when the server never got the value.
func (g *game) applyMuteFail() {
	select {
	case f := <-g.muteFailCh:
		if g.vc.muted == f.muted {
			g.vc.muted = !f.muted
		}
		g.st.Status = fmt.Sprintf("mute failed: %v", f.err)
	default:
	}
}

// barLayout is the pixel layout of the call bar, shared by update and draw.
type barLayout struct {
	y, h                             float64
	nameX                            float64
	muteX0, muteX1, leaveX0, leaveX1 float64
	meterX, meterW                   float64
}

const (
	meterWidth = 64.0
	muteLabel  = "mute"
	mutedLabel = "unmute"
	leaveLabel = "leave"
)

func (g *game) barLayout(gm geometry) barLayout {
	l := barLayout{y: gm.barY, h: gm.lh}
	x := gm.ox + pad
	l.nameX = x + gm.lh + 4 // after the speaker icon
	// Right-aligned controls: [mic mute] [leave] meter.
	right := float64(g.w) - pad
	l.meterW = meterWidth
	l.meterX = right - l.meterW
	right = l.meterX - pad*2
	lw := text.Advance(leaveLabel, g.face)
	l.leaveX1, l.leaveX0 = right, right-lw-pad
	right = l.leaveX0 - pad
	label := muteLabel
	if g.vc.muted {
		label = mutedLabel
	}
	mw := text.Advance(label, g.face) + gm.lh + 4 + pad
	l.muteX1, l.muteX0 = right, right-mw
	return l
}

func (g *game) updateBar(gm geometry) {
	if g.vc.busy || g.st.InCall == "" {
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyM) &&
		(ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight)) {
		g.toggleMute()
	}
	if !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}
	cx, cy := ebiten.CursorPosition()
	x, y := float64(cx), float64(cy)
	l := g.barLayout(gm)
	switch {
	case state.InRect(x, y, l.muteX0, l.y, l.muteX1, l.y+l.h):
		g.toggleMute()
	case state.InRect(x, y, l.leaveX0, l.y, l.leaveX1, l.y+l.h):
		g.leaveCall()
	}
}

func (g *game) drawBar(screen *ebiten.Image, gm geometry) {
	th := g.th
	l := g.barLayout(gm)
	dst := clip(screen, gm.ox, l.y, float64(g.w), l.y+l.h)

	drawSpeaker(dst, float32(gm.ox+pad), float32(l.y), float32(l.h), th.Bright)
	if g.st.InCall == "" { // still joining
		g.drawText(dst, "joining "+g.vc.joining+"...", l.nameX, l.y, th.Dim)
		return
	}
	title := fmt.Sprintf("%s  %s", g.st.InCall, state.FormatElapsed(time.Since(g.vc.start)))
	g.drawText(dst, title, l.nameX, l.y, th.Bright)

	col, label := th.FG, muteLabel
	if g.vc.muted {
		col, label = th.Dim, mutedLabel
	}
	drawMic(dst, float32(l.muteX0), float32(l.y), float32(l.h), col, g.vc.muted)
	g.drawText(dst, label, l.muteX0+l.h+4, l.y, col)
	g.drawText(dst, leaveLabel, l.leaveX0+pad/2, l.y, th.FG)

	// Own mic level; flat while muted (nothing is being sent).
	lvl := g.vc.mic.Level()
	if g.vc.muted {
		lvl = 0
	}
	drawMeter(dst, float32(l.meterX), float32(l.y+l.h/2-3), float32(l.meterW), 6, lvl, th.Dim, th.Bright)
}

// drawMeter draws a horizontal level bar (level 0..1).
func drawMeter(dst *ebiten.Image, x, y, w, h float32, level float64, frame, fill color.RGBA) {
	vector.StrokeRect(dst, x, y, w, h, 1, frame, false)
	if level < 0 {
		level = 0
	}
	if level > 1 {
		level = 1
	}
	// RMS of speech is small; a sqrt curve makes the bar readable.
	level = math.Sqrt(level)
	if iw := float32(level) * (w - 2); iw >= 1 {
		vector.FillRect(dst, x+1, y+1, iw, h-2, fill, false)
	}
}

// drawSpeaker draws a speaker glyph in a size x size box at (x, y).
func drawSpeaker(dst *ebiten.Image, x, y, size float32, c color.RGBA) {
	u := size / 12
	vector.FillRect(dst, x+1*u, y+4*u, 3*u, 4*u, c, false)
	var p vector.Path
	p.MoveTo(x+4*u, y+4*u)
	p.LineTo(x+8*u, y+1*u)
	p.LineTo(x+8*u, y+11*u)
	p.LineTo(x+4*u, y+8*u)
	p.Close()
	vector.FillPath(dst, &p, nil, &vector.DrawPathOptions{ColorScale: scaleOf(c)})
	vector.StrokeLine(dst, x+10*u, y+4*u, x+10*u, y+8*u, u, c, false)
}

// drawMic draws a microphone in a size x size box; slashed when muted.
func drawMic(dst *ebiten.Image, x, y, size float32, c color.RGBA, muted bool) {
	u := size / 12
	vector.FillRect(dst, x+4*u, y+1*u, 3*u, 6*u, c, false)
	vector.StrokeRect(dst, x+2.5*u, y+5*u, 6*u, 3*u, u, c, false)
	vector.StrokeLine(dst, x+5.5*u, y+8*u, x+5.5*u, y+11*u, u, c, false)
	vector.StrokeLine(dst, x+3.5*u, y+11*u, x+7.5*u, y+11*u, u, c, false)
	if muted {
		vector.StrokeLine(dst, x+1*u, y+11*u, x+10*u, y+1*u, 1.5*u, c, false)
	}
}

func scaleOf(c color.RGBA) ebiten.ColorScale {
	var cs ebiten.ColorScale
	cs.ScaleWithColor(c)
	return cs
}

// resolveDevices maps the saved device names to what to open, enumerating
// once per voice join. A saved device that is gone, or an enumeration
// failure, falls back to the system default with a notice; it never blocks
// the call.
func resolveDevices(savedIn, savedOut string) (in, out, notice string) {
	if savedIn == "" && savedOut == "" {
		return "", "", ""
	}
	ins, outs, err := audio.ListDevices()
	if err != nil {
		return "", "", fmt.Sprintf("audio devices: %v; using defaults", err)
	}
	in, n1 := state.ResolveDevice("input", savedIn, ins)
	out, n2 := state.ResolveDevice("output", savedOut, outs)
	if n1 != "" && n2 != "" {
		n1 += "; " + n2
	} else if n2 != "" {
		n1 = n2
	}
	return in, out, n1
}
