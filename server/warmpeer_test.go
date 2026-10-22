package server

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/proto"
)

// warmRTC is a pion client signaling over a warm conn: warm_* until it
// joins voice, rtc_* after.
type warmRTC struct {
	t   *testing.T
	ws  *websocket.Conn
	pc  *webrtc.PeerConnection
	dc  *webrtc.DataChannel
	wmu sync.Mutex

	voice atomic.Bool // set once the client has sent voice_join
	open  chan struct{}
	msgs  chan string
}

// newWarmClient auths warm and offers the "hermec-warm" data channel.
func newWarmClient(t *testing.T, url string) *warmRTC {
	t.Helper()
	ws, env := authWarm(t, url, mustID(t), "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("warm auth: %q", env.Type)
	}
	se := webrtc.SettingEngine{}
	loopbackOnly(&se)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	w := &warmRTC{t: t, ws: ws, pc: pc, open: make(chan struct{}), msgs: make(chan string, 8)}
	t.Cleanup(func() { _ = pc.Close(); _ = ws.Close() })
	dc, err := pc.CreateDataChannel("hermec-warm", nil)
	if err != nil {
		t.Fatal(err)
	}
	w.dc = dc
	dc.OnOpen(func() { close(w.open) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) { w.msgs <- string(m.Data) })
	pc.OnICECandidate(func(ci *webrtc.ICECandidate) {
		if ci == nil {
			return
		}
		raw, _ := json.Marshal(ci.ToJSON())
		typ := proto.TypeWarmCandidate
		if w.voice.Load() {
			typ = proto.TypeRTCCandidate
		}
		w.send(typ, proto.RTCCandidate{Candidate: string(raw)})
	})
	go w.readLoop()
	off, err := pc.CreateOffer(nil)
	if err == nil {
		err = pc.SetLocalDescription(off)
	}
	if err != nil {
		t.Fatal(err)
	}
	w.send(proto.TypeWarmOffer, proto.WarmOffer{SDP: off.SDP})
	return w
}

func (w *warmRTC) send(typ string, payload any) {
	raw, err := proto.Encode(typ, payload)
	if err != nil {
		return
	}
	w.wmu.Lock()
	_ = w.ws.WriteMessage(websocket.TextMessage, raw)
	w.wmu.Unlock()
}

func (w *warmRTC) readLoop() {
	for {
		_, raw, err := w.ws.ReadMessage()
		if err != nil {
			return
		}
		env, err := proto.Decode(raw)
		if err != nil {
			continue
		}
		switch env.Type {
		case proto.TypeWarmAnswer, proto.TypeRTCAnswer:
			var m proto.RTCAnswer
			_ = json.Unmarshal(env.Data, &m)
			_ = w.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
		case proto.TypeWarmCandidate, proto.TypeRTCCandidate:
			var m proto.RTCCandidate
			var ci webrtc.ICECandidateInit
			if json.Unmarshal(env.Data, &m) == nil && json.Unmarshal([]byte(m.Candidate), &ci) == nil {
				_ = w.pc.AddICECandidate(ci)
			}
		case proto.TypeRTCOffer:
			var m proto.RTCOffer
			_ = json.Unmarshal(env.Data, &m)
			if w.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}) != nil {
				continue
			}
			ans, err := w.pc.CreateAnswer(nil)
			if err == nil {
				err = w.pc.SetLocalDescription(ans)
			}
			if err == nil {
				w.send(proto.TypeRTCAnswer, proto.RTCAnswer{SDP: ans.SDP})
			}
		}
	}
}

func (w *warmRTC) waitOpen() {
	w.t.Helper()
	select {
	case <-w.open:
	case <-time.After(10 * time.Second):
		w.t.Fatal("data channel did not open")
	}
}

func warmCfg() Config {
	c := shareCfg()
	c.Mode = ModeStandby
	return c
}

// onlyWarmConn returns the server's single warm conn.
func onlyWarmConn(t *testing.T, s *Server) *conn {
	t.Helper()
	s.chMu.Lock()
	defer s.chMu.Unlock()
	if len(s.warm) != 1 {
		t.Fatalf("%d warm conns, want 1", len(s.warm))
	}
	for c := range s.warm {
		return c
	}
	return nil
}

func (p *peer) pcOrNil() *webrtc.PeerConnection {
	if p == nil {
		return nil
	}
	return p.pc
}

func TestWarmPeerDataChannel(t *testing.T) {
	_, url := startModeServer(t, warmCfg())
	w := newWarmClient(t, url)
	w.waitOpen()
	if err := w.dc.SendText("ping"); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-w.msgs:
		if m != "pong" {
			t.Fatalf("reply = %q, want pong", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no pong within 3s")
	}
}

func TestWarmPeerAdoptedOnVoiceJoin(t *testing.T) {
	s, url := startModeServer(t, warmCfg())
	w := newWarmClient(t, url)
	w.waitOpen()
	wc := onlyWarmConn(t, s)
	warmPC := s.warmPeer(wc)
	if warmPC == nil {
		t.Fatal("no warm peer registered")
	}

	s.SetMode(ModeActive)
	s.PromoteWarm()
	if s.warmPeer(wc) != warmPC {
		t.Fatal("warm peer lost on promotion")
	}

	// A second member to receive the audio.
	b := newTestPeer(t, url, false)
	var got atomic.Int64
	b.pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			if _, _, err := tr.ReadRTP(); err != nil {
				return
			}
			got.Add(1)
		}
	})
	b.join()

	audio, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio-w", "w")
	if _, err := w.pc.AddTrack(audio); err != nil {
		t.Fatal(err)
	}
	w.voice.Store(true)
	w.send(proto.TypeVoiceJoin, proto.VoiceJoin{Channel: "lounge"})
	off, err := w.pc.CreateOffer(nil)
	if err == nil {
		err = w.pc.SetLocalDescription(off)
	}
	if err != nil {
		t.Fatal(err)
	}
	w.send(proto.TypeRTCOffer, proto.RTCOffer{SDP: off.SDP})
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	pumpAudio(stop, audio)

	vs := waitSession(t, s, "lounge")
	eventually(t, 10*time.Second, "warm peer's audio reaches the second member", func() bool { return got.Load() > 0 })
	onActor(t, vs, func() {
		if p := vs.peers[wc]; p == nil || p.pc != warmPC {
			t.Errorf("voice peer pc = %p, want the warm pc %p", p.pcOrNil(), warmPC)
		}
	})
	if s.warmPeer(wc) != nil {
		t.Error("warm peer still registered after adoption")
	}
}

func TestWarmPeerClosedWithConn(t *testing.T) {
	s, url := startModeServer(t, warmCfg())
	w := newWarmClient(t, url)
	w.waitOpen()
	_ = w.ws.Close()
	eventually(t, 5*time.Second, "warm peer released", func() bool {
		s.warmMu.Lock()
		defer s.warmMu.Unlock()
		return len(s.warmPeers) == 0
	})
}
