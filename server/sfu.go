package server

import (
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/proto"
)

// The SFU: one ephemeral voiceSession per occupied voice channel.
//
// Concurrency shape (this is the part to read before changing anything):
//
//   - A session is an actor. One goroutine (loop) runs queued closures one at
//     a time and is the ONLY goroutine that touches peers, PeerConnections,
//     senders and negotiation state. Those fields therefore need no lock.
//   - post() is how everything else talks to the actor. It appends to an
//     unbounded queue under a tiny private mutex and never blocks, never
//     calls out, and never takes chMu, so it is safe to call from code that
//     holds chMu (membership transitions) and from pion's own callback
//     goroutines (OnTrack, OnICECandidate, OnConnectionStateChange).
//   - Lock order: chMu -> session.qmu (post) is the only edge between the
//     membership layer and the session. The actor never takes chMu, and
//     never holds qmu while running a closure, so there is no path back.
//     Outbound signaling uses conn.enqueue (non-blocking, sendMu only).
//   - Slow or blocking pion work (SetRemoteDescription, PeerConnection.Close)
//     runs on the actor, never under chMu.
//   - Per-publisher-track forwarders are separate goroutines (hot path). They
//     share only the forwarder's own mutex-guarded sink list with the actor.
//
// Lifecycle: created under chMu on the first join of an empty channel and
// stopped (stop() posts a final teardown closure) under chMu on the last
// leave. Teardown closes every PeerConnection and joins every goroutine the
// session started, then the actor exits. The actor is counted in Server.wg,
// so Shutdown joins it too.
type voiceSession struct {
	srv     *Server
	channel string
	api     *webrtc.API

	qmu    sync.Mutex
	queue  []func()
	closed bool // set by stop(); later posts are dropped
	wake   chan struct{}
	done   chan struct{} // closed when the actor goroutine has fully exited

	// Actor-owned.
	peers    map[*conn]*peer
	finished bool
	wg       sync.WaitGroup // forwarders and RTCP readers

	relayedBytes atomic.Uint64 // audio payload bytes written to subscribers
	videoBytes   atomic.Uint64 // video payload bytes written to subscribers
	writeErrors  atomic.Uint64 // transient subscriber write errors (not pruned)
	statsAt      time.Time
	statsBytes   uint64
	statsVideo   uint64
}

// answerTimeout bounds how long a server-initiated offer may go unanswered
// before the peer's media state is reset. A variable so tests can shorten it.
var answerTimeout = 15 * time.Second

// maxInactiveLines is how many retired (inactive) audio m-lines a subscriber
// PeerConnection may accumulate before its media is rebuilt from scratch. At
// the max_voice cap of 50 an offer carrying 32 retired lines plus the live
// ones stays under proto.MaxMessageSize (64 KiB); at the default 25 it is
// about 38 KB. A higher value rebuilds the room less often.
const maxInactiveLines = 32

// errOfferPending is the fixed glare message: a client rtc_offer arrived while
// a server offer is outstanding. See docs/protocol.md 4.13.
const errOfferPending = "offer pending — answer first"

// statsInterval is how often an active session logs its participant count and
// relay rate (spec 3.1). A variable so tests can shorten it.
var statsInterval = time.Minute

// peer is one participant's media state. Actor-owned.
type peer struct {
	c       *conn
	pc      *webrtc.PeerConnection
	pending []webrtc.ICECandidateInit // candidates that arrived before the remote description

	negotiating bool        // a server-initiated offer awaits its answer
	dirty       bool        // topology changed while busy; renegotiate when stable
	offerGen    uint64      // generation of the outstanding/last server offer
	offerTimer  *time.Timer // answer deadline for the outstanding offer

	fwds    []*forwarder                     // tracks this peer publishes
	senders map[*forwarder]*webrtc.RTPSender // tracks this peer receives
	retired int                              // inactive audio m-lines left on pc by removed senders

	// Screen share. watchers are the peers receiving this peer's video;
	// watching is the sharer this peer receives video from.
	watchers  map[*peer]bool
	watching  *peer
	kfWanted  bool      // a keyframe is owed to this viewer once an answer covering its video lands
	kfInOffer bool      // the outstanding server offer includes this viewer's video
	lastPLI   time.Time // last PLI written to this peer's PC (sharer side)

	// Pion never frees a transceiver, so a viewer gets exactly one video
	// transceiver (vsender) for its lifetime; switching or restarting a
	// watch swaps the track on it (ReplaceTrack) instead of adding m-lines.
	vsender *webrtc.RTPSender
	vfwd    *forwarder // the video forwarder currently feeding vsender
	vout    *outStream // keeps vsender's seq/timestamps continuous across switches
	vrw     *rewrite   // numbering of the source currently feeding vsender

	sharing atomic.Bool // this peer is sharing (set by the actor, read by forwarders)
}

// outStream is the outgoing RTP numbering of one viewer's video sender.
type outStream struct {
	mu      sync.Mutex
	started bool
	seq     uint16
	ts      uint32
}

// rewrite maps one forwarder's packets onto an outStream. The numbering its
// first packet gets (bseq, bts) is fixed when the rewrite is created, so the
// viewer can be told the boundary before any packet exists; the offsets are
// derived from it on the first packet. Creating a new rewrite retires the
// previous one, so packets of the old source still in a forwarder's snapshot
// are dropped instead of being numbered after the boundary.
type rewrite struct {
	out     *outStream
	bseq    uint16
	bts     uint32
	retired bool
	synced  bool
	seqOff  uint16
	tsOff   uint32
}

// attach (re)starts the stream for a new source: it retires old (may be nil)
// and returns the rewrite for the new source. Its first packet will be
// numbered rw.bseq.
func (o *outStream) attach(old *rewrite) *rewrite {
	o.mu.Lock()
	defer o.mu.Unlock()
	if old != nil {
		old.retired = true
	}
	if !o.started {
		o.started = true
		o.seq, o.ts = uint16(rand.Uint32()), rand.Uint32()
	}
	return &rewrite{out: o, bseq: o.seq + 1, bts: o.ts + 3000}
}

// retire stops r from numbering any further packet.
func (r *rewrite) retire() {
	r.out.mu.Lock()
	r.retired = true
	r.out.mu.Unlock()
}

// apply numbers pkt, or returns nil when r was retired.
func (r *rewrite) apply(pkt *rtp.Packet) *rtp.Packet {
	r.out.mu.Lock()
	defer r.out.mu.Unlock()
	if r.retired {
		return nil
	}
	if !r.synced {
		r.synced = true
		r.seqOff = r.bseq - pkt.SequenceNumber
		r.tsOff = r.bts - pkt.Timestamp
	}
	c := &rtp.Packet{Header: pkt.Header.Clone(), Payload: pkt.Payload}
	c.SequenceNumber += r.seqOff
	c.Timestamp += r.tsOff
	r.out.started, r.out.seq, r.out.ts = true, c.SequenceNumber, c.Timestamp
	return c
}

// forwarder copies one publisher track to every subscriber's local track.
type forwarder struct {
	pub    *peer
	remote *webrtc.TrackRemote
	video  bool // VP8 screen share; fanned out only to the publisher's watchers

	mu    sync.Mutex
	sinks map[*peer]sink
	dead  bool
}

// sink is one subscriber's outgoing copy of a track. pc is immutable and
// safe to query from the forwarder goroutine (ConnectionState is thread-safe).
type sink struct {
	t  *webrtc.TrackLocalStaticRTP
	pc *webrtc.PeerConnection
	rw *rewrite // video only; nil for audio
}

func (f *forwarder) isDead() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dead
}

func (f *forwarder) kill() {
	f.mu.Lock()
	f.dead = true
	f.sinks = nil
	f.mu.Unlock()
}

func (f *forwarder) addSink(p *peer, t *webrtc.TrackLocalStaticRTP, pc *webrtc.PeerConnection, rw *rewrite) {
	f.mu.Lock()
	if !f.dead {
		f.sinks[p] = sink{t, pc, rw}
	}
	f.mu.Unlock()
}

func (f *forwarder) removeSink(p *peer) {
	f.mu.Lock()
	delete(f.sinks, p)
	f.mu.Unlock()
}

type sinkRef struct {
	p *peer
	sink
}

// newVoiceSession builds a session and starts its actor. Caller holds chMu
// (it only spawns a goroutine; nothing here blocks).
func (s *Server) newVoiceSession(ch string) *voiceSession {
	vs := &voiceSession{
		srv:     s,
		channel: ch,
		api:     s.newRTCAPI(),
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		peers:   make(map[*conn]*peer),
		statsAt: time.Now(),
	}
	s.wg.Add(1)
	go vs.loop()
	return vs
}

// newRTCAPI builds a pion API: Opus audio and VP8 video, default interceptors (NACK,
// RTCP reports), host ICE candidates only. cfg.PublicIP, if set, is
// advertised as the host candidate address (NAT 1:1) for internet-facing
// servers.
func (s *Server) newRTCAPI() *webrtc.API {
	me := &webrtc.MediaEngine{}
	if !s.cfg.NoVP8 && !s.cfg.LegacyHost {
		_ = me.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType:     webrtc.MimeTypeVP8,
				ClockRate:    90000,
				RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}},
			},
			PayloadType: 96,
		}, webrtc.RTPCodecTypeVideo)
	}
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
	if s.mux != nil {
		se.SetICEUDPMux(s.mux)
	} else if s.cfg.UDPPortMin != 0 && s.cfg.UDPPortMax != 0 {
		_ = se.SetEphemeralUDPPortRange(s.cfg.UDPPortMin, s.cfg.UDPPortMax)
	}
	if s.cfg.AllowLoopbackICE {
		loopbackOnly(&se)
	}
	if s.cfg.PublicIP != "" {
		se.SetNAT1To1IPs([]string{s.cfg.PublicIP}, webrtc.ICECandidateTypeHost)
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
}

// loopbackIP is the ICE address filter for loopback mode.
func loopbackIP(ip net.IP) bool { return ip.IsLoopback() }

// loopbackOnly confines ICE to loopback and turns off mDNS, so test runs
// never bind a LAN interface or multicast (the Windows Firewall prompts for
// both). This breaks remote peers, so it is for tests only. Duplicated from
// client: server must not import client.
func loopbackOnly(se *webrtc.SettingEngine) {
	se.SetIncludeLoopbackCandidate(true)
	se.SetIPFilter(loopbackIP)
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
}

// post queues fn for the actor. Never blocks; dropped after stop().
func (vs *voiceSession) post(fn func()) {
	vs.qmu.Lock()
	if vs.closed {
		vs.qmu.Unlock()
		return
	}
	vs.queue = append(vs.queue, fn)
	vs.qmu.Unlock()
	vs.signal()
}

func (vs *voiceSession) signal() {
	select {
	case vs.wake <- struct{}{}:
	default:
	}
}

// stop queues teardown as the final closure and refuses further posts.
// Non-blocking; the actor does the closing and joining.
func (vs *voiceSession) stop() {
	vs.qmu.Lock()
	if vs.closed {
		vs.qmu.Unlock()
		return
	}
	vs.queue = append(vs.queue, vs.teardown)
	vs.closed = true
	vs.qmu.Unlock()
	vs.signal()
}

func (vs *voiceSession) pop() func() {
	vs.qmu.Lock()
	defer vs.qmu.Unlock()
	if len(vs.queue) == 0 {
		return nil
	}
	fn := vs.queue[0]
	vs.queue[0] = nil
	vs.queue = vs.queue[1:]
	return fn
}

func (vs *voiceSession) loop() {
	defer vs.srv.wg.Done()
	defer close(vs.done)
	tick := time.NewTicker(statsInterval)
	defer tick.Stop()
	for {
		select {
		case <-vs.wake:
			for fn := vs.pop(); fn != nil; fn = vs.pop() {
				fn()
				if vs.finished {
					return
				}
			}
		case <-tick.C:
			vs.logStats()
		}
	}
}

func (vs *voiceSession) logStats() {
	now := time.Now()
	total, video := vs.relayedBytes.Load(), vs.videoBytes.Load()
	secs := now.Sub(vs.statsAt).Seconds()
	var kbps, vkbps float64
	if secs > 0 {
		kbps = float64(total-vs.statsBytes) * 8 / 1000 / secs
		vkbps = float64(video-vs.statsVideo) * 8 / 1000 / secs
	}
	vs.statsAt, vs.statsBytes, vs.statsVideo = now, total, video
	vs.srv.logf("voice %q: %d participants, ~%.0f kbps relayed, ~%.0f kbps video, %d write errors", vs.channel, len(vs.peers), kbps, vkbps, vs.writeErrors.Load())
}

// teardown (actor): close every PeerConnection, then join every goroutine
// this session started.
func (vs *voiceSession) teardown() {
	for c, p := range vs.peers {
		for _, f := range p.fwds {
			f.kill()
		}
		vs.stopOfferTimer(p)
		pc := p.pc
		p.pc = nil
		if pc != nil {
			_ = pc.Close()
		}
		delete(vs.peers, c)
	}
	vs.wg.Wait()
	vs.finished = true
}

// ---- membership hooks (called under chMu; non-blocking) ----

func (vs *voiceSession) addPeer(c *conn) {
	vs.post(func() {
		if _, ok := vs.peers[c]; !ok {
			vs.peers[c] = &peer{c: c, senders: make(map[*forwarder]*webrtc.RTPSender), watchers: make(map[*peer]bool)}
		}
	})
}

func (vs *voiceSession) removePeer(c *conn) {
	vs.post(func() {
		p, ok := vs.peers[c]
		if !ok {
			return
		}
		vs.clearWatchers(p)
		if p.watching != nil {
			delete(p.watching.watchers, p)
			p.watching = nil
		}
		delete(vs.peers, c)
		vs.releaseMedia(p)
	})
}

// shareStarted (called under chMu; non-blocking) marks c as sharing; only a
// sharing peer's video is relayed.
func (vs *voiceSession) shareStarted(c *conn) {
	vs.post(func() {
		if p, ok := vs.peers[c]; ok {
			p.sharing.Store(true)
		}
	})
}

// shareStopped (called under chMu; non-blocking) removes the sharer's video
// from every watcher. The conn-side watch state is already cleared.
func (vs *voiceSession) shareStopped(c *conn) {
	vs.post(func() {
		if p, ok := vs.peers[c]; ok {
			p.sharing.Store(false)
			vs.clearWatchers(p)
		}
	})
}

// setWatch (called under chMu; non-blocking) points viewer c at the sharer
// target (nil = stop watching).
func (vs *voiceSession) setWatch(c, target *conn) {
	vs.post(func() {
		v, ok := vs.peers[c]
		if !ok {
			return
		}
		if v.watching != nil {
			delete(v.watching.watchers, v)
			v.watching = nil
		}
		v.kfWanted = false
		if target != nil {
			if t, ok := vs.peers[target]; ok && t.sharing.Load() {
				v.watching = t
				t.watchers[v] = true
				vs.requestKeyframe(t, true)
			}
		}
		vs.syncTracks(v)
	})
}

// clearWatchers (actor) detaches every viewer of sharer p and drops their
// video track.
func (vs *voiceSession) clearWatchers(p *peer) {
	viewers := p.watchers
	p.watchers = make(map[*peer]bool)
	for v := range viewers {
		v.watching = nil
		v.kfWanted = false
		vs.syncTracks(v)
	}
}

// PLI spacing per sharer. Viewer-relayed requests are limited to one per
// second; the server's own requests (a viewer started watching) to one per
// 500 ms, so toggling watch cannot make the sharer emit keyframes at message
// rate. Both share one timestamp.
const (
	relayPLIInterval  = time.Second
	forcedPLIInterval = 500 * time.Millisecond
)

// requestKeyframe (actor) writes a PLI to sharer t's PC, subject to the
// per-sharer spacing above.
func (vs *voiceSession) requestKeyframe(t *peer, force bool) {
	if t.pc == nil {
		return
	}
	now := time.Now()
	min := relayPLIInterval
	if force {
		min = forcedPLIInterval
	}
	if now.Sub(t.lastPLI) < min {
		return
	}
	for _, f := range t.fwds {
		if f.video && !f.isDead() {
			t.lastPLI = now
			_ = t.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(f.remote.SSRC())}})
			return
		}
	}
}

// handleRTC is called from a conn's read loop after it verified membership.
func (vs *voiceSession) handleRTC(c *conn, env proto.Envelope) {
	vs.post(func() {
		p, ok := vs.peers[c]
		if !ok {
			return // left in the meantime
		}
		switch env.Type {
		case proto.TypeRTCOffer:
			var m proto.RTCOffer
			if json.Unmarshal(env.Data, &m) != nil {
				c.sendError("bad_request", "malformed rtc_offer")
				return
			}
			vs.handleOffer(p, m.SDP)
		case proto.TypeRTCAnswer:
			var m proto.RTCAnswer
			if json.Unmarshal(env.Data, &m) != nil {
				c.sendError("bad_request", "malformed rtc_answer")
				return
			}
			vs.handleAnswer(p, m.SDP)
		case proto.TypeRTCCandidate:
			var m proto.RTCCandidate
			var ci webrtc.ICECandidateInit
			if json.Unmarshal(env.Data, &m) != nil || json.Unmarshal([]byte(m.Candidate), &ci) != nil {
				c.sendError("bad_request", "malformed rtc_candidate")
				return
			}
			vs.handleCandidate(p, ci)
		}
	})
}

// ---- negotiation (actor) ----

func (vs *voiceSession) reply(p *peer, msgType string, payload any) {
	if !p.c.sendMsg(msgType, payload) {
		dropSlow([]*conn{p.c})
	}
}

func (vs *voiceSession) newPC(p *peer) (*webrtc.PeerConnection, error) {
	pc, err := vs.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	// pion goroutines: only post, never touch session state directly.
	pc.OnICECandidate(func(ci *webrtc.ICECandidate) {
		if ci == nil {
			return
		}
		raw, err := json.Marshal(ci.ToJSON())
		if err != nil {
			return
		}
		// Through the actor so a candidate can never overtake the SDP that
		// was queued by the closure that triggered gathering.
		vs.post(func() {
			if p.pc == pc {
				vs.reply(p, proto.TypeRTCCandidate, proto.RTCCandidate{Candidate: string(raw)})
			}
		})
	})
	pc.OnTrack(func(remote *webrtc.TrackRemote, recv *webrtc.RTPReceiver) {
		vs.post(func() {
			if p.pc != pc {
				return
			}
			vs.wg.Add(1)
			go vs.drainReceiverRTCP(recv)
			isVideo := remote.Kind() == webrtc.RTPCodecTypeVideo
			for _, g := range p.fwds {
				if g.video == isVideo && !g.isDead() {
					// One audio and one screen-share track per peer; discard extras.
					vs.srv.logf("voice %q: %s sent a second %s track; ignoring it", vs.channel, p.c.fingerprint, remote.Kind())
					vs.wg.Add(1)
					go vs.discardTrack(remote)
					return
				}
			}
			f := &forwarder{pub: p, remote: remote, video: isVideo, sinks: make(map[*peer]sink)}
			p.fwds = append(p.fwds, f)
			vs.wg.Add(1)
			go vs.runForwarder(f)
			for _, q := range vs.peers {
				if q != p {
					vs.syncTracks(q)
				}
			}
		})
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st != webrtc.PeerConnectionStateFailed {
			return
		}
		vs.post(func() {
			if p.pc == pc {
				vs.releaseMedia(p) // member stays in the call; may re-offer
			}
		})
	})
	return pc, nil
}

func (vs *voiceSession) handleOffer(p *peer, sdp string) {
	if p.negotiating {
		p.c.sendError("bad_request", errOfferPending)
		return
	}
	fresh := false
	if p.pc == nil {
		pc, err := vs.newPC(p)
		if err != nil {
			p.c.sendError("bad_request", "cannot create peer connection")
			return
		}
		p.pc, fresh = pc, true
	} else if p.pc.SignalingState() != webrtc.SignalingStateStable {
		p.c.sendError("bad_request", "rtc_offer in unexpected signaling state")
		return
	}
	fail := func(msg string) {
		p.c.sendError("bad_request", msg)
		if fresh {
			pc := p.pc
			p.pc = nil
			_ = pc.Close()
		} else {
			// pion may be stuck mid-negotiation; start over.
			vs.releaseMedia(p)
		}
	}
	pc := p.pc
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
		fail("invalid rtc_offer")
		return
	}
	vs.flushPending(p)
	answer, err := pc.CreateAnswer(nil)
	if err == nil {
		err = pc.SetLocalDescription(answer)
	}
	if err != nil {
		fail("cannot answer rtc_offer")
		return
	}
	if videoRefused(sdp, pc.LocalDescription().SDP) {
		// The peer sends video this host has no codec for. Keeping the PC would
		// leave a dead m-line that pion can never renegotiate around; reset so
		// the peer's next (audio-only) offer starts clean.
		fail(msgNoVideoCodec)
		return
	}
	vs.reply(p, proto.TypeRTCAnswer, proto.RTCAnswer{SDP: pc.LocalDescription().SDP})
	vs.syncTracks(p) // attach everyone else's audio; renegotiates if any
}

// msgNoVideoCodec contains the fixed reset hint (docs/protocol.md 4.13), so
// clients recover by sending a new rtc_offer.
const msgNoVideoCodec = "no common video codec; media reset, send a new rtc_offer"

// videoRefused reports whether offer has a video section that answer rejects.
func videoRefused(offer, answer string) bool {
	if !strings.Contains(offer, "m=video") {
		return false
	}
	desc := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}
	parsed, err := desc.Unmarshal()
	if err != nil {
		return false
	}
	for _, m := range parsed.MediaDescriptions {
		if m.MediaName.Media == "video" && m.MediaName.Port.Value == 0 {
			return true
		}
	}
	return false
}

func (vs *voiceSession) handleAnswer(p *peer, sdp string) {
	if p.pc == nil || !p.negotiating {
		p.c.sendError("bad_request", "unexpected rtc_answer")
		return
	}
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		// pion stays in have-local-offer; start over so the peer can recover
		// by sending a fresh rtc_offer.
		p.c.sendError("bad_request", "invalid rtc_answer; media reset, send a new rtc_offer")
		vs.releaseMedia(p)
		return
	}
	p.negotiating = false
	vs.stopOfferTimer(p)
	vs.flushPending(p)
	if p.kfInOffer {
		// The answered offer carried this viewer's video, so the track is
		// bound now; a keyframe sent earlier may have been dropped.
		p.kfInOffer = false
		if p.kfWanted && p.watching != nil {
			p.kfWanted = false
			vs.requestKeyframe(p.watching, true)
		}
	}
	if p.dirty {
		p.dirty = false
		vs.renegotiate(p)
	}
}

func (vs *voiceSession) handleCandidate(p *peer, ci webrtc.ICECandidateInit) {
	if p.pc == nil || p.pc.RemoteDescription() == nil {
		p.pending = append(p.pending, ci)
		if len(p.pending) > 64 {
			p.pending = p.pending[1:]
		}
		return
	}
	_ = p.pc.AddICECandidate(ci) // a bad candidate is not fatal to the call
}

func (vs *voiceSession) flushPending(p *peer) {
	for _, ci := range p.pending {
		_ = p.pc.AddICECandidate(ci)
	}
	p.pending = nil
}

// renegotiate sends a server offer to p, or marks it dirty if p is mid
// negotiation.
func (vs *voiceSession) renegotiate(p *peer) {
	if p.pc == nil {
		return // no initial offer yet; syncTracks runs after it
	}
	if p.negotiating || p.pc.SignalingState() != webrtc.SignalingStateStable {
		p.dirty = true
		return
	}
	offer, err := p.pc.CreateOffer(nil)
	if err == nil {
		err = p.pc.SetLocalDescription(offer)
	}
	if err != nil {
		vs.srv.logf("voice %q: renegotiation offer failed: %v", vs.channel, err)
		p.c.sendError("bad_request", "renegotiation failed; media reset, send a new rtc_offer")
		vs.releaseMedia(p)
		return
	}
	raw, err := proto.Encode(proto.TypeRTCOffer, proto.RTCOffer{SDP: p.pc.LocalDescription().SDP})
	if err != nil || len(raw) > vs.srv.offerLimit() {
		// A dropped offer would wedge negotiation, so reset instead.
		if err != nil {
			vs.srv.logf("voice %q: cannot encode rtc_offer for %s: %v; resetting its media", vs.channel, p.c.fingerprint, err)
		} else {
			vs.srv.logf("voice %q: rtc_offer for %s is %d bytes (max %d); resetting its media", vs.channel, p.c.fingerprint, len(raw), vs.srv.offerLimit())
		}
		p.c.sendError("bad_request", "rtc_offer too large; media reset, send a new rtc_offer")
		vs.releaseMedia(p)
		return
	}
	p.negotiating = true
	p.kfInOffer = p.kfWanted && p.vfwd != nil
	p.offerGen++
	gen, pc := p.offerGen, p.pc
	vs.stopOfferTimer(p)
	p.offerTimer = time.AfterFunc(answerTimeout, func() {
		vs.post(func() {
			if p.negotiating && p.offerGen == gen && p.pc == pc {
				vs.srv.logf("voice %q: no rtc_answer from %s; resetting its media", vs.channel, p.c.fingerprint)
				p.c.sendError("bad_request", "rtc_answer timeout; media reset, send a new rtc_offer")
				vs.releaseMedia(p)
			}
		})
	})
	if !p.c.enqueue(raw) {
		dropSlow([]*conn{p.c})
	}
}

// offerLimit is the largest server rtc_offer (encoded frame) the SFU sends.
func (s *Server) offerLimit() int {
	if s.cfg.maxOfferBytes > 0 {
		return s.cfg.maxOfferBytes
	}
	return proto.MaxMessageSize
}

func (vs *voiceSession) stopOfferTimer(p *peer) {
	if p.offerTimer != nil {
		p.offerTimer.Stop()
		p.offerTimer = nil
	}
}

// syncTracks makes q's outgoing tracks match the live publishers of every
// other peer, renegotiating if anything changed.
func (vs *voiceSession) syncTracks(q *peer) {
	if q.pc == nil {
		return
	}
	changed := false
	for f, snd := range q.senders {
		if f.isDead() {
			_ = q.pc.RemoveTrack(snd)
			delete(q.senders, f)
			q.retired++
			changed = true
		}
	}
	for _, r := range vs.peers {
		if r == q {
			continue
		}
		for _, f := range r.fwds {
			if f.video { // video goes through syncVideo
				continue
			}
			if _, ok := q.senders[f]; ok || f.isDead() {
				continue
			}
			local, err := webrtc.NewTrackLocalStaticRTP(f.remote.Codec().RTPCodecCapability,
				"audio-"+r.c.fingerprint, r.c.fingerprint)
			if err != nil {
				continue
			}
			tr, err := q.pc.AddTransceiverFromTrack(local, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
			if err != nil {
				continue
			}
			snd := tr.Sender()
			q.senders[f] = snd
			f.addSink(q, local, q.pc, nil)
			vs.wg.Add(1)
			go vs.drainRTCP(snd)
			changed = true
		}
	}
	// Pion never frees a transceiver, and reusing an audio line would keep the
	// previous speaker's stream id (clients attribute audio by it), so bound
	// the growth by rebuilding this peer's PC once enough lines are retired.
	// The rebuild also restarts the member's own published audio and any
	// screen share they send (viewers see a brief freeze), and because
	// releaseMedia re-syncs everyone else, the room tends to reset together.
	if q.retired > maxInactiveLines {
		vs.srv.logf("voice %q: %s has %d inactive audio lines; resetting its media", vs.channel, q.c.fingerprint, q.retired)
		q.c.sendError("bad_request", "too many retired audio lines; media reset, send a new rtc_offer")
		vs.releaseMedia(q)
		return
	}
	if vs.syncVideo(q) {
		changed = true
	}
	if changed {
		vs.renegotiate(q)
	}
}

// syncVideo (actor) points q's single video sender at the forwarder of the
// sharer q watches (or at nothing). It reports whether a renegotiation is
// needed, which is only the case the first time q gets a video transceiver.
func (vs *voiceSession) syncVideo(q *peer) bool {
	var want *forwarder
	if t := q.watching; t != nil && t.sharing.Load() {
		for _, f := range t.fwds {
			if f.video && !f.isDead() {
				want = f
				break
			}
		}
	}
	if q.vfwd == want {
		return false
	}
	if q.vfwd != nil {
		// The sender keeps its stale local track but nothing writes to it.
		q.vfwd.removeSink(q)
		q.vfwd = nil
		if q.vrw != nil {
			q.vrw.retire()
			q.vrw = nil
		}
		if want == nil {
			vs.sendVideoSource(q, "", 0)
		}
	}
	if want == nil {
		return false
	}
	fp := want.pub.c.fingerprint
	local, err := webrtc.NewTrackLocalStaticRTP(want.remote.Codec().RTPCodecCapability, "video-"+fp, fp)
	if err != nil {
		return false
	}
	if q.vout == nil {
		q.vout = &outStream{}
	}
	changed := false
	if q.vsender == nil {
		tr, err := q.pc.AddTransceiverFromTrack(local, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
		if err != nil {
			return false
		}
		q.vsender = tr.Sender()
		vs.wg.Add(1)
		go vs.readViewerRTCP(q.vsender, q)
		q.kfWanted = true // sent once an answer covering this transceiver lands
		changed = true
	} else {
		if err := q.vsender.ReplaceTrack(local); err != nil {
			return false
		}
		q.kfWanted = false
		vs.requestKeyframe(want.pub, true) // already negotiated: bound now
	}
	q.vfwd = want
	// Fix the boundary, tell the viewer, and only then let the new source's
	// packets reach the sender: the forwarder writes to q only through its
	// sink, which is added last. attach also retires the previous source's
	// rewrite, so none of its in-flight packets can be numbered at or after
	// the boundary. The WebSocket and RTP paths are independent, so the
	// message can still arrive after the first packets; the client copes.
	q.vrw = q.vout.attach(q.vrw)
	vs.sendVideoSource(q, fp, q.vrw.bseq)
	want.addSink(q, local, q.pc, q.vrw)
	return changed
}

// sendVideoSource (actor) tells q which source its video track carries from
// seq on ("" = detached).
func (vs *voiceSession) sendVideoSource(q *peer, fp string, seq uint16) {
	raw, err := proto.Encode(proto.TypeVideoSource, proto.VideoSource{Fingerprint: fp, Seq: seq})
	if err != nil {
		return
	}
	if !q.c.enqueue(raw) {
		dropSlow([]*conn{q.c})
	}
}

// releaseMedia (actor) tears down p's PeerConnection and everything attached
// to it, then re-syncs the other peers (their copies of p's tracks go away).
func (vs *voiceSession) releaseMedia(p *peer) {
	pc := p.pc
	p.pc = nil
	p.negotiating, p.dirty, p.pending = false, false, nil
	p.retired = 0
	vs.stopOfferTimer(p)
	for _, f := range p.fwds {
		f.kill()
	}
	p.fwds = nil
	for f := range p.senders {
		f.removeSink(p)
	}
	p.senders = make(map[*forwarder]*webrtc.RTPSender)
	if p.vfwd != nil {
		p.vfwd.removeSink(p)
	}
	// A fresh PC gets a fresh video transceiver; syncVideo re-arms kfWanted
	// when it re-attaches a watched stream.
	p.vsender, p.vfwd, p.vout, p.vrw, p.kfInOffer = nil, nil, nil, nil, false
	if pc != nil {
		_ = pc.Close()
	}
	for _, q := range vs.peers {
		if q != p {
			vs.syncTracks(q)
		}
	}
}

// ---- media (forwarder goroutines) ----

// runForwarder copies one publisher track to its subscribers. A failing
// subscriber is pruned; only the publisher's own death ends the loop.
func (vs *voiceSession) runForwarder(f *forwarder) {
	defer vs.wg.Done()
	defer vs.post(func() { vs.forwarderEnded(f) })
	var snap []sinkRef
	for {
		pkt, _, err := f.remote.ReadRTP()
		if err != nil {
			return
		}
		if f.video && !f.pub.sharing.Load() {
			continue // only a sharing peer's video is relayed
		}
		snap = snap[:0]
		f.mu.Lock()
		for p, sk := range f.sinks {
			snap = append(snap, sinkRef{p, sk})
		}
		f.mu.Unlock()
		for _, s := range snap {
			out := pkt
			if s.rw != nil {
				if out = s.rw.apply(pkt); out == nil {
					continue // retired: the viewer moved to another source
				}
			}
			if err := s.t.WriteRTP(out); err != nil {
				// Only a closed/failed subscriber is pruned; other write
				// errors are transient and counted.
				st := s.pc.ConnectionState()
				if errors.Is(err, io.ErrClosedPipe) || st == webrtc.PeerConnectionStateClosed || st == webrtc.PeerConnectionStateFailed {
					f.removeSink(s.p)
					p := s.p
					vs.post(func() { vs.pruneSender(p, f) })
				} else {
					vs.writeErrors.Add(1)
				}
				continue
			}
			if f.video {
				vs.videoBytes.Add(uint64(len(pkt.Payload)))
			} else {
				vs.relayedBytes.Add(uint64(len(pkt.Payload)))
			}
		}
	}
}

func (vs *voiceSession) pruneSender(sub *peer, f *forwarder) {
	if f.video {
		// Single reusable video sender: just detach and re-attach.
		if sub.vfwd == f {
			sub.vfwd = nil
			if sub.pc != nil {
				if st := sub.pc.ConnectionState(); st != webrtc.PeerConnectionStateClosed && st != webrtc.PeerConnectionStateFailed {
					vs.syncTracks(sub)
				}
			}
		}
		return
	}
	snd, ok := sub.senders[f]
	if !ok {
		return
	}
	delete(sub.senders, f)
	if sub.pc != nil {
		_ = sub.pc.RemoveTrack(snd)
		sub.retired++
		// Re-sync: if the publisher is still alive and the subscriber's PC
		// is usable, the track comes back (fresh local track) in the same
		// renegotiation instead of waiting for an unrelated topology change.
		if st := sub.pc.ConnectionState(); st != webrtc.PeerConnectionStateClosed && st != webrtc.PeerConnectionStateFailed {
			vs.syncTracks(sub)
		}
		vs.renegotiate(sub)
	}
}

// forwarderEnded (actor): the publisher's track stopped on its own.
func (vs *voiceSession) forwarderEnded(f *forwarder) {
	f.kill()
	for i, g := range f.pub.fwds {
		if g == f {
			f.pub.fwds = append(f.pub.fwds[:i], f.pub.fwds[i+1:]...)
			break
		}
	}
	for _, q := range vs.peers {
		if q != f.pub {
			vs.syncTracks(q)
		}
	}
}

// discardTrack reads and drops a publisher track the SFU does not relay.
func (vs *voiceSession) discardTrack(t *webrtc.TrackRemote) {
	defer vs.wg.Done()
	for {
		if _, _, err := t.ReadRTP(); err != nil {
			return
		}
	}
}

// drainReceiverRTCP reads (and discards) RTCP on a publisher's receiver so
// receiver-side interceptors run. Ends when the receiver is stopped.
func (vs *voiceSession) drainReceiverRTCP(r *webrtc.RTPReceiver) {
	defer vs.wg.Done()
	buf := make([]byte, 1500)
	for {
		if _, _, err := r.Read(buf); err != nil {
			return
		}
	}
}

// drainRTCP reads (and discards) RTCP for a sender; required for pion's
// interceptors (NACK etc.) to run. Ends when the sender is stopped.
func (vs *voiceSession) drainRTCP(snd *webrtc.RTPSender) {
	defer vs.wg.Done()
	buf := make([]byte, 1500)
	for {
		if _, _, err := snd.Read(buf); err != nil {
			return
		}
	}
}

// readViewerRTCP reads a viewer's RTCP for a video sender (which also keeps
// pion's interceptors running) and relays keyframe requests (PLI/FIR) to the
// sharer through the actor, which rate-limits them to one per second.
func (vs *voiceSession) readViewerRTCP(snd *webrtc.RTPSender, v *peer) {
	defer vs.wg.Done()
	for {
		pkts, _, err := snd.ReadRTCP()
		if err != nil {
			return
		}
		for _, pk := range pkts {
			switch pk.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				vs.post(func() {
					if v.watching != nil && v.vfwd != nil {
						vs.requestKeyframe(v.watching, false)
					}
				})
			}
		}
	}
}

// ---- hooks on Server (called under chMu) ----

// sessionJoinLocked registers c with ch's session, creating it if c is the
// first occupant. Caller holds chMu.
func (s *Server) sessionJoinLocked(ch string, c *conn) {
	vs := s.sessions[ch]
	if vs == nil {
		vs = s.newVoiceSession(ch)
		s.sessions[ch] = vs
	}
	vs.addPeer(c)
}

// sessionLeaveLocked removes c from ch's session and destroys the session if
// that emptied the channel. Caller holds chMu (after updating s.voice).
func (s *Server) sessionLeaveLocked(ch string, c *conn) {
	vs := s.sessions[ch]
	if vs == nil {
		return
	}
	vs.removePeer(c)
	if len(s.voice[ch]) == 0 {
		vs.stop()
		delete(s.sessions, ch)
	}
}
