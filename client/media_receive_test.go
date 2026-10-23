package client

import (
	"context"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/video"
)

type recordingMediaSink struct{ frames chan MediaFrame }

func (s *recordingMediaSink) WriteMediaFrame(frame MediaFrame) { s.frames <- frame }

func encodedFrames(t *testing.T, seq uint16, shade byte) (key, delta []*rtp.Packet) {
	t.Helper()
	enc, err := video.NewEncoder(16, 16, 15, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	frame := video.NewFrame(16, 16)
	for i := range frame.Y {
		frame.Y[i] = shade
	}
	for i := range frame.U {
		frame.U[i], frame.V[i] = 128, 128
	}
	keyData, isKey, err := enc.Encode(frame, true)
	if err != nil || !isKey || len(keyData) == 0 {
		t.Fatalf("encode key: key=%v len=%d err=%v", isKey, len(keyData), err)
	}
	keyData = append([]byte(nil), keyData...)
	frame.Y[0]++
	deltaData, _, err := enc.Encode(frame, false)
	if err != nil || len(deltaData) == 0 {
		t.Fatalf("encode delta: len=%d err=%v", len(deltaData), err)
	}
	packetize := func(data []byte, ts uint32) []*rtp.Packet {
		pay := &codecs.VP8Payloader{}
		chunks := pay.Payload(400, data)
		out := make([]*rtp.Packet, 0, len(chunks))
		for i, payload := range chunks {
			out = append(out, &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: ts, Marker: i == len(chunks)-1}, Payload: payload})
			seq++
		}
		return out
	}
	return packetize(keyData, 3000), packetize(append([]byte(nil), deltaData...), 6000)
}

func pushPackets(r *mediaReceiver, packets ...[]*rtp.Packet) {
	for _, group := range packets {
		for _, packet := range group {
			r.push(packet)
		}
	}
}

func receiveMediaFrames(t *testing.T, sink *recordingMediaSink, n int) []MediaFrame {
	t.Helper()
	frames := make([]MediaFrame, 0, n)
	deadline := time.After(5 * time.Second)
	for len(frames) < n {
		select {
		case frame := <-sink.frames:
			frames = append(frames, frame)
		case <-deadline:
			t.Fatalf("received %d/%d media frames", len(frames), n)
		}
	}
	return frames
}

func TestMediaReceiversDeliverConcurrentCameraAndScreenFrames(t *testing.T) {
	c := &Client{}
	sink := &recordingMediaSink{frames: make(chan MediaFrame, 16)}
	c.SetMediaSink(sink)
	keys := []slotKey{{Kind: proto.MediaCamera, Index: 0}, {Kind: proto.MediaCamera, Index: 1}, {Kind: proto.MediaScreen, Index: 0}}
	fps := []string{"camera-a", "camera-b", "screen-c"}
	gens := []uint32{0, 0, 7}
	watching(c, "screen-c", 7, 0)
	receivers := make([]*mediaReceiver, len(keys))
	for i, key := range keys {
		c.onMediaSource(proto.MediaSource{Kind: key.Kind, Slot: key.Index, Fingerprint: fps[i], Seq: uint16(100 + i*100), Gen: gens[i]})
		receiver, err := newMediaReceiver(c, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		receivers[i] = receiver
		t.Cleanup(receiver.close)
		keyPackets, deltaPackets := encodedFrames(t, uint16(100+i*100), byte(40+i*20))
		go pushPackets(receiver, keyPackets, deltaPackets)
	}

	got := receiveMediaFrames(t, sink, 3)
	seen := make(map[string]MediaFrame)
	for _, frame := range got {
		seen[frame.Fingerprint] = frame
	}
	for i, fp := range fps {
		frame, ok := seen[fp]
		if !ok || frame.Kind != keys[i].Kind || frame.Slot != keys[i].Index || frame.Gen != gens[i] || frame.Frame == nil || frame.Frame.W != 16 || frame.Frame.H != 16 {
			t.Fatalf("frame for %s = %+v", fp, frame)
		}
	}
}

func TestReassignedSlotDropsOldAndPreKeyframePackets(t *testing.T) {
	c := &Client{}
	sink := &recordingMediaSink{frames: make(chan MediaFrame, 16)}
	c.SetMediaSink(sink)
	key := slotKey{Kind: proto.MediaCamera, Index: 0}
	r, err := newMediaReceiver(c, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()

	c.onMediaSource(proto.MediaSource{Kind: key.Kind, Slot: key.Index, Fingerprint: "A", Seq: 10})
	aKey, aDelta := encodedFrames(t, 10, 50)
	pushPackets(r, aKey, aDelta)
	if first := receiveMediaFrames(t, sink, 1)[0]; first.Fingerprint != "A" {
		t.Fatalf("first frame = %+v", first)
	}

	c.onMediaSource(proto.MediaSource{Kind: key.Kind, Slot: key.Index, Fingerprint: "B", Seq: 100})
	late := bFrames(60, 10000, 3, true)
	deltas := bFrames(100, 20000, 4, false)
	pushPackets(r, late, deltas)
	select {
	case frame := <-sink.frames:
		t.Fatalf("rendered before B keyframe: %+v", frame)
	default:
	}
	bKey, bDelta := encodedFrames(t, deltas[len(deltas)-1].SequenceNumber+1, 90)
	pushPackets(r, bKey, bDelta)
	if replacement := receiveMediaFrames(t, sink, 1)[0]; replacement.Fingerprint != "B" {
		t.Fatalf("replacement frame = %+v", replacement)
	}
}

func TestStaleMediaSourceGenerationCannotReplaceCurrentScreen(t *testing.T) {
	c := &Client{}
	aGen, aWait := c.beginWatch("A")
	c.endWatch(aWait)
	bGen, _ := c.beginWatch("B")
	c.onMediaSource(proto.MediaSource{Kind: proto.MediaScreen, Slot: 0, Fingerprint: "B", Seq: 200, Gen: bGen})
	c.onMediaSource(proto.MediaSource{Kind: proto.MediaScreen, Slot: 0, Fingerprint: "A", Seq: 100, Gen: aGen})

	state := c.mediaSource(slotKey{Kind: proto.MediaScreen, Index: 0})
	if state.fingerprint != "B" || state.seq != 200 || state.gen != bGen {
		t.Fatalf("stale source replaced current screen: %+v", state)
	}
}

func TestMediaReceiversModernWatchUsesMediaSubscribe(t *testing.T) {
	s := newStub(t, true)
	s.mediaLanes = true
	c := dialStub(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stop := make(chan struct{})
	defer close(stop)
	if err := c.JoinVoice(ctx, "lobby", nopSource{stop: stop}, nopSink{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Watch("screen-publisher"); err != nil {
		t.Fatal(err)
	}
	if !s.sawType(proto.TypeMediaSubscribe) || s.sawType(proto.TypeWatch) {
		t.Fatal("modern watch did not use media_subscribe exclusively")
	}
}
