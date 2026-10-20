package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/proto"
)

func startServerS(t *testing.T, cfg Config) (*Server, string) {
	t.Helper()
	cfg.Addr = "127.0.0.1:0"
	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, "ws://" + s.Addr()
}

// onActor runs f on the session actor and waits for it.
func onActor(t *testing.T, vs *voiceSession, f func()) {
	t.Helper()
	done := make(chan struct{})
	vs.post(func() { f(); close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("actor did not run closure")
	}
}

func peerByFP(vs *voiceSession, fp string) *peer {
	for c, p := range vs.peers {
		if c.fingerprint == fp {
			return p
		}
	}
	return nil
}

// pumpAudio writes synthetic Opus RTP on tracks until stop is closed.
func pumpAudio(stop <-chan struct{}, tracks ...*webrtc.TrackLocalStaticRTP) {
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
			ts += 480
			for _, tr := range tracks {
				_ = tr.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: seq, Timestamp: ts},
					Payload: []byte{0xF8, 0xFF, 0xFE}})
			}
		}
	}()
}

func TestSecondAudioTrackIgnored(t *testing.T) {
	s, url := startServerS(t, shareCfg())
	a := newTestPeer(t, url, false)
	extra, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio2-"+a.fp, a.fp)
	if _, err := a.pc.AddTrack(extra); err != nil {
		t.Fatal(err)
	}
	b := newTestPeer(t, url, false)
	a.join()
	b.join()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	pumpAudio(stop, a.audio, extra)

	vs := sessionFor(s, "lounge")
	count := func() int {
		n := 0
		onActor(t, vs, func() {
			if p := peerByFP(vs, a.fp); p != nil {
				for _, f := range p.fwds {
					if !f.video && !f.isDead() {
						n++
					}
				}
			}
		})
		return n
	}
	eventually(t, 10*time.Second, "first audio track forwarded", func() bool { return count() >= 1 })
	time.Sleep(500 * time.Millisecond) // the second track would have arrived by now
	if n := count(); n != 1 {
		t.Fatalf("%d audio forwarders for one peer, want 1", n)
	}
}

func TestAudioTransceiversStayBoundedOverChurn(t *testing.T) {
	s, url := startServerS(t, shareCfg())
	stay := newTestPeer(t, url, false)
	stay.join()
	vs := sessionFor(s, "lounge")
	max := 0
	for i := 0; i < 50; i++ {
		c := newTestPeer(t, url, false)
		c.join()
		stop := make(chan struct{})
		pumpAudio(stop, c.audio)
		eventually(t, 10*time.Second, "churner's audio forwarded", func() bool {
			ok := false
			onActor(t, vs, func() {
				if p := peerByFP(vs, c.fp); p != nil {
					ok = len(p.fwds) > 0
				}
			})
			return ok
		})
		onActor(t, vs, func() {
			if p := peerByFP(vs, stay.fp); p != nil && p.pc != nil {
				if n := len(p.pc.GetTransceivers()); n > max {
					max = n
				}
			}
		})
		close(stop)
		_ = c.pc.Close()
		_ = c.ws.Close()
		eventually(t, 10*time.Second, "churner left", func() bool {
			gone := false
			onActor(t, vs, func() { gone = peerByFP(vs, c.fp) == nil })
			return gone
		})
	}
	// own receive line + live lines + inactive lines up to the rebuild threshold
	// (bound = live + maxInactiveLines + slack).
	if bound := 2 + maxInactiveLines + 1; max > bound {
		t.Fatalf("staying member's PC grew to %d transceivers over 50 join/leave cycles, want <= %d", max, bound)
	}
}

func TestOversizeOfferResetsPeer(t *testing.T) {
	cfg := shareCfg()
	cfg.maxOfferBytes = 200 // any real offer is bigger
	s, url := startServerS(t, cfg)
	a := newTestPeer(t, url, false)
	b := newTestPeer(t, url, false)
	a.join()
	b.join()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	pumpAudio(stop, a.audio)

	var got string
	select {
	case got = <-b.errs:
	case <-time.After(10 * time.Second):
		t.Fatal("no error for the oversize offer")
	}
	if !strings.Contains(got, "send a new rtc_offer") {
		t.Fatalf("error = %q, want the media-reset hint", got)
	}
	vs := sessionFor(s, "lounge")
	onActor(t, vs, func() {
		if p := peerByFP(vs, b.fp); p == nil || p.pc != nil || p.negotiating {
			t.Errorf("peer media not reset: %+v", p)
		}
	})
}

func TestVoiceFull(t *testing.T) {
	cfg := voiceCfg()
	cfg.MaxVoice = 2
	url := startServer(t, cfg)
	var cs [3]*websocket.Conn
	for i := range cs {
		cs[i], _ = authed(t, url)
	}
	joinLounge(t, cs[0])
	joinLounge(t, cs[1])
	readVoiceUntil(t, cs[1], func(v proto.VoiceState) bool { return len(v.Members) == 2 })
	joinLounge(t, cs[2])
	if code := readErrorCode(t, cs[2]); code != proto.CodeVoiceFull {
		t.Fatalf("code = %q, want voice_full", code)
	}
	// A member leaving frees a slot.
	sendEnv(t, cs[0], proto.TypeVoiceLeave, struct{}{})
	readVoiceUntil(t, cs[1], func(v proto.VoiceState) bool { return len(v.Members) == 1 })
	joinLounge(t, cs[2])
	readVoiceUntil(t, cs[2], func(v proto.VoiceState) bool { return len(v.Members) == 2 })
}
