package client

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/video"
)

type laneSource struct {
	n    atomic.Int64
	fail atomic.Bool
}

func TestStartShareUsesNegotiatedMediaMessages(t *testing.T) {
	s := newStub(t, true)
	s.mediaLanes = true
	c := dialStub(t, s)
	if err := c.JoinVoice(testCtx(t), "lobby", nopSource{stop: make(chan struct{})}, nopSink{}); err != nil {
		t.Fatal(err)
	}
	if err := c.StartShare(&countSource{}, 16, 16, 15, 100); err != nil {
		t.Fatal(err)
	}
	if !s.waitType(proto.TypeMediaStart) {
		t.Fatalf("messages = %v, want media_start", s.seen)
	}
	if err := c.StopShare(); err != nil {
		t.Fatal(err)
	}
	if !s.waitType(proto.TypeMediaStop) {
		t.Fatalf("messages = %v, want media_start and media_stop", s.seen)
	}
	if s.sawType(proto.TypeShareStart) || s.sawType(proto.TypeShareStop) {
		t.Fatalf("modern share used legacy messages: %v", s.seen)
	}
}

func (s *laneSource) Next(dst *video.Frame) error {
	if s.fail.Load() {
		return errors.New("camera disconnected")
	}
	n := s.n.Add(1)
	dst.Y[0], dst.Y[1] = byte(n), byte(n>>8)
	return nil
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestPublishersRunCameraAndScreenIndependently(t *testing.T) {
	cameraSrc, screenSrc := &laneSource{}, &laneSource{}
	cameraWrites, screenWrites := make(chan struct{}, 16), make(chan struct{}, 16)
	cameraFailed := make(chan struct{}, 1)
	camera := newVideoPublisher(proto.MediaCamera, cameraSrc, &fakeEnc{}, 16, 16, 60, func([]byte, time.Duration) error {
		cameraWrites <- struct{}{}
		return nil
	})
	screen := newVideoPublisher(proto.MediaScreen, screenSrc, &fakeEnc{}, 16, 16, 60, func([]byte, time.Duration) error {
		screenWrites <- struct{}{}
		return nil
	})
	camera.onFail = func(error) { cameraFailed <- struct{}{} }
	camera.start()
	screen.start()
	defer camera.stop()
	defer screen.stop()

	waitSignal(t, cameraWrites, "camera frame")
	waitSignal(t, screenWrites, "screen frame")
	if camera.kind != proto.MediaCamera || screen.kind != proto.MediaScreen {
		t.Fatalf("publisher kinds = %q/%q", camera.kind, screen.kind)
	}

	cameraSrc.fail.Store(true)
	waitSignal(t, cameraFailed, "camera failure")
	for len(screenWrites) > 0 {
		<-screenWrites
	}
	waitSignal(t, screenWrites, "screen frame after camera failure")
}

type countCloseEncoder struct {
	fakeEnc
	closeCalls atomic.Int64
}

func (e *countCloseEncoder) Close() { e.closeCalls.Add(1) }

func TestPublisherKeepsNewestFrameAndStopsOnce(t *testing.T) {
	src := &countSource{}
	enc := &countCloseEncoder{fakeEnc: fakeEnc{delay: 80 * time.Millisecond, src: src}}
	wrote := make(chan struct{}, 16)
	p := newVideoPublisher(proto.MediaScreen, src, enc, 16, 16, 60, func([]byte, time.Duration) error {
		wrote <- struct{}{}
		return nil
	})
	p.start()
	waitSignal(t, wrote, "first encoded frame")
	waitSignal(t, wrote, "second encoded frame")

	enc.mu.Lock()
	stamps := append([]int64(nil), enc.stamps...)
	enc.mu.Unlock()
	if len(stamps) < 2 || stamps[1] <= stamps[0]+1 {
		t.Fatalf("encoded stamps = %v; pending frames were queued instead of replaced", stamps)
	}

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); p.stop() }()
	}
	wg.Wait()
	if got := enc.closeCalls.Load(); got != 1 {
		t.Fatalf("encoder Close calls = %d, want 1", got)
	}
}
