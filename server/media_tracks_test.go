package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/proto"
)

func TestServerRejectsExtraOrMisorderedVideoTracks(t *testing.T) {
	valid := []offeredMedia{
		{kind: "audio", direction: "sendrecv"},
		{kind: "video", direction: "sendonly", trackID: cameraPublishTrackID},
		{kind: "video", direction: "sendonly", trackID: screenPublishTrackID},
	}
	for i := 0; i < proto.MaxCameraSlots+1; i++ {
		valid = append(valid, offeredMedia{kind: "video", direction: "recvonly"})
	}
	if layout := classifyOffer(valid); !layout.valid || !layout.audio {
		t.Fatalf("valid layout rejected: %+v", layout)
	}

	bad := append([]offeredMedia(nil), valid...)
	bad[1], bad[2] = bad[2], bad[1]
	layout := classifyOffer(bad)
	if layout.valid || !layout.audio || len(layout.publish) != 0 {
		t.Fatalf("misordered video layout = %+v, want rejection", layout)
	}

	extra := append([]offeredMedia(nil), valid...)
	extra = append(extra, offeredMedia{kind: "video", direction: "sendonly", trackID: "extra"})
	layout = classifyOffer(extra)
	if layout.valid || !layout.audio || len(layout.publish) != 0 {
		t.Fatalf("extra video layout = %+v, want rejection", layout)
	}

	s, url := startServerS(t, shareCfg())
	badPeer := newTestPeer(t, url)
	var videos []*webrtc.TrackLocalStaticRTP
	for i, id := range []string{screenPublishTrackID, cameraPublishTrackID} {
		track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, id, badPeer.fp)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := badPeer.pc.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly}); err != nil {
			t.Fatalf("add bad publish track %d: %v", i, err)
		}
		videos = append(videos, track)
	}
	for i := 0; i < proto.MaxCameraSlots+1; i++ {
		if _, err := badPeer.pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			t.Fatal(err)
		}
	}
	pub := newTestPeer(t, url)
	listener := newTestPeer(t, url)
	pub.join()
	listener.join()
	badPeer.join()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	pumpAudio(stop, pub.audio)
	go func() {
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		var seq uint16
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				seq++
				for _, track := range videos {
					_ = track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: uint32(seq) * 3000}, Payload: []byte{0x10, 0, 1, 9}})
				}
			}
		}
	}()

	select {
	case got := <-badPeer.errs:
		if !strings.HasPrefix(got, proto.CodeBadRequest+":") {
			t.Fatalf("error = %q, want bad_request", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no error for the malformed video layout")
	}
	vs := waitSession(t, s, "lounge")
	eventually(t, 10*time.Second, "well-formed listener hears the well-formed publisher", func() bool { return listener.apkts.Load() > 0 })
	onActor(t, vs, func() {
		p := peerByFP(vs, badPeer.fp)
		if p == nil {
			t.Fatal("rejected peer missing")
		}
		if p.pc != nil {
			t.Error("rejected peer kept its media")
		}
		for _, f := range p.fwds {
			if f.kind != "" && !f.isDead() {
				t.Fatal("malformed video layout created a publication")
			}
		}
	})
}

func TestOfferFitsProtocolAtMaxVoice(t *testing.T) {
	s := New(voiceCfg())
	pc, err := s.newRTCAPI().NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	for i := 0; i < 49+maxInactiveLines; i++ {
		track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		}, fmt.Sprintf("audio-%d", i), fmt.Sprintf("peer-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pc.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < proto.MaxCameraSlots+1; i++ {
		key := slotKey{Kind: proto.MediaCamera, Index: i}
		if i == proto.MaxCameraSlots {
			key = slotKey{Kind: proto.MediaScreen, Index: 0}
		}
		track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, slotTrackID(key), "hermec-media")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pc.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly}); err != nil {
			t.Fatal(err)
		}
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Encode(proto.TypeRTCOffer, proto.RTCOffer{SDP: offer.SDP})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > proto.MaxMessageSize {
		t.Fatalf("max-voice offer = %d bytes, max = %d", len(raw), proto.MaxMessageSize)
	}
}

func TestOfferAnswerHasStableReceiveSlotIDs(t *testing.T) {
	clientPC, serverPC := offerPair(t)
	offer, err := clientPC.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientPC.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	layout := classifyOffer(parseOfferedMedia(offer.SDP))
	if !layout.valid {
		t.Fatalf("generated offer rejected: %+v", layout)
	}
	if err := serverPC.SetRemoteDescription(offer); err != nil {
		t.Fatal(err)
	}
	vs := &voiceSession{}
	p := &peer{}
	if err := vs.configureAnswer(p, serverPC, layout); err != nil {
		t.Fatal(err)
	}
	answer, err := serverPC.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []slotKey{{Kind: proto.MediaCamera, Index: 0}, {Kind: proto.MediaCamera, Index: 9}, {Kind: proto.MediaScreen, Index: 0}} {
		id := slotTrackID(key)
		if !strings.Contains(answer.SDP, " "+id+"\r\n") {
			t.Fatalf("answer lacks stable slot track ID %q", id)
		}
		if got, ok := parseSlotTrackID(id); !ok || got != key {
			t.Fatalf("slot ID round trip = %+v, %v; want %+v", got, ok, key)
		}
	}
}

func offerPair(t *testing.T) (*webrtc.PeerConnection, *webrtc.PeerConnection) {
	t.Helper()
	s := New(voiceCfg())
	clientPC, err := s.newRTCAPI().NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	serverPC, err := s.newRTCAPI().NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientPC.Close(); _ = serverPC.Close() })
	audio, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio-test", "test")
	if _, err := clientPC.AddTrack(audio); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{cameraPublishTrackID, screenPublishTrackID} {
		track, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, id, "test")
		if _, err := clientPC.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < proto.MaxCameraSlots+1; i++ {
		if _, err := clientPC.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			t.Fatal(err)
		}
	}
	return clientPC, serverPC
}
