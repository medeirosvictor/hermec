package server

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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

// testPeer is a WebRTC client with HERMEC's media layout: audio, camera and
// screen publish tracks plus the receive slots.
type testPeer struct {
	t   *testing.T
	ws  *websocket.Conn
	fp  string
	pc  *webrtc.PeerConnection
	wmu sync.Mutex

	camera *webrtc.TrackLocalStaticRTP
	screen *webrtc.TrackLocalStaticRTP
	vpkts  atomic.Int64
	apkts  atomic.Int64

	states chan proto.VoiceState
	audio  *webrtc.TrackLocalStaticRTP // the audio track this peer publishes
	errs   chan string                 // messages of server error frames
	msrc   chan proto.MediaSource      // media_source messages received
	mpkts  chan mediaPacket            // received slot packets
}

type mediaPacket struct {
	trackID string
	seq     uint16
	tag     byte
}

func newTestPeer(t *testing.T, url string) *testPeer {
	t.Helper()
	ws, id := authed(t, url)
	p := &testPeer{t: t, ws: ws, fp: fpOf(id),
		states: make(chan proto.VoiceState, 64), errs: make(chan string, 16),
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
	pc.OnICECandidate(func(ci *webrtc.ICECandidate) {
		if ci == nil {
			return
		}
		raw, _ := json.Marshal(ci.ToJSON())
		p.send(proto.TypeRTCCandidate, proto.RTCCandidate{Candidate: string(raw)})
	})
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		isVideo := tr.Kind() == webrtc.RTPCodecTypeVideo
		go func() {
			for {
				pkt, _, err := tr.ReadRTP()
				if err != nil {
					return
				}
				if !isVideo {
					p.apkts.Add(1)
					continue
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
