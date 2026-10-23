package client

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/proto"
)

func mediaTestTracks(t *testing.T) (*webrtc.TrackLocalStaticSample, map[proto.MediaKind]*webrtc.TrackLocalStaticSample) {
	t.Helper()
	audio, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
	}, "audio-test", "test")
	if err != nil {
		t.Fatal(err)
	}
	video, err := newVideoTracks("test")
	if err != nil {
		t.Fatal(err)
	}
	return audio, video
}

func mediaTestPC(t *testing.T) *webrtc.PeerConnection {
	t.Helper()
	c := &Client{}
	pc, err := c.newAPI().NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	return pc
}

func localOffer(t *testing.T, pc *webrtc.PeerConnection) string {
	t.Helper()
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	return pc.LocalDescription().SDP
}

func TestModernOfferHasTwoPublishTracksAndElevenReceiveSlots(t *testing.T) {
	pc := mediaTestPC(t)
	audio, video := mediaTestTracks(t)
	senders, err := addInitialMediaTracks(pc, audio, video, true, false)
	if err != nil {
		t.Fatal(err)
	}

	sdp := localOffer(t, pc)
	if got := len(senders); got != 2 {
		t.Fatalf("video senders = %d, want 2", got)
	}
	if got := strings.Count(sdp, "m=video "); got != 2+proto.MaxCameraSlots+1 {
		t.Fatalf("video m-lines = %d, want 13", got)
	}
	for _, id := range []string{cameraPublishTrackID, screenPublishTrackID} {
		if !strings.Contains(sdp, " "+id+"\r\n") {
			t.Fatalf("offer lacks stable publish track ID %q", id)
		}
	}
	for _, kind := range []proto.MediaKind{proto.MediaCamera, proto.MediaScreen} {
		if senders[kind] == nil || senders[kind].Track() == nil {
			t.Fatalf("missing %s sender", kind)
		}
	}
	for _, want := range []slotKey{{Kind: proto.MediaCamera, Index: 9}, {Kind: proto.MediaScreen, Index: 0}} {
		if got, ok := parseSlotTrackID(slotTrackID(want)); !ok || got != want {
			t.Fatalf("slot ID round trip = %+v, %v; want %+v", got, ok, want)
		}
	}
}

func TestLegacyOfferKeepsSingleOnDemandScreenTrack(t *testing.T) {
	audio, video := mediaTestTracks(t)
	pc := mediaTestPC(t)
	if _, err := addInitialMediaTracks(pc, audio, video, false, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(localOffer(t, pc), "m=video "); got != 0 {
		t.Fatalf("initial legacy offer has %d video m-lines, want 0", got)
	}

	audio, video = mediaTestTracks(t)
	pc = mediaTestPC(t)
	if _, err := addInitialMediaTracks(pc, audio, video, false, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(localOffer(t, pc), "m=video "); got != 1 {
		t.Fatalf("sharing legacy offer has %d video m-lines, want 1", got)
	}
}
