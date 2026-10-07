package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// stubServer speaks just enough of the protocol to drive JoinVoice: auth,
// voice_join -> voice_state, and (optionally) a real pion answerer.
type stubServer struct {
	t      *testing.T
	url    string
	answer bool // act as an SFU answerer; false = never answer offers

	mu   sync.Mutex
	seen []string // client message types, in order
	pc   *webrtc.PeerConnection
}

func (s *stubServer) sawType(typ string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.seen {
		if x == typ {
			return true
		}
	}
	return false
}

func newStub(t *testing.T, answer bool) *stubServer {
	s := &stubServer{t: t, answer: answer}
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		s.serve(ws)
	}))
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
		s.mu.Lock()
		if s.pc != nil {
			_ = s.pc.Close()
		}
		s.mu.Unlock()
	})
	s.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	return s
}

func (s *stubServer) serve(ws *websocket.Conn) {
	var wmu sync.Mutex
	send := func(typ string, v any) {
		raw, _ := proto.Encode(typ, v)
		wmu.Lock()
		defer wmu.Unlock()
		_ = ws.WriteMessage(websocket.TextMessage, raw)
	}
	send(proto.TypeChallenge, proto.Challenge{Nonce: []byte("nonce-nonce-nonce")})
	var fp string
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		env, err := proto.Decode(raw)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.seen = append(s.seen, env.Type)
		s.mu.Unlock()
		switch env.Type {
		case proto.TypeAuth:
			var a proto.Auth
			_ = json.Unmarshal(env.Data, &a)
			fp = identity.Fingerprint(a.PubKey)
			send(proto.TypeAuthOK, proto.AuthOK{Fingerprint: fp, Roles: []string{"user"},
				Channels: []proto.ChannelInfo{{Name: "lobby", Type: "voice"}}})
		case proto.TypeVoiceJoin:
			send(proto.TypeVoiceState, proto.VoiceState{Channel: "lobby",
				Members: []proto.VoiceMember{{Fingerprint: fp, Name: "alice"}}})
		case proto.TypeRTCOffer:
			if s.answer {
				s.answerOffer(env, send)
			}
		case proto.TypeRTCCandidate:
			s.mu.Lock()
			pc := s.pc
			s.mu.Unlock()
			if pc != nil && pc.RemoteDescription() != nil {
				var m proto.RTCCandidate
				var ci webrtc.ICECandidateInit
				_ = json.Unmarshal(env.Data, &m)
				if json.Unmarshal([]byte(m.Candidate), &ci) == nil {
					_ = pc.AddICECandidate(ci)
				}
			}
		case proto.TypeChatSend:
			var m proto.ChatSend
			_ = json.Unmarshal(env.Data, &m)
			send(proto.TypeChatMessage, proto.ChatMessage{Channel: m.Channel, Text: m.Text})
		}
	}
}

func (s *stubServer) answerOffer(env proto.Envelope, send func(string, any)) {
	var m proto.RTCOffer
	_ = json.Unmarshal(env.Data, &m)
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	me := &webrtc.MediaEngine{}
	_ = me.RegisterDefaultCodecs()
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(se), webrtc.WithMediaEngine(me)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		s.t.Errorf("stub pc: %v", err)
		return
	}
	s.mu.Lock()
	s.pc = pc
	s.mu.Unlock()
	pc.OnICECandidate(func(ci *webrtc.ICECandidate) {
		if ci == nil {
			return
		}
		raw, _ := json.Marshal(ci.ToJSON())
		send(proto.TypeRTCCandidate, proto.RTCCandidate{Candidate: string(raw)})
	})
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}); err != nil {
		s.t.Errorf("stub SetRemote: %v", err)
		return
	}
	ans, err := pc.CreateAnswer(nil)
	if err == nil {
		err = pc.SetLocalDescription(ans)
	}
	if err != nil {
		s.t.Errorf("stub answer: %v", err)
		return
	}
	send(proto.TypeRTCAnswer, proto.RTCAnswer{SDP: pc.LocalDescription().SDP})
}

type nopSource struct{ stop chan struct{} }

func (n nopSource) ReadOpusFrame() ([]byte, error) {
	select {
	case <-n.stop:
		return nil, errors.New("eof")
	case <-time.After(20 * time.Millisecond):
		return []byte{0xF8, 0xFF, 0xFE}, nil
	}
}

type nopSink struct{}

func (nopSink) WriteOpusFrame(string, []byte) {}

func dialStub(t *testing.T, s *stubServer) *Client {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	c, err := Dial(testCtx(t), s.url, id, "alice", "")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	c.SetLoopbackICE(true)
	t.Cleanup(func() { c.Close() })
	go func() {
		for range c.Events() {
		}
	}()
	return c
}

func TestJoinVoiceHappyPath(t *testing.T) {
	s := newStub(t, true)
	c := dialStub(t, s)
	if c.VoiceChannel() != "" {
		t.Fatal("VoiceChannel before join should be empty")
	}
	src := nopSource{stop: make(chan struct{})}
	if err := c.JoinVoice(testCtx(t), "lobby", src, nopSink{}); err != nil {
		t.Fatalf("JoinVoice: %v", err)
	}
	if got := c.VoiceChannel(); got != "lobby" {
		t.Fatalf("VoiceChannel = %q", got)
	}
	// State machine order: voice_join strictly before the first rtc_offer.
	s.mu.Lock()
	seen := append([]string(nil), s.seen...)
	s.mu.Unlock()
	vj, ro := -1, -1
	for i, x := range seen {
		if x == proto.TypeVoiceJoin && vj < 0 {
			vj = i
		}
		if x == proto.TypeRTCOffer && ro < 0 {
			ro = i
		}
	}
	if vj < 0 || ro < 0 || vj > ro {
		t.Fatalf("message order = %v", seen)
	}
	if err := c.JoinVoice(testCtx(t), "lobby", src, nopSink{}); !errors.Is(err, ErrInVoice) {
		t.Fatalf("second JoinVoice err = %v, want ErrInVoice", err)
	}
	if err := c.SetMuted(true); err != nil {
		t.Fatal(err)
	}
	if err := c.LeaveVoice(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	if c.VoiceChannel() != "" {
		t.Fatal("VoiceChannel after leave should be empty")
	}
	if err := c.SetMuted(true); !errors.Is(err, ErrNotInVoice) {
		t.Fatalf("SetMuted outside call = %v", err)
	}
}

func TestJoinVoiceTimeoutLeavesClientUsable(t *testing.T) {
	s := newStub(t, false) // never answers
	c := dialStub(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	err := c.JoinVoice(ctx, "lobby", nopSource{stop: make(chan struct{})}, nopSink{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("JoinVoice err = %v, want DeadlineExceeded", err)
	}
	if c.VoiceChannel() != "" {
		t.Fatal("call should be torn down")
	}
	deadline := time.Now().Add(3 * time.Second)
	for !s.sawType(proto.TypeVoiceLeave) {
		if time.Now().After(deadline) {
			t.Fatal("client did not send voice_leave after timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Chat still works and a later join is allowed.
	if err := c.SendChat(testCtx(t), "lobby", "hi"); err != nil {
		t.Fatalf("SendChat after timeout: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	if err := c.JoinVoice(ctx2, "lobby", nopSource{stop: make(chan struct{})}, nopSink{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second JoinVoice err = %v", err)
	}
}

func TestJoinVoiceServerRejects(t *testing.T) {
	s := newStub(t, false)
	c := dialStub(t, s)
	// The stub only accepts "lobby"-style joins; make it fail by closing the
	// connection under the client instead: JoinVoice must report ErrClosed.
	done := make(chan error, 1)
	go func() {
		done <- c.JoinVoice(testCtx(t), "lobby", nopSource{stop: make(chan struct{})}, nopSink{})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !s.sawType(proto.TypeRTCOffer) {
		if time.Now().After(deadline) {
			t.Fatal("no offer")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = c.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("JoinVoice succeeded on a closed client")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("JoinVoice did not return after Close")
	}
}
