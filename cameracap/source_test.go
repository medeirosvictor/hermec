package cameracap

import (
	"errors"
	"image"
	"image/color"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/video"
)

type readResult struct {
	image   image.Image
	release func()
	err     error
}

type fakeReader struct {
	results chan readResult
	closed  chan struct{}
	closes  atomic.Int32
}

func newFakeReader() *fakeReader {
	return &fakeReader{results: make(chan readResult, 8), closed: make(chan struct{})}
}

func (r *fakeReader) Read() (image.Image, func(), error) {
	select {
	case result := <-r.results:
		return result.image, result.release, result.err
	case <-r.closed:
		return nil, nil, errors.New("reader closed")
	}
}

func (r *fakeReader) Close() error {
	if r.closes.Add(1) == 1 {
		close(r.closed)
	}
	return nil
}

func solidImage(y byte) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for py := 0; py < 4; py++ {
		for px := 0; px < 4; px++ {
			img.Set(px, py, color.Gray{Y: y})
		}
	}
	return img
}

func nextEventually(t *testing.T, source Source) (*video.Frame, error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		frame := &video.Frame{}
		err := source.Next(frame)
		if !errors.Is(err, video.ErrNoFrame) {
			return frame, err
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("source never produced a result")
	return nil, nil
}

func TestLatestFrameSourceIsNonBlockingAndKeepsNewest(t *testing.T) {
	reader := newFakeReader()
	source := newLatestFrameSource(reader)
	released := make(chan int, 3)
	for i, shade := range []byte{30, 90, 180} {
		i := i
		reader.results <- readResult{image: solidImage(shade), release: func() { released <- i }}
	}
	for i := 0; i < 3; i++ {
		select {
		case <-released:
		case <-time.After(2 * time.Second):
			t.Fatal("reader did not release an input frame")
		}
	}

	frame, err := nextEventually(t, source)
	if err != nil {
		t.Fatal(err)
	}
	if frame.W != 640 || frame.H != 360 || len(frame.Y) != 640*360 {
		t.Fatalf("frame = %dx%d with %d luma bytes", frame.W, frame.H, len(frame.Y))
	}
	if frame.Y[0] < 160 {
		t.Fatalf("newest frame luma = %d, want the brightest third frame", frame.Y[0])
	}
	if err := source.Next(&video.Frame{}); !errors.Is(err, video.ErrNoFrame) {
		t.Fatalf("unchanged Next = %v, want ErrNoFrame", err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if got := reader.closes.Load(); got != 1 {
		t.Fatalf("reader closed %d times, want once", got)
	}
}

func TestReaderFailureIsReportedOnce(t *testing.T) {
	reader := newFakeReader()
	source := newLatestFrameSource(reader)
	boom := errors.New("camera disconnected")
	reader.results <- readResult{err: boom}
	_, err := nextEventually(t, source)
	if !errors.Is(err, boom) {
		t.Fatalf("Next error = %v, want %v", err, boom)
	}
	if err := source.Next(&video.Frame{}); !errors.Is(err, video.ErrNoFrame) {
		t.Fatalf("repeated failure = %v, want ErrNoFrame", err)
	}
	_ = source.Close()
	_ = source.Close()
	if got := reader.closes.Load(); got != 1 {
		t.Fatalf("reader closed %d times, want once", got)
	}
}
