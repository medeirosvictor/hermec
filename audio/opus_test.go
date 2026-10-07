package audio

import (
	"io"
	"math"
	"testing"
	"time"

	"github.com/hraban/opus"
)

func encodeFrames(t *testing.T, k int) [][]byte {
	t.Helper()
	enc, err := opus.NewEncoder(sampleRate, 1, opus.AppVoIP)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, frameSize*k)
	for i := range pcm {
		pcm[i] = int16(12000 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
	}
	var out [][]byte
	for i := 0; i < k; i++ {
		buf := make([]byte, 1500)
		n, err := enc.Encode(pcm[i*frameSize:(i+1)*frameSize], buf)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, buf[:n])
	}
	return out
}

func TestMixerDecodePLCAndCleanup(t *testing.T) {
	m := newMixer()
	clock := time.Unix(1000, 0)
	m.now = func() time.Time { return clock }
	frames := encodeFrames(t, 6)

	dst := make([]int16, frameSize)
	m.Pull(dst)
	if RMS(dst) != 0 {
		t.Fatal("expected silence with no senders")
	}

	m.Write("a", frames[0])
	m.Pull(dst) // below prefill: still buffering
	if RMS(dst) != 0 {
		t.Fatal("sender should be buffering")
	}
	m.Write("a", frames[1])
	m.Write("a", frames[2])
	m.Write("a", nil)                      // lost frame: PLC
	m.Write("a", []byte{0xff, 0xff, 0xff}) // garbage: concealed too
	m.Write("a", frames[5])
	m.Pull(dst)
	if RMS(dst) < 0.05 {
		t.Fatalf("expected audible audio, rms=%v", RMS(dst))
	}
	if m.level("a") < 0.05 {
		t.Fatalf("meter should show speaker a, got %v", m.level("a"))
	}

	m.Write("b", frames[0])
	m.Write("b", frames[1])
	m.Write("b", frames[2])
	m.Pull(dst)
	if len(m.senders) != 2 {
		t.Fatalf("senders = %d", len(m.senders))
	}

	for i := 0; i < 20; i++ {
		m.Pull(dst) // drain
	}
	clock = clock.Add(senderIdle + time.Second)
	m.Pull(dst)
	if len(m.senders) != 0 {
		t.Fatalf("idle senders not cleaned: %d", len(m.senders))
	}
	if m.level("a") != 0 {
		t.Fatal("departed sender should read 0")
	}
	m.Write("a", frames[0])
	m.Remove("a")
	if len(m.senders) != 0 {
		t.Fatal("Remove failed")
	}
}

func TestMixerStrayFrameCleanedUp(t *testing.T) {
	m := newMixer()
	clock := time.Unix(1000, 0)
	m.now = func() time.Time { return clock }
	m.Write("a", encodeFrames(t, 1)[0]) // below prefill, never primed
	clock = clock.Add(senderIdle + time.Second)
	m.Pull(make([]int16, frameSize))
	if len(m.senders) != 0 {
		t.Fatal("unprimed idle sender with stray frame not collected")
	}
}

func TestCaptureStalenessAndEOF(t *testing.T) {
	now := time.Unix(1000, 0)
	if stale(now, now.Add(frameMs*time.Millisecond)) {
		t.Fatal("one frame old should be fresh")
	}
	if !stale(now, now.Add(2*frameMs*time.Millisecond)) {
		t.Fatal("two frames old should be stale")
	}

	enc, err := opus.NewEncoder(sampleRate, 1, opus.AppVoIP)
	if err != nil {
		t.Fatal(err)
	}
	c := &capture{
		enc: enc, meter: &Meter{}, frames: make(chan pcmFrame, captureRing),
		done: make(chan struct{}), obuf: make([]byte, 1500), clock: func() time.Time { return now },
	}
	// A backlog after a pause yields exactly one (the newest) frame.
	c.push(pcmFrame{at: now.Add(-time.Hour)})
	c.push(pcmFrame{at: now})
	if f, err := c.ReadOpusFrame(); err != nil || len(f) == 0 {
		t.Fatalf("read: %v %d", err, len(f))
	}
	if len(c.frames) != 0 {
		t.Fatal("backlog not drained")
	}
	// After close, buffered frames must never be served.
	for i := 0; i < 50; i++ {
		c.push(pcmFrame{at: now})
		close2 := i == 0
		if close2 {
			close(c.done)
		}
		if _, err := c.ReadOpusFrame(); err != io.EOF {
			t.Fatalf("iteration %d: got %v, want io.EOF", i, err)
		}
	}
}

// Device test: skipped when no microphone can be opened.
func TestCaptureDevice(t *testing.T) {
	src, meter, err := Capture()
	if err != nil {
		t.Skipf("no capture device: %v", err)
	}
	defer src.(io.Closer).Close()
	got := 0
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f, err := src.ReadOpusFrame()
		if err != nil {
			t.Fatal(err)
		}
		if len(f) == 0 {
			t.Fatal("empty frame")
		}
		got++
	}
	t.Logf("read %d frames in ~1s, mic level %.4f", got, meter.Level())
	if got < 20 {
		t.Fatalf("only %d frames in 1s", got)
	}
	src.(io.Closer).Close()
	if _, err := src.ReadOpusFrame(); err != io.EOF {
		t.Fatalf("after Close got %v, want io.EOF", err)
	}
}
