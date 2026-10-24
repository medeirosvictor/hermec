package server

import (
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/proto"
)

func pumpModernVideo(stop <-chan struct{}, tag byte, tracks ...*webrtc.TrackLocalStaticRTP) {
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
				for _, track := range tracks {
					_ = track.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: uint32(seq) * 3000, Marker: true}, Payload: []byte{0x10, 0, 1, tag}})
				}
			}
		}
	}()
}

func joinModernScene(t *testing.T, n int) (*Server, []*testPeer, chan struct{}) {
	t.Helper()
	s, url := startServerS(t, shareCfg())
	peers := make([]*testPeer, n)
	for i := range peers {
		peers[i] = newModernTestPeer(t, url, false)
		peers[i].join()
	}
	peers[0].waitState(func(v proto.VoiceState) bool { return len(v.Members) == n })
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	return s, peers, stop
}

func waitMediaSources(t *testing.T, p *testPeer, kind proto.MediaKind, n int) []proto.MediaSource {
	t.Helper()
	got := make([]proto.MediaSource, 0, n)
	mediaDeadline := time.After(10 * time.Second)
	for len(got) < n {
		select {
		case m := <-p.msrc:
			if m.Kind == kind && m.Fingerprint != "" {
				got = append(got, m)
			}
		case <-mediaDeadline:
			t.Fatalf("got %d/%d %s media sources", len(got), n, kind)
		}
	}
	return got
}

func TestCamerasAutoAssignStableSlotsForEveryModernViewer(t *testing.T) {
	s, peers, stop := joinModernScene(t, 4)
	for i := 0; i < 3; i++ {
		peers[i].send(proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaCamera})
		pumpModernVideo(stop, byte(0xA0+i), peers[i].camera)
	}
	sources := waitMediaSources(t, peers[3], proto.MediaCamera, 3)
	wantPublishers := map[string]bool{peers[0].fp: false, peers[1].fp: false, peers[2].fp: false}
	bySlot := make(map[int]proto.MediaSource)
	for _, source := range sources {
		if _, ok := wantPublishers[source.Fingerprint]; !ok {
			t.Fatalf("unexpected camera source %+v", source)
		}
		wantPublishers[source.Fingerprint] = true
		bySlot[source.Slot] = source
	}
	for i := 0; i < 3; i++ {
		if _, ok := bySlot[i]; !ok {
			t.Fatalf("camera sources did not fill lowest slots: %+v", sources)
		}
	}

	peers[1].send(proto.TypeMediaStop, proto.MediaStop{Kind: proto.MediaCamera})
	var detached proto.MediaSource
	deadline := time.After(5 * time.Second)
	for detached.Fingerprint != "" || detached.Kind != proto.MediaCamera {
		select {
		case detached = <-peers[3].msrc:
		case <-deadline:
			t.Fatal("no camera detach source")
		}
	}
	if detached.Slot != slotForFingerprint(sources, peers[1].fp) {
		t.Fatalf("detach = %+v, want only %s's original slot", detached, peers[1].fp)
	}
	vs := waitSession(t, s, "lounge")
	onActor(t, vs, func() {
		viewer := peerByFP(vs, peers[3].fp)
		for _, source := range sources {
			if source.Fingerprint == peers[1].fp {
				continue
			}
			slot := viewer.slots[slotKey{Kind: proto.MediaCamera, Index: source.Slot}]
			if slot == nil || slot.source != source.Fingerprint {
				t.Fatalf("camera %s moved from stable slot %d", source.Fingerprint, source.Slot)
			}
		}
	})
}

func slotForFingerprint(sources []proto.MediaSource, fp string) int {
	for _, source := range sources {
		if source.Fingerprint == fp {
			return source.Slot
		}
	}
	return -1
}

func TestSelectedScreenCoexistsWithCameraSlots(t *testing.T) {
	_, peers, stop := joinModernScene(t, 4)
	for i := 0; i < 2; i++ {
		peers[i].send(proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaCamera})
		pumpModernVideo(stop, byte(0xA0+i), peers[i].camera)
	}
	peers[2].send(proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaScreen})
	peers[3].waitState(func(v proto.VoiceState) bool { return mediaOf(v, peers[2].fp, proto.MediaScreen) })
	peers[3].send(proto.TypeMediaSubscribe, proto.MediaSubscribe{Kind: proto.MediaScreen, Fingerprint: peers[2].fp, Gen: 7})
	pumpModernVideo(stop, 0xCC, peers[2].screen)
	cameras := 0
	var screen proto.MediaSource
	mediaDeadline := time.After(10 * time.Second)
	for cameras < 2 || screen.Fingerprint == "" {
		select {
		case source := <-peers[3].msrc:
			switch source.Kind {
			case proto.MediaCamera:
				if source.Fingerprint != "" {
					cameras++
				}
			case proto.MediaScreen:
				if source.Fingerprint != "" {
					screen = source
				}
			}
		case <-mediaDeadline:
			t.Fatalf("media sources: cameras=%d/2 screen=%+v", cameras, screen)
		}
	}
	if screen.Slot != 0 || screen.Gen != 7 || screen.Fingerprint != peers[2].fp {
		t.Fatalf("screen source = %+v", screen)
	}

	want := map[string]bool{"camera-slot-0": false, "camera-slot-1": false, "screen-slot-0": false}
	deadline := time.After(10 * time.Second)
	for {
		all := true
		for _, seen := range want {
			all = all && seen
		}
		if all {
			break
		}
		select {
		case pkt := <-peers[3].mpkts:
			if _, ok := want[pkt.trackID]; ok {
				want[pkt.trackID] = true
			}
		case <-deadline:
			t.Fatalf("simultaneous slot packets = %v", want)
		}
	}
}

func TestSlotReuseAnnouncesBoundaryBeforeNewPackets(t *testing.T) {
	_, peers, stop := joinModernScene(t, 3)
	peers[0].send(proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaCamera})
	pumpModernVideo(stop, 0xA1, peers[0].camera)
	first := waitMediaSources(t, peers[2], proto.MediaCamera, 1)[0]
	if first.Slot != 0 {
		t.Fatalf("first camera slot = %d, want 0", first.Slot)
	}
	peers[0].send(proto.TypeMediaStop, proto.MediaStop{Kind: proto.MediaCamera})
	detachDeadline := time.After(5 * time.Second)
	for detached := false; !detached; {
		select {
		case m := <-peers[2].msrc:
			detached = m.Kind == proto.MediaCamera && m.Slot == 0 && m.Fingerprint == ""
		case <-detachDeadline:
			t.Fatal("no camera slot detach before reuse")
		}
	}
	peers[1].send(proto.TypeMediaStart, proto.MediaStart{Kind: proto.MediaCamera})
	pumpModernVideo(stop, 0xB2, peers[1].camera)
	second := waitMediaSources(t, peers[2], proto.MediaCamera, 1)[0]
	if second.Slot != 0 || second.Fingerprint != peers[1].fp {
		t.Fatalf("reused source = %+v", second)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case pkt := <-peers[2].mpkts:
			if pkt.trackID == "camera-slot-0" && pkt.tag == 0xB2 {
				if pkt.seq != second.Seq {
					t.Fatalf("first replacement packet seq = %d, announced boundary = %d", pkt.seq, second.Seq)
				}
				return
			}
		case <-deadline:
			t.Fatal("no replacement camera packet")
		}
	}
}
