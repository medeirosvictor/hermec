package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"github.com/medeirosvictor/hermec/core/proto"
)

// AudioSource produces Opus frames. ReadOpusFrame blocks until the next
// frame (the source paces itself, normally one 20ms frame per call) and
// returns io.EOF (or any error) to end the stream.
type AudioSource interface {
	ReadOpusFrame() ([]byte, error)
}

// AudioSink receives Opus frames from other participants. fromFP is the
// sender's fingerprint; mixing and decoding are the sink's concern. It is
// called from one goroutine per remote track and must not block for long.
type AudioSink interface {
	WriteOpusFrame(fromFP string, frame []byte)
}

const (
	frameDuration = 20 * time.Millisecond
	maxResets     = 6 // consecutive media resets tolerated while joining

	// Fixed server wording (docs/protocol.md 4.13).
	msgOfferPending = "offer pending — answer first"
	msgResetHint    = "send a new rtc_offer"
)

// ErrNotInVoice is returned by voice operations outside a call.
var ErrNotInVoice = errors.New("client: not in a voice channel")

// ErrInVoice is returned by JoinVoice when a call is already active.
var ErrInVoice = errors.New("client: already in a voice channel")

// call is the client's state for one voice session. All negotiation state is
// owned by the actor goroutine (run); other goroutines talk to it via post.
// The read loop, pion callbacks and the public API only post or read the
// atomics below.
type call struct {
	c       *Client
	channel string
	src     AudioSource
	sink    AudioSink
	track   *webrtc.TrackLocalStaticSample
	api     *webrtc.API

	// actor queue
	qmu   sync.Mutex
	queue []func()
	wake  chan struct{}
	stop  chan struct{} // closed by teardown
	adone chan struct{} // closed when the actor has exited

	// lifecycle
	ready     chan struct{} // closed once: first answer applied and media connected
	readyOnce sync.Once
	failCh    chan struct{}
	failErr   error
	failOnce  sync.Once
	tdOnce    sync.Once
	tdDone    chan struct{}

	joined atomic.Bool // saw ourselves in a voice_state for channel
	muted  atomic.Bool
	muteCh chan struct{} // wakes the sender loop on unmute

	// receive-goroutine accounting
	tmu     sync.Mutex
	tclosed bool
	wg      sync.WaitGroup

	// actor-owned
	pc         *webrtc.PeerConnection
	offerOut   bool // our rtc_offer awaits its answer
	answered   bool // a remote description from our offer was applied
	pending    []webrtc.ICECandidateInit
	resets     int
	superseded int // our offers abandoned while the server may still reject them
	senderOn   bool
	retryGen   uint64
	glareSeen  bool // the server already rejected our current offer as glare
}

func (c *Client) newAPI() *webrtc.API {
	me := &webrtc.MediaEngine{}
	_ = me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   48000,
			Channels:    2,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio)
	ir := &interceptor.Registry{}
	_ = webrtc.RegisterDefaultInterceptors(me, ir)
	se := webrtc.SettingEngine{}
	if c.loopbackICE.Load() {
		se.SetIncludeLoopbackCandidate(true)
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
}

// SetLoopbackICE makes future voice calls gather loopback ICE candidates.
// For tests and same-host setups; call before JoinVoice.
func (c *Client) SetLoopbackICE(on bool) { c.loopbackICE.Store(on) }

// VoiceChannel returns the voice channel of the active call, or "".
func (c *Client) VoiceChannel() string {
	cl := c.getCall()
	if cl == nil || !cl.joined.Load() {
		return ""
	}
	return cl.channel
}

func (c *Client) getCall() *call {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.call
}

// JoinVoice joins voice channel, negotiates the media connection and returns
// once audio can flow (answer applied, transport connected). src is read for
// outgoing frames, sink receives frames from others. If ctx ends or the
// negotiation fails the call is torn down and the client stays usable.
func (c *Client) JoinVoice(ctx context.Context, channel string, src AudioSource, sink AudioSink) error {
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
	}, "audio-"+c.fingerprint, c.fingerprint)
	if err != nil {
		return err
	}
	cl := &call{
		c: c, channel: channel, src: src, sink: sink, track: track, api: c.newAPI(),
		wake: make(chan struct{}, 1), stop: make(chan struct{}), adone: make(chan struct{}),
		ready: make(chan struct{}), failCh: make(chan struct{}), tdDone: make(chan struct{}),
		muteCh: make(chan struct{}, 1),
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	if c.call != nil {
		c.mu.Unlock()
		return ErrInVoice
	}
	c.call = cl
	c.mu.Unlock()
	go cl.run()

	if err := c.send(proto.TypeVoiceJoin, proto.VoiceJoin{Channel: channel}); err != nil {
		c.abortCall(cl, false)
		return err
	}
	select {
	case <-cl.ready:
		return nil
	case <-cl.failCh:
		c.abortCall(cl, true)
		return cl.failErr
	case <-cl.tdDone: // connection died: the read loop tore the call down
		c.abortCall(cl, false)
		return ErrClosed
	case <-ctx.Done():
		c.abortCall(cl, true)
		return ctx.Err()
	}
}

// abortCall tears a call down; if leave, it also tells the server.
func (c *Client) abortCall(cl *call, leave bool) {
	if leave {
		_ = c.send(proto.TypeVoiceLeave, proto.VoiceLeave{})
	}
	cl.teardown()
	c.mu.Lock()
	if c.call == cl {
		c.call = nil
	}
	c.mu.Unlock()
}

// LeaveVoice leaves the active call and releases all media resources. It is
// a no-op outside a call.
func (c *Client) LeaveVoice(ctx context.Context) error {
	cl := c.getCall()
	if cl == nil {
		return nil
	}
	err := c.send(proto.TypeVoiceLeave, proto.VoiceLeave{})
	cl.teardownCtx(ctx)
	c.mu.Lock()
	if c.call == cl {
		c.call = nil
	}
	c.mu.Unlock()
	if errors.Is(err, ErrClosed) {
		return nil
	}
	return err
}

// SetMuted tells the server and pauses reading from the source while muted.
func (c *Client) SetMuted(m bool) error {
	cl := c.getCall()
	if cl == nil {
		return ErrNotInVoice
	}
	cl.muted.Store(m)
	if !m {
		select {
		case cl.muteCh <- struct{}{}:
		default:
		}
	}
	return c.send(proto.TypeVoiceMute, proto.VoiceMute{Muted: m})
}

// ---- read-loop entry points (never block) ----

// onVoiceState is called by the read loop for every voice_state.
func (c *Client) onVoiceState(st proto.VoiceState) {
	cl := c.getCall()
	if cl == nil || st.Channel != cl.channel {
		return
	}
	for _, m := range st.Members {
		if m.Fingerprint == c.fingerprint {
			if cl.joined.CompareAndSwap(false, true) {
				cl.post(cl.startOffer)
			}
			return
		}
	}
}

// onRTC routes an rtc_* envelope to the active call. Reports whether a call
// consumed it.
func (c *Client) onRTC(env proto.Envelope) {
	cl := c.getCall()
	if cl == nil {
		return
	}
	cl.post(func() { cl.handleRTC(env) })
}

// onError offers a server error to the active call. It reports whether the
// call consumed it (media recovery or a failed join).
func (c *Client) onError(em proto.ErrorMsg) bool {
	cl := c.getCall()
	if cl == nil {
		return false
	}
	if em.Code == "bad_request" {
		switch {
		case em.Message == msgOfferPending:
			cl.post(cl.onGlare)
			return true
		case isResetError(em.Message):
			cl.post(cl.reset)
			return true
		}
	}
	if !cl.joined.Load() {
		cl.fail(&ServerError{em.Code, em.Message})
		return true
	}
	return false
}

func isResetError(msg string) bool {
	return strings.Contains(msg, msgResetHint) ||
		strings.HasPrefix(msg, "invalid rtc_offer") ||
		strings.HasPrefix(msg, "cannot answer rtc_offer") ||
		strings.HasPrefix(msg, "rtc_offer in unexpected signaling state")
}

// ---- actor ----

func (cl *call) post(fn func()) {
	select {
	case <-cl.stop:
		return
	default:
	}
	cl.qmu.Lock()
	cl.queue = append(cl.queue, fn)
	cl.qmu.Unlock()
	select {
	case cl.wake <- struct{}{}:
	default:
	}
}

func (cl *call) pop() func() {
	cl.qmu.Lock()
	defer cl.qmu.Unlock()
	if len(cl.queue) == 0 {
		return nil
	}
	fn := cl.queue[0]
	cl.queue[0] = nil
	cl.queue = cl.queue[1:]
	return fn
}

func (cl *call) run() {
	defer close(cl.adone)
	for {
		select {
		case <-cl.stop:
			return
		case <-cl.wake:
			for fn := cl.pop(); fn != nil; fn = cl.pop() {
				select {
				case <-cl.stop:
					return
				default:
				}
				fn()
			}
		}
	}
}

func (cl *call) fail(err error) {
	cl.failOnce.Do(func() {
		cl.failErr = err
		close(cl.failCh)
	})
}

func (cl *call) teardown() { cl.teardownCtx(context.Background()) }

func (cl *call) teardownCtx(ctx context.Context) {
	cl.tdOnce.Do(func() {
		go func() {
			defer close(cl.tdDone)
			close(cl.stop)
			<-cl.adone
			cl.tmu.Lock()
			cl.tclosed = true
			cl.tmu.Unlock()
			if cl.pc != nil {
				_ = cl.pc.Close()
				cl.pc = nil
			}
			cl.wg.Wait()
		}()
	})
	select {
	case <-cl.tdDone:
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
}

// newPC builds a fresh PeerConnection with our audio track attached and the
// receive/connection callbacks wired. Actor only.
func (cl *call) newPC() error {
	pc, err := cl.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	snd, err := pc.AddTrack(cl.track)
	if err != nil {
		_ = pc.Close()
		return err
	}
	pc.OnICECandidate(func(ci *webrtc.ICECandidate) {
		if ci == nil {
			return
		}
		raw, err := json.Marshal(ci.ToJSON())
		if err != nil {
			return
		}
		cl.post(func() {
			if cl.pc == pc {
				_ = cl.c.send(proto.TypeRTCCandidate, proto.RTCCandidate{Candidate: string(raw)})
			}
		})
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		cl.post(func() {
			if cl.pc != pc {
				return
			}
			switch st {
			case webrtc.PeerConnectionStateConnected:
				cl.checkReady()
			case webrtc.PeerConnectionStateFailed:
				select {
				case <-cl.ready:
				default:
					cl.fail(errors.New("client: voice transport failed"))
				}
			}
		})
	})
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		cl.tmu.Lock()
		if cl.tclosed {
			cl.tmu.Unlock()
			return
		}
		cl.wg.Add(1)
		cl.tmu.Unlock()
		go cl.readTrack(remote)
	})
	cl.tmu.Lock()
	if cl.tclosed {
		cl.tmu.Unlock()
		_ = pc.Close()
		return errors.New("client: call closed")
	}
	cl.wg.Add(1)
	cl.tmu.Unlock()
	go func() { // sender-side RTCP must be drained for interceptors
		defer cl.wg.Done()
		buf := make([]byte, 1500)
		for {
			if _, _, err := snd.Read(buf); err != nil {
				return
			}
		}
	}()
	cl.pc = pc
	cl.offerOut, cl.answered, cl.pending = false, false, nil
	cl.startSender()
	return nil
}

func (cl *call) startSender() {
	if cl.senderOn {
		return
	}
	cl.tmu.Lock()
	if cl.tclosed {
		cl.tmu.Unlock()
		return
	}
	cl.senderOn = true
	cl.wg.Add(1)
	cl.tmu.Unlock()
	go cl.sendLoop()
}

func (cl *call) sendLoop() {
	defer cl.wg.Done()
	for {
		for cl.muted.Load() {
			select {
			case <-cl.stop:
				return
			case <-cl.muteCh:
			}
		}
		select {
		case <-cl.stop:
			return
		default:
		}
		frame, err := cl.src.ReadOpusFrame()
		if err != nil {
			return // io.EOF or a dead source ends our stream
		}
		if cl.muted.Load() {
			continue
		}
		_ = cl.track.WriteSample(media.Sample{Data: frame, Duration: frameDuration})
	}
}

func (cl *call) readTrack(remote *webrtc.TrackRemote) {
	defer cl.wg.Done()
	fp := remote.StreamID() // the server sets StreamID to the publisher's fingerprint
	for {
		pkt, _, err := remote.ReadRTP()
		if err != nil {
			return
		}
		if len(pkt.Payload) == 0 {
			continue
		}
		frame := make([]byte, len(pkt.Payload))
		copy(frame, pkt.Payload)
		cl.sink.WriteOpusFrame(fp, frame)
	}
}

// discardPC drops the current PeerConnection and all negotiation state tied
// to it. Actor only.
func (cl *call) discardPC() {
	cl.retryGen++ // cancels any scheduled (re-)offer for the old PC
	if cl.pc != nil {
		pc := cl.pc
		cl.pc = nil
		_ = pc.Close() // its track readers end; teardown joins them
	}
	cl.offerOut, cl.answered, cl.pending = false, false, nil
}

// startOffer builds a PC and sends our offer. Actor only.
func (cl *call) startOffer() {
	if cl.pc == nil {
		if err := cl.newPC(); err != nil {
			cl.fail(err)
			return
		}
	}
	pc := cl.pc
	offer, err := pc.CreateOffer(nil)
	if err == nil {
		err = pc.SetLocalDescription(offer)
	}
	if err != nil {
		cl.fail(fmt.Errorf("client: create offer: %w", err))
		return
	}
	cl.offerOut, cl.glareSeen = true, false
	if err := cl.c.send(proto.TypeRTCOffer, proto.RTCOffer{SDP: pc.LocalDescription().SDP}); err != nil {
		cl.fail(err)
	}
}

// reset discards media state and offers again from scratch. Anything the
// server had outstanding for the old state is deliberately never answered.
func (cl *call) reset() {
	cl.resets++
	if cl.resets > maxResets {
		cl.fail(errors.New("client: voice negotiation keeps failing"))
		return
	}
	cl.discardPC()
	cl.startOffer()
}

// onGlare handles "offer pending": the server has an outstanding offer of
// its own. Server offers are answered the moment they arrive, so normally
// ours was the stale one and was already superseded in handleOffer; otherwise
// back off briefly (the server offer is in flight, or will time out and be
// reset) and offer again.
func (cl *call) onGlare() {
	if cl.superseded > 0 {
		cl.superseded--
		return
	}
	if cl.pc == nil || !cl.offerOut {
		return
	}
	cl.glareSeen = true
	cl.retryGen++
	gen, pc := cl.retryGen, cl.pc
	time.AfterFunc(250*time.Millisecond, func() {
		cl.post(func() {
			if cl.retryGen == gen && cl.pc == pc && cl.offerOut {
				cl.reset()
			}
		})
	})
}

func (cl *call) checkReady() {
	if cl.answered && cl.pc != nil && cl.pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
		cl.readyOnce.Do(func() { close(cl.ready) })
	}
}

func (cl *call) handleRTC(env proto.Envelope) {
	switch env.Type {
	case proto.TypeRTCAnswer:
		var m proto.RTCAnswer
		if json.Unmarshal(env.Data, &m) != nil || cl.pc == nil || !cl.offerOut {
			return // stale or malformed: never apply an answer to a superseded offer
		}
		if err := cl.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP}); err != nil {
			cl.reset()
			return
		}
		cl.offerOut, cl.answered = false, true
		cl.retryGen++
		cl.flushPending()
		cl.checkReady()
	case proto.TypeRTCOffer:
		var m proto.RTCOffer
		if json.Unmarshal(env.Data, &m) != nil {
			return
		}
		cl.handleOffer(m.SDP)
	case proto.TypeRTCCandidate:
		var m proto.RTCCandidate
		var ci webrtc.ICECandidateInit
		if json.Unmarshal(env.Data, &m) != nil || json.Unmarshal([]byte(m.Candidate), &ci) != nil || cl.pc == nil {
			return
		}
		if cl.pc.RemoteDescription() == nil {
			if len(cl.pending) < 64 {
				cl.pending = append(cl.pending, ci)
			}
			return
		}
		_ = cl.pc.AddICECandidate(ci)
	}
}

func (cl *call) flushPending() {
	for _, ci := range cl.pending {
		_ = cl.pc.AddICECandidate(ci)
	}
	cl.pending = nil
}

// handleOffer answers a server-initiated offer. If our own offer is still
// outstanding (glare) it is abandoned in favour of the server's: fresh PC,
// answer first, then a new offer of ours.
func (cl *call) handleOffer(sdp string) {
	reoffer := false
	if cl.pc != nil && cl.offerOut {
		cl.discardPC()
		if !cl.glareSeen {
			cl.superseded++ // the server's rejection of our old offer is still coming
		}
		reoffer = true
	}
	if cl.pc == nil {
		if !reoffer && !cl.joined.Load() {
			return
		}
		if !reoffer {
			// Media was reset locally; this offer belongs to state we threw
			// away. Do not answer it; the server will reset and we re-offer.
			return
		}
		if err := cl.newPC(); err != nil {
			cl.fail(err)
			return
		}
	}
	pc := cl.pc
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
		cl.reset()
		return
	}
	cl.flushPending()
	answer, err := pc.CreateAnswer(nil)
	if err == nil {
		err = pc.SetLocalDescription(answer)
	}
	if err != nil {
		cl.reset()
		return
	}
	if err := cl.c.send(proto.TypeRTCAnswer, proto.RTCAnswer{SDP: pc.LocalDescription().SDP}); err != nil {
		cl.fail(err)
		return
	}
	if reoffer {
		cl.answered = false // our own offer is still to come
		// Give the server a moment to reject the answer (media reset, which
		// triggers its own fresh offer and cancels this one) before we offer;
		// two offers in flight would leave their answers ambiguous.
		cl.retryGen++
		gen := cl.retryGen
		time.AfterFunc(300*time.Millisecond, func() {
			cl.post(func() {
				if cl.retryGen == gen && cl.pc == pc && !cl.offerOut {
					cl.startOffer()
				}
			})
		})
		return
	}
	cl.checkReady()
}
