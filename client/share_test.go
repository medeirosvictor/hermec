package client

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtcp"

	"github.com/medeirosvictor/hermec/video"
)

// countSource returns a fresh frame instantly, every call.
type countSource struct{ n atomic.Int64 }

func (s *countSource) Next(dst *video.Frame) error { s.n.Add(1); return nil }

// fakeEnc is a frameEncoder with a configurable cost that records its calls.
type fakeEnc struct {
	delay time.Duration

	mu    sync.Mutex
	calls int
	keys  []bool
}

func (e *fakeEnc) Encode(f *video.Frame, forceKey bool) ([]byte, bool, error) {
	time.Sleep(e.delay)
	e.mu.Lock()
	e.calls++
	e.keys = append(e.keys, forceKey)
	e.mu.Unlock()
	return []byte{1, 2, 3}, forceKey, nil
}
func (e *fakeEnc) Close() {}

func (e *fakeEnc) snapshot() (int, []bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls, append([]bool(nil), e.keys...)
}

func TestShareSkipsWhenEncoderBusy(t *testing.T) {
	src := &countSource{}
	enc := &fakeEnc{delay: 150 * time.Millisecond}
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
	if m := sl.maxPending.Load(); m > 1 {
		t.Fatalf("pending queue grew to %d", m)
	}
	if wrote.Load() != int64(calls) {
		t.Fatalf("wrote %d packets for %d encodes", wrote.Load(), calls)
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
