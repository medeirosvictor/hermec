package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"math"
	"time"
	"unicode/utf8"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/audio"
	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/ui/state"
)

// voiceResult is the outcome of a background join or leave. key, s and c
// identify the session (instance and client) the op was started for: the
// result applies only if g.sess[key] is still s and still has client c.
type voiceResult struct {
	key     string
	s       *session
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
// the client). Only touched on the Ebiten update goroutine. The call belongs
// to the session g.callKey; an op in flight belongs to opKey.
type voiceCall struct {
	busy    bool   // a join/leave goroutine is in flight
	joining string // channel being joined while busy
	opKey   string // session key the in-flight op was started for
	start   time.Time
	muted   bool
	src     io.Closer
	sink    io.Closer
	mic     *audio.Meter
	spk     *audio.Meters
}

// audioOpener opens the capture and playback devices for a call. g.openAudio
// is a test seam; nil means openAudio (the real devices).
type audioOpener func(in, out string) (client.AudioSource, *audio.Meter, client.AudioSink, *audio.Meters, error)

// openAudio opens the real devices; on failure nothing stays open.
func openAudio(in, out string) (client.AudioSource, *audio.Meter, client.AudioSink, *audio.Meters, error) {
	src, mic, err := audio.Capture(in)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("microphone unavailable: %w", err)
	}
	sink, spk, err := audio.Playback(out)
	if err != nil {
		closeAll(asCloser(src))
		return nil, nil, nil, nil, fmt.Errorf("audio output unavailable: %w", err)
	}
	return src, mic, sink, spk, nil
}

// callSess is the session holding the call, or nil with no call (also while
// a join is still in flight).
func (g *game) callSess() *session {
	if g.callKey == "" {
		return nil
	}
	return g.sess[g.callKey]
}

// barVisible reports whether the call bar is shown: a call exists or is being
// joined, whatever server (or the connect scene) is viewed.
func (g *game) barVisible() bool { return g.callSess() != nil || g.vc.joining != "" }

// callTitle is the bar title of the current call ("" with none).
func (g *game) callTitle() string {
	s := g.callSess()
	if s == nil {
		return ""
	}
	return state.CallTitle(s.st.InCall, serverTitle(g.servers, g.callKey), g.callKey != g.curKey)
}

// fitCallTitle joins name and elapsed within cols runes, ellipsizing the
// name so the elapsed time always stays visible.
func fitCallTitle(name, elapsed string, cols int) string {
	room := cols - utf8.RuneCountInString(elapsed) - 2
	if room < 1 {
		return elapsed
	}
	return state.Ellipsize(name, room) + "  " + elapsed
}

func closeAll(cs ...io.Closer) {
	for _, c := range cs {
		if c != nil {
			_ = c.Close()
		}
	}
}

// clickVoice toggles the call on the viewed session: join ch, or leave if it
// is the current call.
func (g *game) clickVoice(ch string) {
	if g.cur().c == nil || g.vc.busy {
		return
	}
	if g.cur().st.VoiceClickAction(ch) == "leave" {
		g.leaveCall()
		return
	}
	g.joinCall(ch)
}

// joinCall leaves the current call (on whichever session holds it), then
// opens the devices and joins ch on the viewed session, all in one goroutine;
// the result arrives on voiceCh.
func (g *game) joinCall(ch string) {
	s := g.cur()
	c := s.c
	g.endCallMedia()
	var oldC *client.Client // the call to leave first, if any
	if old := g.callSess(); old != nil {
		oldC = old.c
		old.st.SetInCall("")
	}
	oldSrc, oldSink := g.vc.src, g.vc.sink
	savedIn, savedOut := g.set.InputDevice, g.set.OutputDevice // read here: g.set belongs to the update goroutine
	open := g.openAudio
	if open == nil {
		open = openAudio
	}
	g.vc = voiceCall{busy: true, joining: ch, opKey: s.key}
	g.callKey = ""
	s.st.SetInCall("")
	s.st.Status = ""
	g.logf("joining voice %s on %s", ch, s.url)
	go func(key string, s *session) { // reads no session field
		res := voiceResult{key: key, s: s, c: c, channel: ch}
		if oldC != nil && oldC != c {
			leaveVoice(oldC)
		}
		leaveVoice(c) // no-op unless c holds a call
		closeAll(oldSrc, oldSink)
		inDev, outDev, notice := resolveDevices(savedIn, savedOut)
		src, mic, sink, spk, err := open(inDev, outDev)
		if err != nil {
			res.err = err
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
	}(s.key, s)
}

// leaveVoice leaves c's call, bounded so a dead connection cannot stall a join.
func leaveVoice(c *client.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.LeaveVoice(ctx)
}

func asCloser(v any) io.Closer {
	c, _ := v.(io.Closer)
	return c
}

// leaveCall leaves the call in the background, on the session that holds it,
// and frees the devices.
func (g *game) leaveCall() {
	s := g.callSess()
	if s == nil {
		return
	}
	if s.c == nil { // no connection to tell
		g.dropVoice()
		return
	}
	c := s.c
	g.endCallMedia()
	src, sink := g.vc.src, g.vc.sink
	g.vc = voiceCall{busy: true, opKey: s.key}
	g.callKey = ""
	s.st.SetInCall("")
	g.logf("leaving voice on %s", s.url)
	go func(key string, s *session) { // reads no session field
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := c.LeaveVoice(ctx)
		closeAll(src, sink)
		g.voiceCh <- voiceResult{key: key, s: s, c: c, leave: true, err: err}
	}(s.key, s)
}

// dropVoice ends the call without talking to the server (the connection is
// gone or the call already died): it frees the devices, clears the call
// session's InCall and callKey. An op in flight is left to report on voiceCh.
// Safe with no call.
func (g *game) dropVoice() {
	g.endCallMedia()
	src, sink := g.vc.src, g.vc.sink
	if s := g.callSess(); s != nil {
		s.st.SetInCall("")
	}
	g.callKey = ""
	g.vc = voiceCall{busy: g.vc.busy, joining: g.vc.joining, opKey: g.vc.opKey}
	if src != nil || sink != nil {
		go closeAll(src, sink)
	}
}

// endVoiceFor ends whatever voice the session at key has: its call (devices
// freed) and the bar's "joining" for an op in flight on it, whose result will
// arrive stale (its client is gone) and be discarded. Other sessions' voice is
// untouched.
func (g *game) endVoiceFor(key string) {
	if key == "" {
		return
	}
	if g.callKey == key {
		g.dropVoice()
	}
	if g.vc.busy && g.vc.opKey == key {
		g.vc.joining = ""
	}
}

// updateVoice consumes background results and notices a dead call.
func (g *game) updateVoice() {
	g.applyMuteFail()
	g.stepShare()
	select {
	case r := <-g.voiceCh:
		g.applyVoice(r)
	default:
	}
	// Poll: the client reports a dead call as VoiceChannel() == "".
	if s := g.callSess(); s != nil && s.c != nil && !g.vc.busy && s.st.InCall != "" && s.c.VoiceChannel() == "" {
		g.logf("voice call on %s ended", s.url)
		g.dropVoice()
		s.st.Status = "voice call ended"
	}
}

// applyVoice applies a finished join or leave to the session it was started
// for, whatever is viewed now.
func (g *game) applyVoice(r voiceResult) {
	g.vc.busy = false
	g.vc.joining = ""
	g.vc.opKey = ""
	s := g.sess[r.key]
	switch {
	case s == nil || s != r.s || s.c != r.c: // session gone, replaced or reconnected underneath: discard
		closeAll(r.src, r.sink)
	case r.leave:
		if r.err != nil {
			s.st.Status = fmt.Sprintf("voice leave failed: %v", r.err)
		}
	case r.err != nil:
		g.logf("voice join failed on %s: %v", s.url, r.err)
		s.st.Status = state.VoiceJoinNotice(r.err)
	default:
		g.logf("joined voice %s on %s", r.channel, s.url)
		g.vc = voiceCall{start: time.Now(), src: r.src, sink: r.sink, mic: r.mic, spk: r.spk}
		g.callKey = s.key
		s.st.SetInCall(r.channel)
		if r.notice != "" {
			s.st.Status = r.notice
		}
	}
}

// toggleMute flips own mute (Ctrl+M or the bar's mute button) on the call,
// wherever it is.
func (g *game) toggleMute() {
	s := g.callSess()
	if s == nil || s.c == nil || g.vc.busy || s.st.InCall == "" {
		return
	}
	g.vc.muted = !g.vc.muted
	// Single slot, latest wins: drop a pending older value, then queue ours.
	// Only this goroutine fills the slot, so the send cannot block.
	select {
	case <-g.muteCh:
	default:
	}
	g.muteCh <- muteReq{c: s.c, muted: g.vc.muted}
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
		s := g.callSess()
		if s == nil {
			s = g.cur()
		}
		s.st.Status = fmt.Sprintf("mute failed: %v", f.err)
	default:
	}
}

// barLayout is the pixel layout of the call bar, shared by update and draw.
type barLayout struct {
	y, h                             float64
	nameX                            float64
	muteX0, muteX1, leaveX0, leaveX1 float64
	shareX0, shareX1                 float64
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
	x := gm.barR.x0 + pad
	l.nameX = x + gm.lh + 4 // after the speaker icon
	// Right-aligned controls: [mic mute] [leave] meter.
	right := gm.barR.x1 - pad - signalW(gm.lh) - pad // signal bars sit at the far right
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
	right = l.muteX0 - pad
	sw := text.Advance(g.shareLabel(), g.face)
	l.shareX1, l.shareX0 = right, right-sw-pad
	return l
}

// inCall reports whether a settled call exists (bar controls are live).
func (g *game) inCall() bool {
	s := g.callSess()
	return s != nil && !g.vc.busy && s.st.InCall != ""
}

// updateBar handles Ctrl+M and the bar's buttons; they act on the call
// session from any scene but settings.
func (g *game) updateBar(gm geometry) {
	if !g.inCall() {
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyM) &&
		(ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight)) {
		g.toggleMute()
	}
	if !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}
	h, ok := g.hitAt()
	if !ok {
		return
	}
	g.clickBar(h)
}

// addBarHits registers the call-bar buttons; they are live exactly when
// updateBar would act on them.
func (g *game) addBarHits(gm geometry) {
	if !g.inCall() {
		return
	}
	l := g.barLayout(gm)
	// While the bar slides, targets follow the drawn strip (clipped to it).
	y0, y1 := math.Max(l.y, gm.barR.y0), math.Min(l.y+l.h, gm.barR.y1)
	if y1 <= y0 {
		return
	}
	g.hits.Add(state.Hit{X0: l.shareX0, Y0: y0, X1: l.shareX1, Y1: y1, Kind: state.HitBarShare})
	g.hits.Add(state.Hit{X0: l.muteX0, Y0: y0, X1: l.muteX1, Y1: y1, Kind: state.HitBarMute})
	g.hits.Add(state.Hit{X0: l.leaveX0, Y0: y0, X1: l.leaveX1, Y1: y1, Kind: state.HitBarLeave})
}

func (g *game) drawBar(screen *ebiten.Image, gm geometry) {
	th := g.th
	l := g.barLayout(gm)
	dst := clip(screen, gm.barR.x0, gm.barR.y0, gm.barR.x1, gm.barR.y1)

	drawSpeaker(dst, float32(gm.barR.x0+pad), float32(l.y), float32(l.h), th.Bright)
	if cs := g.callSess(); cs == nil || cs.st.InCall == "" { // still joining, or sliding out after a leave
		if g.vc.joining != "" {
			what := state.CallTitle(g.vc.joining, serverTitle(g.servers, g.vc.opKey), g.vc.opKey != g.curKey)
			g.drawText(dst, "joining "+what+"...", l.nameX, l.y, th.Dim)
		}
		return
	}
	title := fitCallTitle(g.callTitle(), state.FormatElapsed(time.Since(g.vc.start)), int((l.shareX0-pad-l.nameX)/gm.adv))
	g.drawText(dst, title, l.nameX, l.y, th.Bright)

	shareCol := th.FG
	switch {
	case !g.shareSupported():
		shareCol = th.Dim
	case g.share.on:
		shareCol = th.Bright
	}
	g.drawText(dst, g.shareLabel(), l.shareX0+pad/2, l.y, shareCol)

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
	sr := g.signalRect(gm)
	g.drawSignal(dst, sr.x0, sr.y0, l.h, g.q.grade)
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
