package server

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

func shareCfg() Config {
	c := voiceCfg()
	c.AllowLoopbackICE = true
	return c
}

func fpOf(id *identity.Identity) string { return identity.Fingerprint(id.PublicKey()) }

func joinLounge(t *testing.T, c *websocket.Conn) {
	t.Helper()
	sendEnv(t, c, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
}

// readVoiceUntil reads voice_states until pred matches; 5 s deadline.
func readVoiceUntil(t *testing.T, c *websocket.Conn, pred func(proto.VoiceState) bool) proto.VoiceState {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	for {
		v := readVoiceState(t, c)
		if pred(v) {
			return v
		}
	}
}

func sharingOf(v proto.VoiceState, fp string) bool {
	for _, m := range v.Members {
		if m.Fingerprint == fp {
			return m.Sharing
		}
	}
	return false
}

func TestShareStartSetsSharing(t *testing.T) {
	url := startServer(t, shareCfg())
	a, aid := authed(t, url)
	b, _ := authed(t, url)
	joinLounge(t, a)
	readVoiceState(t, a)
	sendEnv(t, a, proto.TypeShareStart, proto.ShareStart{})
	v := readVoiceUntil(t, b, func(v proto.VoiceState) bool { return sharingOf(v, fpOf(aid)) })
	if v.Channel != "lounge" {
		t.Fatalf("state = %+v", v)
	}
	sendEnv(t, a, proto.TypeShareStop, proto.ShareStop{})
	readVoiceUntil(t, b, func(v proto.VoiceState) bool { return !sharingOf(v, fpOf(aid)) && len(v.Members) == 1 })
}

func TestShareStartOutsideVoiceIgnored(t *testing.T) {
	url := startServer(t, shareCfg())
	a, _ := authed(t, url)
	b, _ := authed(t, url)
	sendEnv(t, a, proto.TypeShareStart, proto.ShareStart{})
	joinLounge(t, b)
	v := readVoiceState(t, b) // first state is b's join, not a broadcast from the ignored share_start
	if len(v.Members) != 1 || v.Members[0].Sharing {
		t.Fatalf("state = %+v", v)
	}
	joinLounge(t, a)
	v = readVoiceUntil(t, b, func(v proto.VoiceState) bool { return len(v.Members) == 2 })
	for _, m := range v.Members {
		if m.Sharing {
			t.Fatalf("share_start outside voice leaked: %+v", v)
		}
	}
}

// readErrorCode skips roster and voice_state frames and returns the next
// error's code.
func readErrorCode(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	for {
		env := readEnvSkipRoster(t, c)
		if env.Type != proto.TypeError {
			continue
		}
		var e proto.ErrorMsg
		_ = json.Unmarshal(env.Data, &e)
		return e.Code
	}
}

func TestWatchNonSharerErrors(t *testing.T) {
	url := startServer(t, shareCfg())
	a, aid := authed(t, url)
	b, _ := authed(t, url)
	joinLounge(t, a)
	joinLounge(t, b)
	sendEnv(t, b, proto.TypeWatch, proto.Watch{Fingerprint: fpOf(aid)})
	if code := readErrorCode(t, b); code != proto.CodeNotSharing {
		t.Fatalf("code = %q, want not_sharing", code)
	}
	sendEnv(t, b, proto.TypeWatch, proto.Watch{Fingerprint: "nobody"})
	if code := readErrorCode(t, b); code != proto.CodeNotSharing {
		t.Fatalf("code = %q, want not_sharing", code)
	}
}

func TestWatchSharerInOtherChannelErrors(t *testing.T) {
	url := startServer(t, shareCfg())
	a, aid := authed(t, url)
	b, _ := authed(t, url)
	joinLounge(t, a)
	sendEnv(t, a, proto.TypeShareStart, proto.ShareStart{})
	sendEnv(t, b, proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "war-room"})
	readVoiceUntil(t, b, func(v proto.VoiceState) bool { return sharingOf(v, fpOf(aid)) })
	sendEnv(t, b, proto.TypeWatch, proto.Watch{Fingerprint: fpOf(aid)})
	if code := readErrorCode(t, b); code != proto.CodeNotSharing {
		t.Fatalf("code = %q, want not_sharing", code)
	}
}

// ---- pion test peer ----

type testPeer struct {
	t   *testing.T
	ws  *websocket.Conn
	fp  string
	pc  *webrtc.PeerConnection
	wmu sync.Mutex

	video   *webrtc.TrackLocalStaticRTP // nil for a pure viewer
	camera  *webrtc.TrackLocalStaticRTP
	screen  *webrtc.TrackLocalStaticRTP
	vtracks chan *webrtc.TrackRemote
	vpkts   atomic.Int64
	apkts   atomic.Int64
	vtag    atomic.Int32 // payload tag of the last video packet received
	tag     byte         // payload tag this peer stamps on the video it sends
	plis    atomic.Int64

	states chan proto.VoiceState
	audio  *webrtc.TrackLocalStaticRTP // the audio track this peer publishes
	errs   chan string                 // messages of server error frames
	vsrc   chan proto.VideoSource      // video_source messages received
	msrc   chan proto.MediaSource      // media_source messages received
	mpkts  chan mediaPacket            // received modern slot packets
}

type mediaPacket struct {
	trackID string
	seq     uint16
	tag     byte
}

func newTestPeer(t *testing.T, url string, publish bool) *testPeer {
	return newTestPeerMode(t, url, publish, false)
}

func newModernTestPeer(t *testing.T, url string, publish bool) *testPeer {
	return newTestPeerMode(t, url, publish, true)
}

func newTestPeerMode(t *testing.T, url string, publish, modern bool) *testPeer {
	t.Helper()
	var ws *websocket.Conn
	var id *identity.Identity
	if modern {
		ws, id = authedMedia(t, url)
	} else {
		ws, id = authed(t, url)
	}
	p := &testPeer{t: t, ws: ws, fp: fpOf(id), vtracks: make(chan *webrtc.TrackRemote, 8),
		states: make(chan proto.VoiceState, 64),
		errs:   make(chan string, 16), vsrc: make(chan proto.VideoSource, 16),
		msrc: make(chan proto.MediaSource, 64), mpkts: make(chan mediaPacket, 256)}
	me := &webrtc.MediaEngine{}
	_ = me.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, PayloadType: 111}, webrtc.RTPCodecTypeAudio)
	_ = me.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
		RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}}}, PayloadType: 96}, webrtc.RTPCodecTypeVideo)
	se := webrtc.SettingEngine{}
	loopbackOnly(&se)
	api := webrtc.NewAPI(webrtc.WithMediaEngine(me), webrtc.WithSettingEngine(se))
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	p.pc = pc
	t.Cleanup(func() { _ = pc.Close(); _ = ws.Close() })

	audio, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio-"+p.fp, p.fp)
	if _, err := pc.AddTrack(audio); err != nil {
		t.Fatal(err)
	}
	p.audio = audio
	if modern {
		for _, spec := range []struct {
			kind proto.MediaKind
			id   string
		}{{proto.MediaCamera, cameraPublishTrackID}, {proto.MediaScreen, screenPublishTrackID}} {
			track, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, spec.id, p.fp)
			sender, err := pc.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
			if err != nil {
				t.Fatal(err)
			}
			if spec.kind == proto.MediaCamera {
				p.camera = track
			} else {
				p.screen = track
			}
			go func(s *webrtc.RTPSender) {
				for {
					if _, _, err := s.ReadRTCP(); err != nil {
						return
					}
				}
			}(sender.Sender())
		}
		for i := 0; i < proto.MaxCameraSlots+1; i++ {
			if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
				t.Fatal(err)
			}
		}
	} else if publish {
		v, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, "video-"+p.fp, p.fp)
		snd, err := pc.AddTrack(v)
		if err != nil {
			t.Fatal(err)
		}
		p.video = v
		go func() {
			for {
				pkts, _, err := snd.ReadRTCP()
				if err != nil {
					return
				}
				for _, pk := range pkts {
					if _, ok := pk.(*rtcp.PictureLossIndication); ok {
						p.plis.Add(1)
					}
				}
			}
		}()
	}
	pc.OnICECandidate(func(ci *webrtc.ICECandidate) {
		if ci == nil {
			return
		}
		raw, _ := json.Marshal(ci.ToJSON())
		p.send(proto.TypeRTCCandidate, proto.RTCCandidate{Candidate: string(raw)})
	})
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		isVideo := tr.Kind() == webrtc.RTPCodecTypeVideo
		if isVideo {
			p.vtracks <- tr
		}
		go func() {
			for {
				pkt, _, err := tr.ReadRTP()
				if err != nil {
					return
				}
				if isVideo {
					if len(pkt.Payload) > 3 {
						p.vtag.Store(int32(pkt.Payload[3]))
					}
					p.vpkts.Add(1)
					if key, ok := parseSlotTrackID(tr.ID()); ok {
						tag := byte(0)
						if len(pkt.Payload) > 3 {
							tag = pkt.Payload[3]
						}
						select {
						case p.mpkts <- mediaPacket{trackID: slotTrackID(key), seq: pkt.SequenceNumber, tag: tag}:
						default:
						}
					}
				} else {
					p.apkts.Add(1)
				}
			}
		}()
	})
	go p.readLoop()
	return p
}

func (p *testPeer) send(typ string, payload any) {
	raw, err := proto.Encode(typ, payload)
	if err != nil {
		return
	}
	p.wmu.Lock()
	_ = p.ws.WriteMessage(websocket.TextMessage, raw)
	p.wmu.Unlock()
}

func (p *testPeer) readLoop() {
	for {
		_, raw, err := p.ws.ReadMessage()
		if err != nil {
			return
		}
		env, err := proto.Decode(raw)
		if err != nil {
			continue
		}
		switch env.Type {
		case proto.TypeVideoSource:
			var m proto.VideoSource
			if json.Unmarshal(env.Data, &m) == nil {
				select {
				case p.vsrc <- m:
				default:
				}
			}
		case proto.TypeMediaSource:
			var m proto.MediaSource
			if json.Unmarshal(env.Data, &m) == nil {
				select {
				case p.msrc <- m:
				default:
				}
			}
		case proto.TypeError:
			var m proto.ErrorMsg
			_ = json.Unmarshal(env.Data, &m)
			select {
			case p.errs <- m.Code + ": " + m.Message:
			default:
			}
		case proto.TypeVoiceState:
			var v proto.VoiceState
			_ = json.Unmarshal(env.Data, &v)
			select {
			case p.states <- v:
			default:
			}
		case proto.TypeRTCOffer:
			var m proto.RTCOffer
			_ = json.Unmarshal(env.Data, &m)
			if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}); err != nil {
				continue
			}
			ans, err := p.pc.CreateAnswer(nil)
			if err == nil {
				err = p.pc.SetLocalDescription(ans)
			}
			if err != nil {
				continue
			}
			p.send(proto.TypeRTCAnswer, proto.RTCAnswer{SDP: ans.SDP})
		case proto.TypeRTCAnswer:
			var m proto.RTCAnswer
			_ = json.Unmarshal(env.Data, &m)
			_ = p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
		case proto.TypeRTCCandidate:
			var m proto.RTCCandidate
			var ci webrtc.ICECandidateInit
			if json.Unmarshal(env.Data, &m) == nil && json.Unmarshal([]byte(m.Candidate), &ci) == nil {
				_ = p.pc.AddICECandidate(ci)
			}
		}
	}
}

// join enters lounge and runs the initial client offer.
func (p *testPeer) join() {
	p.t.Helper()
	p.send(proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	off, err := p.pc.CreateOffer(nil)
	if err == nil {
		err = p.pc.SetLocalDescription(off)
	}
	if err != nil {
		p.t.Fatal(err)
	}
	p.send(proto.TypeRTCOffer, proto.RTCOffer{SDP: off.SDP})
}

func (p *testPeer) waitState(pred func(proto.VoiceState) bool) proto.VoiceState {
	p.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case v := <-p.states:
			if pred(v) {
				return v
			}
		case <-deadline:
			p.t.Fatal("timed out waiting for voice_state")
		}
	}
}

// pump sends synthetic VP8 RTP from the sharer until stop is closed.
func (p *testPeer) pump(stop <-chan struct{}) {
	go func() {
		seq := uint16(0)
		ts := uint32(0)
		tk := time.NewTicker(10 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
			}
			seq++
			ts += 900
			_ = p.video.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: ts, Marker: true},
				Payload: []byte{0x10, 0x00, 0x01, p.tag, 0x03, 0x04}})
		}
	}()
}

func eventually(t *testing.T, d time.Duration, what string, f func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func shareScene(t *testing.T) (sharer, viewer, other *testPeer) {
	t.Helper()
	url := startServer(t, shareCfg())
	sharer = newTestPeer(t, url, true)
	viewer = newTestPeer(t, url, false)
	other = newTestPeer(t, url, false)
	sharer.join()
	viewer.join()
	other.join()
	sharer.send(proto.TypeShareStart, proto.ShareStart{})
	viewer.waitState(func(v proto.VoiceState) bool { return sharingOf(v, sharer.fp) && len(v.Members) == 3 })
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	sharer.pump(stop)
	return
}

func TestVideoOnlyToWatchers(t *testing.T) {
	sharer, viewer, other := shareScene(t)
	time.Sleep(500 * time.Millisecond)
	if viewer.vpkts.Load() != 0 || other.vpkts.Load() != 0 {
		t.Fatal("video delivered before any watch")
	}
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp})
	eventually(t, 10*time.Second, "viewer receives VP8", func() bool { return viewer.vpkts.Load() > 5 })
	time.Sleep(300 * time.Millisecond)
	if other.vpkts.Load() != 0 || len(other.vtracks) != 0 {
		t.Fatal("non-watcher received video")
	}
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: ""})
	time.Sleep(700 * time.Millisecond)
	n := viewer.vpkts.Load()
	time.Sleep(500 * time.Millisecond)
	if viewer.vpkts.Load() > n+2 {
		t.Fatal("video still flowing after unwatch")
	}
}

func TestWatchSendsPLI(t *testing.T) {
	sharer, viewer, _ := shareScene(t)
	base := sharer.plis.Load()
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp})
	eventually(t, time.Second, "PLI at sharer after watch", func() bool { return sharer.plis.Load() > base })
}

func TestPLIRelayRateLimited(t *testing.T) {
	sharer, viewer, _ := shareScene(t)
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp})
	var tr *webrtc.TrackRemote
	select {
	case tr = <-viewer.vtracks:
	case <-time.After(10 * time.Second):
		t.Fatal("no video track at viewer")
	}
	eventually(t, 10*time.Second, "video flowing", func() bool { return viewer.vpkts.Load() > 0 })
	time.Sleep(1100 * time.Millisecond) // let the limiter window pass
	base := sharer.plis.Load()
	for i := 0; i < 5; i++ {
		_ = viewer.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(tr.SSRC())}})
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	got := sharer.plis.Load() - base
	if got < 1 || got > 2 {
		t.Fatalf("%d PLIs reached the sharer from 5 viewer PLIs in <1 s; want 1..2", got)
	}
}

func TestShareClearedOnLeave(t *testing.T) {
	sharer, viewer, _ := shareScene(t)
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp})
	eventually(t, 10*time.Second, "viewer receives VP8", func() bool { return viewer.vpkts.Load() > 5 })
	sharer.send(proto.TypeVoiceLeave, proto.VoiceLeave{})
	viewer.waitState(func(v proto.VoiceState) bool { return !sharingOf(v, sharer.fp) && len(v.Members) == 2 })
	time.Sleep(500 * time.Millisecond)
	n := viewer.vpkts.Load()
	time.Sleep(500 * time.Millisecond)
	if viewer.vpkts.Load() > n+2 {
		t.Fatal("video still flowing after the sharer left")
	}
	// The sharer comes back and shares again: the old watch must not return.
	// (Its published video track is gone with its old PC, so it re-publishes
	// by sending a fresh offer; the viewer must still not receive anything.)
	sharer.send(proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	sharer.send(proto.TypeShareStart, proto.ShareStart{})
	viewer.waitState(func(v proto.VoiceState) bool { return sharingOf(v, sharer.fp) })
	n = viewer.vpkts.Load()
	time.Sleep(600 * time.Millisecond)
	if viewer.vpkts.Load() > n+2 {
		t.Fatal("re-share restored a watch that leaving should have cleared")
	}
}

func videoTransceivers(pc *webrtc.PeerConnection) int {
	n := 0
	for _, tr := range pc.GetTransceivers() {
		if tr.Kind() == webrtc.RTPCodecTypeVideo {
			n++
		}
	}
	return n
}

func videoMLines(pc *webrtc.PeerConnection) int {
	rd := pc.RemoteDescription()
	if rd == nil {
		return 0
	}
	return strings.Count(rd.SDP, "\nm=video")
}

func TestWatchToggleReusesVideoTransceiver(t *testing.T) {
	sharer, viewer, _ := shareScene(t)
	start := time.Now()
	for i := 0; i < 20; i++ {
		viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp})
		viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: ""})
		time.Sleep(5 * time.Millisecond)
	}
	elapsed := time.Since(start)
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp})
	eventually(t, 10*time.Second, "viewer receives VP8 after toggling", func() bool { return viewer.vpkts.Load() > 5 })
	if n := videoTransceivers(viewer.pc); n != 1 {
		t.Fatalf("viewer has %d video transceivers after 20 toggles, want 1", n)
	}
	if n := videoMLines(viewer.pc); n != 1 {
		t.Fatalf("viewer SDP has %d video m-lines, want 1", n)
	}
	// Server-initiated keyframes are spaced: toggling must not make the
	// sharer emit one per watch.
	if max := int(elapsed/forcedPLIInterval) + 3; int(sharer.plis.Load()) > max {
		t.Fatalf("%d PLIs in %v of toggling, want <= %d", sharer.plis.Load(), elapsed, max)
	}
}

func TestRepeatedWatchIsNoop(t *testing.T) {
	sharer, viewer, _ := shareScene(t)
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp})
	eventually(t, 10*time.Second, "viewer receives VP8", func() bool { return viewer.vpkts.Load() > 5 })
	time.Sleep(1200 * time.Millisecond)
	base := sharer.plis.Load()
	for i := 0; i < 10; i++ {
		viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp})
	}
	time.Sleep(800 * time.Millisecond)
	if got := sharer.plis.Load() - base; got != 0 {
		t.Fatalf("repeated watch of the same sharer sent %d PLIs", got)
	}
}

func TestWatchSwitchReusesTransceiver(t *testing.T) {
	url := startServer(t, shareCfg())
	a := newTestPeer(t, url, true)
	b := newTestPeer(t, url, true)
	a.tag, b.tag = 0xA1, 0xB2
	viewer := newTestPeer(t, url, false)
	a.join()
	b.join()
	viewer.join()
	a.send(proto.TypeShareStart, proto.ShareStart{})
	b.send(proto.TypeShareStart, proto.ShareStart{})
	viewer.waitState(func(v proto.VoiceState) bool { return sharingOf(v, a.fp) && sharingOf(v, b.fp) })
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	a.pump(stop)
	b.pump(stop)
	for i := 0; i < 6; i++ {
		who, tag := a, int32(0xA1)
		if i%2 == 1 {
			who, tag = b, 0xB2
		}
		viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: who.fp})
		eventually(t, 10*time.Second, "viewer gets the switched stream", func() bool { return viewer.vtag.Load() == tag })
	}
	if n := videoTransceivers(viewer.pc); n != 1 {
		t.Fatalf("viewer has %d video transceivers after switching, want 1", n)
	}
}

func TestVideoRelayedOnlyWhileSharing(t *testing.T) {
	sharer, viewer, _ := shareScene(t)
	sharer.send(proto.TypeShareStop, proto.ShareStop{})
	viewer.waitState(func(v proto.VoiceState) bool { return !sharingOf(v, sharer.fp) })
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp}) // refused: not sharing
	time.Sleep(500 * time.Millisecond)
	if viewer.vpkts.Load() != 0 {
		t.Fatal("video relayed from a peer that is not sharing")
	}
}

func TestShareStopClearsWatch(t *testing.T) {
	sharer, viewer, _ := shareScene(t)
	viewer.send(proto.TypeWatch, proto.Watch{Fingerprint: sharer.fp})
	eventually(t, 10*time.Second, "viewer receives VP8", func() bool { return viewer.vpkts.Load() > 5 })
	sharer.send(proto.TypeShareStop, proto.ShareStop{})
	viewer.waitState(func(v proto.VoiceState) bool { return !sharingOf(v, sharer.fp) })
	time.Sleep(500 * time.Millisecond)
	n := viewer.vpkts.Load()
	time.Sleep(500 * time.Millisecond)
	if viewer.vpkts.Load() > n+2 {
		t.Fatal("video still flowing after share_stop")
	}
	// sharing again must not resurrect the old watch
	sharer.send(proto.TypeShareStart, proto.ShareStart{})
	viewer.waitState(func(v proto.VoiceState) bool { return sharingOf(v, sharer.fp) })
	n = viewer.vpkts.Load()
	time.Sleep(500 * time.Millisecond)
	if viewer.vpkts.Load() > n+2 {
		t.Fatal("watch survived share_stop")
	}
}
