package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"maps"
	"math"
	"strings"
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
	src, sink     io.Closer
	mic           *audio.Meter
	spk           *audio.Meters
	notice        string // device fallback notice, shown on a successful join
	muted         bool   // the call starts muted
	rejoin        bool   // an automatic rejoin after a reconnect
	inDev, outDev string // the devices actually opened ("" = default)
	reqIn, reqOut string // the devices Settings asked for at join (before any fallback)
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

	inDev, outDev string    // devices the call is on ("" = default)
	reqIn, reqOut string    // devices last asked for in Settings (what a swap compares against)
	swapAt        time.Time // a debounced swap is due then (zero = none)
	swapping      bool      // a device swap is in flight
	swapAgain     bool      // Settings changed again meanwhile: swap once more after it
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

// clickVoice joins voice channel ch on the viewed session. Clicking the
// channel you are already in does nothing (leaving is the bar's and the
// panel's job), and so does any click while a join or leave is in flight.
func (g *game) clickVoice(ch string) {
	if g.cur().c == nil || g.vc.busy {
		return
	}
	switch g.cur().st.VoiceClickAction(ch) {
	case "join":
		g.joinCall(ch)
	case "open":
		g.cur().st.OpenCallView()
	}
}

// joinCall joins ch on the viewed session; see joinCallOn.
func (g *game) joinCall(ch string) { g.joinCallOn(g.cur(), ch, false, false) }

// joinCallOn leaves the current call (on whichever session holds it), then
// opens the devices and joins ch on s, starting muted if muted, all in one
// goroutine; the result arrives on voiceCh. rejoin marks an automatic rejoin
// after a reconnect, which words its status differently. Any call joined
// cancels the pending rejoins: the user's newest choice wins.
func (g *game) joinCallOn(s *session, ch string, muted, rejoin bool) {
	for _, o := range g.sess {
		o.rejoin.Cancel()
	}
	c := s.c
	g.endCallMedia()
	var oldC *client.Client // the call to leave first, if any
	if old := g.callSess(); old != nil {
		oldC = old.c
		old.st.SetInCall("")
	}
	oldSrc, oldSink := g.vc.src, g.vc.sink
	savedIn, savedOut := g.set.InputDevice, g.set.OutputDevice // read here: g.set belongs to the update goroutine
	set := g.set
	set.Volumes = maps.Clone(g.set.Volumes)
	micGain := g.set.MicGainPct()
	listDevices := g.listAudioDevices
	if listDevices == nil {
		listDevices = audio.ListDevices
	}
	open := g.openAudio
	if open == nil {
		open = openAudio
	}
	c.SetPushToTalk(g.set.PushToTalk()) // push-to-talk: closed until the key is held
	g.vc = voiceCall{busy: true, joining: ch, opKey: s.key, muted: muted}
	g.callKey = ""
	s.st.SetInCall("")
	s.st.Status = ""
	g.logf("joining voice %s on %s", ch, s.url)
	go func(key string, s *session) { // reads no session field
		res := voiceResult{key: key, s: s, c: c, channel: ch, muted: muted, rejoin: rejoin}
		if oldC != nil && oldC != c {
			leaveVoice(oldC)
		}
		leaveVoice(c) // no-op unless c holds a call
		closeAll(oldSrc, oldSink)
		inDev, outDev, notice, detail := resolveDevices(savedIn, savedOut, listDevices)
		if detail != nil {
			g.logf("audio device enumeration failed on %s: %v", s.url, detail)
		}
		src, mic, sink, spk, err := open(inDev, outDev)
		if err != nil {
			res.err = err
			g.voiceCh <- res
			return
		}
		applyVolumes(sink, set)
		applyMicGain(src, micGain)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.JoinVoiceMuted(ctx, ch, src, sink, muted); err != nil {
			closeAll(asCloser(src), asCloser(sink))
			res.err = err
			g.voiceCh <- res
			return
		}
		res.src, res.sink, res.mic, res.spk = asCloser(src), asCloser(sink), mic, spk
		res.notice = notice
		res.inDev, res.outDev = inDev, outDev
		res.reqIn, res.reqOut = savedIn, savedOut
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
	g.stepCamera()
	select {
	case r := <-g.voiceCh:
		g.applyVoice(r)
	default:
	}
	select {
	case r := <-g.swapCh:
		g.applyDevSwap(r)
	default:
	}
	g.stepSwap(time.Now())
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
			g.logf("voice leave failed on %s: %v", s.url, r.err)
			s.st.Status = "voice disconnect failed; try reconnecting"
		}
	case r.err != nil:
		g.logf("voice join failed on %s: %v", s.url, r.err)
		s.st.Status = state.VoiceJoinNotice(r.err)
		if r.rejoin {
			s.st.Status = state.RejoinFailNotice(r.err)
		}
	default:
		g.logf("joined voice %s on %s", r.channel, s.url)
		g.vc = voiceCall{start: time.Now(), muted: r.muted, src: r.src, sink: r.sink, mic: r.mic, spk: r.spk, inDev: r.inDev, outDev: r.outDev, reqIn: r.reqIn, reqOut: r.reqOut}
		g.callKey = s.key
		s.st.SetInCall(r.channel)
		status := r.notice
		if r.rejoin {
			status = strings.TrimSuffix(state.RejoinedNotice+"; "+r.notice, "; ")
		}
		if status != "" {
			s.st.Status = status
		}
		g.swapDevices() // a device changed in Settings while the join was in flight
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
	g.muteSeq++
	// Single slot, latest wins: drop a pending older value, then queue ours.
	// Only this goroutine fills the slot, so the send cannot block.
	select {
	case <-g.muteCh:
	default:
	}
	g.muteCh <- muteReq{c: s.c, token: s.c.CurrentMuteToken(), gen: g.muteSeq, start: g.vc.start, muted: g.vc.muted}
}

// muteReq is the desired mute state for one client.
type muteReq struct {
	c     *client.Client
	token client.MuteToken
	gen   uint64
	start time.Time
	muted bool
}

// muteFail reports a SetMuted that did not reach the server.
type muteFail struct {
	c     *client.Client
	gen   uint64
	start time.Time
	muted bool // the value that failed to send
	err   error
}

// muteWorker sends the latest desired mute state, in order, one at a time.
func (g *game) muteWorker() {
	for r := range g.muteCh {
		if err := r.c.SetMutedFor(r.token, r.muted); err != nil && !errors.Is(err, client.ErrNotInVoice) {
			g.postMuteFail(muteFail{c: r.c, gen: r.gen, start: r.start, muted: r.muted, err: err})
		}
	}
}

func (g *game) postMuteFail(f muteFail) {
	select {
	case g.muteFailCh <- f:
		return
	default:
	}
	// An older failure may fill the queue while the latest request fails.
	// Keep the latest result so the UI can reconcile to local capture state.
	select {
	case <-g.muteFailCh:
	default: // the game goroutine drained it between the two selects
	}
	select {
	case g.muteFailCh <- f:
	default: // only possible with a zero-capacity channel
	}
}

// applyMuteFail reconciles the UI to the privacy-preserving local capture state.
func (g *game) applyMuteFail() {
	select {
	case f := <-g.muteFailCh:
		s := g.callSess()
		if s == nil || s.c != f.c || g.muteSeq != f.gen || g.vc.start != f.start {
			return
		}
		g.vc.muted = true
		g.logf("mute update failed on %s: %v", s.url, f.err)
		s.st.Status = "microphone stays muted; try again"
	default:
	}
}

// barLayout is the pixel layout of the call bar, shared by update and draw.
type barLayout struct {
	y, h                             float64
	nameX                            float64
	muteX0, muteX1, leaveX0, leaveX1 float64
	shareX0, shareX1                 float64
	cameraX0, cameraX1               float64
	pttX0                            float64 // 0 = no push-to-talk tag
	leftX                            float64 // left edge of the controls
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
	right = l.shareX0 - pad
	cw := text.Advance(g.cameraLabel(), g.face)
	l.cameraX1, l.cameraX0 = right, right-cw-pad
	l.leftX = l.cameraX0
	if g.set.PushToTalk() {
		l.pttX0 = l.cameraX0 - text.Advance(pttTagWidest, g.face) - pad
		l.leftX = l.pttX0
	}
	return l
}

func (g *game) cameraLabel() string {
	if g.camera.on {
		return "stop camera"
	}
	return "camera"
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
	g.hits.Add(state.Hit{X0: l.cameraX0, Y0: y0, X1: l.cameraX1, Y1: y1, Kind: state.HitBarCamera})
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
	title := fitCallTitle(g.callTitle(), state.FormatElapsed(time.Since(g.vc.start)), int((l.leftX-pad-l.nameX)/gm.adv))
	g.drawText(dst, title, l.nameX, l.y, th.Bright)

	shareCol := th.FG
	switch {
	case !g.shareSupported():
		shareCol = th.Dim
	case g.share.on:
		shareCol = th.Bright
	}
	g.drawText(dst, g.shareLabel(), l.shareX0+pad/2, l.y, shareCol)
	cameraCol := th.FG
	if !g.cameraSupported() {
		cameraCol = th.Dim
	} else if g.camera.on {
		cameraCol = th.Bright
	}
	g.drawText(dst, g.cameraLabel(), l.cameraX0+pad/2, l.y, cameraCol)
	if tag := g.pttTag(); tag != "" {
		g.drawText(dst, tag, l.pttX0+pad/2, l.y, g.pttColor())
	}

	col, label := th.FG, muteLabel
	if g.vc.muted {
		col, label = th.Dim, mutedLabel
	}
	drawMic(dst, float32(l.muteX0), float32(l.y), float32(l.h), col, g.vc.muted)
	g.drawText(dst, label, l.muteX0+l.h+4, l.y, col)
	g.drawText(dst, leaveLabel, l.leaveX0+pad/2, l.y, th.FG)

	// Own mic level; flat while muted (nothing is being sent).
	lvl := g.vc.mic.Level()
	if g.vc.muted || (g.set.PushToTalk() && !g.transmitting) {
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
func resolveDevices(savedIn, savedOut string, list func() (inputs, outputs []string, err error)) (in, out, notice string, detail error) {
	if savedIn == "" && savedOut == "" {
		return "", "", "", nil
	}
	ins, outs, err := list()
	if err != nil {
		return "", "", "audio devices unavailable; using defaults", err
	}
	in, n1 := state.ResolveDevice("input", savedIn, ins)
	out, n2 := state.ResolveDevice("output", savedOut, outs)
	if n1 != "" && n2 != "" {
		n1 += "; " + n2
	} else if n2 != "" {
		n1 = n2
	}
	return in, out, n1, nil
}
