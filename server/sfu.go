package server

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
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

	relayedBytes atomic.Uint64 // payload bytes written to subscribers
	writeErrors  atomic.Uint64 // transient subscriber write errors (not pruned)
	statsAt      time.Time
	statsBytes   uint64
}

// answerTimeout bounds how long a server-initiated offer may go unanswered
// before the peer's media state is reset. A variable so tests can shorten it.
var answerTimeout = 15 * time.Second

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
}

// forwarder copies one publisher track to every subscriber's local track.
type forwarder struct {
	pub    *peer
	remote *webrtc.TrackRemote

	mu    sync.Mutex
	sinks map[*peer]sink
	dead  bool
}

// sink is one subscriber's outgoing copy of a track. pc is immutable and
// safe to query from the forwarder goroutine (ConnectionState is thread-safe).
type sink struct {
	t  *webrtc.TrackLocalStaticRTP
	pc *webrtc.PeerConnection
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

func (f *forwarder) addSink(p *peer, t *webrtc.TrackLocalStaticRTP, pc *webrtc.PeerConnection) {
	f.mu.Lock()
	if !f.dead {
		f.sinks[p] = sink{t, pc}
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

// newRTCAPI builds a pion API: Opus audio only, default interceptors (NACK,
// RTCP reports), host ICE candidates only. cfg.PublicIP, if set, is
// advertised as the host candidate address (NAT 1:1) for internet-facing
// servers.
func (s *Server) newRTCAPI() *webrtc.API {
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
	if s.cfg.UDPPortMin != 0 && s.cfg.UDPPortMax != 0 {
		_ = se.SetEphemeralUDPPortRange(s.cfg.UDPPortMin, s.cfg.UDPPortMax)
	}
	if s.cfg.AllowLoopbackICE {
		se.SetIncludeLoopbackCandidate(true)
	}
	if s.cfg.PublicIP != "" {
		se.SetNAT1To1IPs([]string{s.cfg.PublicIP}, webrtc.ICECandidateTypeHost)
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
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
	total := vs.relayedBytes.Load()
	secs := now.Sub(vs.statsAt).Seconds()
	var kbps float64
	if secs > 0 {
		kbps = float64(total-vs.statsBytes) * 8 / 1000 / secs
	}
	vs.statsAt, vs.statsBytes = now, total
	log.Printf("voice %q: %d participants, ~%.0f kbps relayed, %d write errors", vs.channel, len(vs.peers), kbps, vs.writeErrors.Load())
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
			vs.peers[c] = &peer{c: c, senders: make(map[*forwarder]*webrtc.RTPSender)}
		}
	})
}

func (vs *voiceSession) removePeer(c *conn) {
	vs.post(func() {
		p, ok := vs.peers[c]
		if !ok {
			return
		}
		delete(vs.peers, c)
		vs.releaseMedia(p)
	})
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
			f := &forwarder{pub: p, remote: remote, sinks: make(map[*peer]sink)}
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
	vs.reply(p, proto.TypeRTCAnswer, proto.RTCAnswer{SDP: pc.LocalDescription().SDP})
	vs.syncTracks(p) // attach everyone else's audio; renegotiates if any
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
		log.Printf("voice %q: renegotiation offer failed: %v", vs.channel, err)
		p.c.sendError("bad_request", "renegotiation failed; media reset, send a new rtc_offer")
		vs.releaseMedia(p)
		return
	}
	p.negotiating = true
	p.offerGen++
	gen, pc := p.offerGen, p.pc
	vs.stopOfferTimer(p)
	p.offerTimer = time.AfterFunc(answerTimeout, func() {
		vs.post(func() {
			if p.negotiating && p.offerGen == gen && p.pc == pc {
				log.Printf("voice %q: no rtc_answer from %s; resetting its media", vs.channel, p.c.fingerprint)
				p.c.sendError("bad_request", "rtc_answer timeout; media reset, send a new rtc_offer")
				vs.releaseMedia(p)
			}
		})
	})
	vs.reply(p, proto.TypeRTCOffer, proto.RTCOffer{SDP: p.pc.LocalDescription().SDP})
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
			changed = true
		}
	}
	for _, r := range vs.peers {
		if r == q {
			continue
		}
		for _, f := range r.fwds {
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
			f.addSink(q, local, q.pc)
			vs.wg.Add(1)
			go vs.drainRTCP(snd)
			changed = true
		}
	}
	if changed {
		vs.renegotiate(q)
	}
}

// releaseMedia (actor) tears down p's PeerConnection and everything attached
// to it, then re-syncs the other peers (their copies of p's tracks go away).
func (vs *voiceSession) releaseMedia(p *peer) {
	pc := p.pc
	p.pc = nil
	p.negotiating, p.dirty, p.pending = false, false, nil
	vs.stopOfferTimer(p)
	for _, f := range p.fwds {
		f.kill()
	}
	p.fwds = nil
	for f := range p.senders {
		f.removeSink(p)
	}
	p.senders = make(map[*forwarder]*webrtc.RTPSender)
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
		snap = snap[:0]
		f.mu.Lock()
		for p, sk := range f.sinks {
			snap = append(snap, sinkRef{p, sk})
		}
		f.mu.Unlock()
		for _, s := range snap {
			if err := s.t.WriteRTP(pkt); err != nil {
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
			vs.relayedBytes.Add(uint64(len(pkt.Payload)))
		}
	}
}

func (vs *voiceSession) pruneSender(sub *peer, f *forwarder) {
	snd, ok := sub.senders[f]
	if !ok {
		return
	}
	delete(sub.senders, f)
	if sub.pc != nil {
		_ = sub.pc.RemoveTrack(snd)
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
