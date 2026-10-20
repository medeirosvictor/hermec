package client

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/video"
)

// countSource returns a fresh frame instantly, every call.
type countSource struct{ n atomic.Int64 }

// Next stamps the grab sequence number into the first two luma bytes.
func (s *countSource) Next(dst *video.Frame) error {
	n := s.n.Add(1)
	dst.Y[0], dst.Y[1] = byte(n), byte(n>>8)
	return nil
}

// fakeEnc is a frameEncoder with a configurable cost that records its calls.
type fakeEnc struct {
	delay     time.Duration
	failFirst int // the first N Encode calls return an error
	src       *countSource

	mu     sync.Mutex
	calls  int
	keys   []bool
	stamps []int64 // grab sequence number of each encoded frame
	behind []int64 // grabs made after that frame when it reached the encoder
	closed bool
}

func (e *fakeEnc) Encode(f *video.Frame, forceKey bool) ([]byte, bool, error) {
	stamp := int64(f.Y[0]) | int64(f.Y[1])<<8
	var behind int64
	if e.src != nil {
		behind = e.src.n.Load() - stamp
	}
	time.Sleep(e.delay)
	e.mu.Lock()
	e.calls++
	e.keys = append(e.keys, forceKey)
	e.stamps = append(e.stamps, stamp)
	e.behind = append(e.behind, behind)
	fail := e.calls <= e.failFirst
	e.mu.Unlock()
	if fail {
		return nil, false, errors.New("boom")
	}
	return []byte{1, 2, 3}, forceKey, nil
}
func (e *fakeEnc) Close() { e.mu.Lock(); e.closed = true; e.mu.Unlock() }

func (e *fakeEnc) isClosed() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.closed }

func (e *fakeEnc) snapshot() (int, []bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls, append([]bool(nil), e.keys...)
}

func TestShareSkipsWhenEncoderBusy(t *testing.T) {
	src := &countSource{}
	enc := &fakeEnc{delay: 150 * time.Millisecond, src: src}
	var wrote atomic.Int64
	sl := newShareLoop(src, enc, 16, 16, 60, func([]byte, time.Duration) error { wrote.Add(1); return nil })
	sl.start()
	time.Sleep(900 * time.Millisecond)
	sl.stop()

	calls, _ := enc.snapshot()
	grabbed := src.n.Load()
	if calls > 8 {
		t.Fatalf("encoder ran %d times in 0.9s at 150ms each: frames were queued", calls)
	}
	if grabbed < 20 {
		t.Fatalf("source only polled %d times: grabbing must not wait for the encoder", grabbed)
	}
	if sl.dropped.Load() == 0 {
		t.Fatal("no frames reported dropped")
	}
	// The encoder always gets the newest grab, never a stale queued one: the
	// stamps rise with gaps (the skipped grabs were dropped), and each frame
	// was at most a couple of grabs behind the source when it was taken.
	enc.mu.Lock()
	stamps, behind := append([]int64(nil), enc.stamps...), append([]int64(nil), enc.behind...)
	enc.mu.Unlock()
	if len(stamps) < 3 {
		t.Fatalf("only %d frames encoded", len(stamps))
	}
	for i := 1; i < len(stamps); i++ {
		if stamps[i] <= stamps[i-1]+1 {
			t.Fatalf("encoded grab #%d after #%d: no grabs were skipped, frames queued: %v", stamps[i], stamps[i-1], stamps)
		}
	}
	for i, b := range behind {
		if b > 3 {
			t.Fatalf("frame %d (grab #%d) was %d grabs old when encoded: not the newest", i, stamps[i], b)
		}
	}
	if wrote.Load() != int64(calls) {
		t.Fatalf("wrote %d packets for %d encodes", wrote.Load(), calls)
	}
}

// vp8Frames packetizes n fake frames of size bytes each, returning one packet
// list per frame (3000 ticks apart, marker on the last packet).
func vp8Frames(n, size int) [][]*rtp.Packet {
	pay := &codecs.VP8Payloader{}
	var seq uint16
	out := make([][]*rtp.Packet, n)
	for i := range out {
		data := make([]byte, size)
		data[0] = byte(i)
		chunks := pay.Payload(400, data)
		for j, c := range chunks {
			seq++
			out[i] = append(out[i], &rtp.Packet{
				Header:  rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: uint32(i+1) * 3000, Marker: j == len(chunks)-1},
				Payload: c,
			})
		}
	}
	return out
}

func TestSampleBuilderRecoversFromLoss(t *testing.T) {
	frames := vp8Frames(40, 1200)
	if len(frames[2]) < 2 {
		t.Fatal("test frame must span several packets")
	}
	sb := newVideoSampleBuilder()
	got := 0
	var lastTS uint32
	for i, f := range frames {
		for j, p := range f {
			if i == 2 && j == 1 {
				continue // lost for good
			}
			sb.Push(p)
			for s := sb.Pop(); s != nil; s = sb.Pop() {
				got++
				lastTS = s.PacketTimestamp
			}
		}
	}
	if got < 30 {
		t.Fatalf("only %d of 40 frames came out after one lost packet", got)
	}
	if lastTS < 38*3000 {
		t.Fatalf("stream stalled at timestamp %d", lastTS)
	}
}

// feed pushes pkts through a videoRx and reports the frames and whether any
// push saw unrecovered loss.
func feed(rx *videoRx, pkts []*rtp.Packet) (frames int, lost bool) {
	for _, p := range pkts {
		fs, l := rx.push(p)
		frames += len(fs)
		lost = lost || l
	}
	return
}

// A gap that NACK repairs (the packet shows up late or reordered) must not
// ask the sharer for a keyframe.
func TestVideoRxFilledGapIsNotLoss(t *testing.T) {
	frames := vp8Frames(40, 1200)
	if len(frames[2]) < 3 {
		t.Fatal("test frame must span several packets")
	}
	var order []*rtp.Packet
	var held *rtp.Packet
	for i, f := range frames {
		for j, p := range f {
			if i == 2 && j == 1 {
				held = p // arrives 3 frames late, as a NACK retransmission would
				continue
			}
			order = append(order, p)
		}
		if i == 5 {
			order = append(order, held)
		}
	}
	got, lost := feed(newVideoRx(), order)
	if lost {
		t.Fatal("a repaired gap was reported as unrecovered loss")
	}
	if got < 39 { // the builder holds the newest frame until a later one arrives
		t.Fatalf("decoded %d of 40 frames", got)
	}
}

// A packet that never arrives is reported once the builder gives up on it, so
// the viewer can ask for a keyframe.
func TestVideoRxPermanentLossIsReported(t *testing.T) {
	frames := vp8Frames(40, 1200)
	var order []*rtp.Packet
	for i, f := range frames {
		for j, p := range f {
			if i == 2 && j == 1 {
				continue // lost for good
			}
			order = append(order, p)
		}
	}
	got, lost := feed(newVideoRx(), order)
	if !lost {
		t.Fatal("permanent loss was not reported")
	}
	if got < 30 {
		t.Fatalf("only %d of 40 frames after one lost packet", got)
	}
}

func TestStartShareOnClosedCall(t *testing.T) {
	c := &Client{}
	cl := &call{c: c}
	c.call = cl
	enc := &fakeEnc{}
	old := newEncoder
	newEncoder = func(w, h, fps, kbps int) (frameEncoder, error) { return enc, nil }
	defer func() { newEncoder = old }()

	if sl := cl.closeShare(); sl != nil {
		t.Fatal("unexpected loop")
	}
	if err := c.StartShare(&countSource{}, 16, 16, 15, 100); err != ErrNotInVoice {
		t.Fatalf("StartShare on a torn-down call = %v, want ErrNotInVoice", err)
	}
	if cl.share != nil {
		t.Fatal("loop installed on a dead call")
	}
	if !enc.isClosed() {
		t.Fatal("encoder leaked")
	}
}

func TestStartShareValidatesArgs(t *testing.T) {
	c := &Client{}
	c.call = &call{c: c}
	for _, a := range [][4]int{{0, 16, 15, 100}, {16, 0, 15, 100}, {16, 16, 0, 100}, {16, 16, 61, 100}, {16, 16, 15, 0}} {
		if err := c.StartShare(&countSource{}, a[0], a[1], a[2], a[3]); err == nil {
			t.Errorf("StartShare%v accepted", a)
		}
	}
}

func TestEncodeErrorRestoresKeyRequest(t *testing.T) {
	enc := &fakeEnc{failFirst: 1}
	var errs atomic.Int64
	sl := newShareLoop(&countSource{}, enc, 16, 16, 60, func([]byte, time.Duration) error { return nil })
	sl.onEncodeErr = func(error) { errs.Add(1) }
	sl.start()
	defer sl.stop()
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, keys := enc.snapshot()
		if c >= 3 {
			if !keys[0] || !keys[1] {
				t.Fatalf("keyframe request lost after an encode error: %v", keys)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("encoder stalled")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if errs.Load() != 1 {
		t.Fatalf("encode errors reported = %d, want 1", errs.Load())
	}
}

func TestPLIForcesKeyframe(t *testing.T) {
	src := &countSource{}
	enc := &fakeEnc{}
	sl := newShareLoop(src, enc, 16, 16, 30, func([]byte, time.Duration) error { return nil })
	sl.start()
	defer sl.stop()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if c, _ := enc.snapshot(); c >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("encoder never ran")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, before := enc.snapshot()
	for _, k := range before[1:] { // the first frame is always a keyframe
		if k {
			t.Fatal("keyframe forced without a PLI")
		}
	}
	sl.onSenderRTCP([]rtcp.Packet{&rtcp.ReceiverReport{}, &rtcp.PictureLossIndication{}})
	for {
		_, keys := enc.snapshot()
		forced := false
		for _, k := range keys[len(before):] {
			forced = forced || k
		}
		if forced {
			return
		}
		if time.Now().After(deadline.Add(3 * time.Second)) {
			t.Fatal("PLI did not force a keyframe")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// bFrames packetizes n frames starting at sequence number seq0. The first is a
// keyframe when key is set; the rest are delta frames. ts0 spaces them 3000
// ticks apart.
func bFrames(seq0 uint16, ts0 uint32, n int, key bool) []*rtp.Packet {
	pay := &codecs.VP8Payloader{}
	var out []*rtp.Packet
	seq := seq0
	for i := 0; i < n; i++ {
		data := make([]byte, 900)
		data[0] = 1 // delta frame
		if i == 0 && key {
			data[0] = 0
		}
		chunks := pay.Payload(400, data)
		for j, c := range chunks {
			out = append(out, &rtp.Packet{
				Header:  rtp.Header{Version: 2, SequenceNumber: seq, Timestamp: ts0 + uint32(i)*3000, Marker: j == len(chunks)-1},
				Payload: c,
			})
			seq++
		}
	}
	return out
}

func feedGate(g *srcGate, pkts []*rtp.Packet) (frames [][]byte) {
	for _, p := range pkts {
		fs, _, _ := g.push(p)
		frames = append(frames, fs...)
	}
	return
}

// Packets of the previous source that arrive after the switch (sequence
// numbers before the boundary), keyframe included, must never produce a frame;
// the new source's packets from the boundary on must.
func TestSrcGateDropsPreBoundaryKeyframe(t *testing.T) {
	for _, boundary := range []uint16{1000, 65530} { // the second wraps the sequence space
		var g srcGate
		g.start(boundary)
		// Source A: a keyframe and deltas, all numbered before the boundary.
		old := bFrames(boundary-40, 0, 8, true)
		if last := old[len(old)-1].SequenceNumber; !seqBefore(last, boundary) {
			t.Fatalf("test setup: A ends at %d, not before %d", last, boundary)
		}
		if got := feedGate(&g, old); len(got) != 0 {
			t.Fatalf("boundary %d: %d frames from the old source were released", boundary, len(got))
		}
		// Source B from the boundary on, a keyframe first.
		got := feedGate(&g, bFrames(boundary, 1<<20, 8, true))
		if len(got) < 6 {
			t.Fatalf("boundary %d: only %d frames from the new source", boundary, len(got))
		}
		if !isVP8Key(got[0]) {
			t.Fatalf("boundary %d: first released frame is not a keyframe", boundary)
		}
	}
}

// Before the boundary is known the gate is closed, and after it nothing is
// released until a keyframe arrives.
func TestSrcGateNeedsKeyframe(t *testing.T) {
	var g srcGate
	if got := feedGate(&g, bFrames(1, 0, 8, true)); len(got) != 0 {
		t.Fatalf("%d frames released before any boundary", len(got))
	}
	g.start(500)
	var needKey bool
	var released int
	deltas := bFrames(500, 0, 6, false)
	for _, p := range deltas { // deltas only
		fs, _, nk := g.push(p)
		released += len(fs)
		needKey = needKey || nk
	}
	if released != 0 || !needKey {
		t.Fatalf("deltas without a keyframe: released=%d needKey=%v", released, needKey)
	}
	if got := feedGate(&g, bFrames(deltas[len(deltas)-1].SequenceNumber+1, 1<<20, 6, true)); len(got) == 0 || !isVP8Key(got[0]) {
		t.Fatalf("no keyframe-led frames after a keyframe arrived (%d)", len(got))
	}
}

func TestSeqBefore(t *testing.T) {
	cases := []struct {
		a, b uint16
		want bool
	}{{1, 2, true}, {2, 1, false}, {5, 5, false}, {65535, 0, true}, {0, 65535, false}, {65530, 3, true}}
	for _, c := range cases {
		if got := seqBefore(c.a, c.b); got != c.want {
			t.Errorf("seqBefore(%d,%d)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestOnVideoSourceIgnoresStale(t *testing.T) {
	c := &Client{}
	c.watchFP, c.watchGen = "B", 2
	c.onVideoSource(proto.VideoSource{Fingerprint: "A", Seq: 9})
	if w := c.watchSnapshot(); w.have {
		t.Fatal("video_source for a sharer we are not watching was accepted")
	}
	c.onVideoSource(proto.VideoSource{Fingerprint: "B", Seq: 77})
	if w := c.watchSnapshot(); !w.have || w.seq != 77 {
		t.Fatalf("video_source for the watched sharer not recorded: %+v", w)
	}
}
