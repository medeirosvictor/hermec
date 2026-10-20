package video

import (
	"runtime"
	"strings"
	"testing"
)

// vp8KeyHeader builds the start of a VP8 key frame claiming w x h: the 3-byte
// frame tag (key frame, show_frame, first partition size 10), the start code
// and the 14-bit dimensions, then padding.
func vp8KeyHeader(w, h int) []byte {
	b := []byte{0x50, 0x01, 0x00, 0x9d, 0x01, 0x2a, byte(w), byte(w >> 8), byte(h), byte(h >> 8)}
	return append(b, make([]byte, 64)...)
}

func TestDecoderRejectsOversizedKeyFrame(t *testing.T) {
	d, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f, err := d.Decode(vp8KeyHeader(16383, 16383))
	runtime.ReadMemStats(&after)
	if err == nil || f != nil {
		t.Fatalf("Decode = %v, %v; want an error", f, err)
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 64<<20 {
		t.Fatalf("rejecting the header allocated %d MiB", got>>20)
	}
}

func TestDecoderSurvivesCodecPanic(t *testing.T) {
	d, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.decode = func([]byte) error { panic("boom") }
	f, err := d.Decode([]byte{1, 2, 3})
	if err == nil || f != nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Decode = %v, %v; want the panic as an error", f, err)
	}
}
