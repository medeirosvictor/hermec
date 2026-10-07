package audio

import (
	"io"
	"math"
	"math/rand"
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

func TestDropCount(t *testing.T) {
	now := time.Unix(1000, 0)
	ms := func(d int) time.Time { return now.Add(time.Duration(d) * time.Millisecond) }
	q := func(ds ...int) []time.Time {
		var out []time.Time
		for _, d := range ds {
			out = append(out, ms(d))
		}
		return out
	}
	cases := []struct {
		name string
		last time.Time
		q    []time.Time
		want int
	}{
		{"continuous, 2 queued", ms(-20), q(-30, -10), 0},
		{"continuous, ring full", ms(-30), q(-70, -50, -30, -10), 0},
		{"gap exactly pauseGap", ms(-100), q(-70, -50, -30, -10), 0},
		{"after pause, backlog", ms(-101), q(-70, -50, -30, -10), 3},
		{"after 5s mute", ms(-5000), q(-70, -50, -30, -10), 3},
		{"after pause, single", ms(-5000), q(-10), 0},
		{"first read", time.Time{}, q(-50, -30, -10), 2},
		{"empty", ms(-5000), nil, 0},
	}
	for _, c := range cases {
		if got := dropCount(now, c.last, c.q); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func newTestCapture(t *testing.T, clock *time.Time) *capture {
	t.Helper()
	enc, err := opus.NewEncoder(sampleRate, 1, opus.AppVoIP)
	if err != nil {
		t.Fatal(err)
	}
	return &capture{
		enc: enc, meter: &Meter{}, frames: make(chan pcmFrame, captureRing),
		done: make(chan struct{}), obuf: make([]byte, 1500), clock: func() time.Time { return *clock },
	}
}

func seqFrame(seq int, at time.Time) pcmFrame {
	var f pcmFrame
	f.pcm[0] = int16(seq)
	f.at = at
	return f
}

// Review Focus 1: a simulated minute at 20ms cadence with +-10ms jitter on
// both producer and consumer must lose nothing and keep order.
func TestCaptureSteadyJitterNoDiscards(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newTestCapture(t, &now)
	rng := rand.New(rand.NewSource(7))
	jit := func() time.Duration { return time.Duration(rng.Intn(21)-10) * time.Millisecond }
	const total = 3000 // 60s of frames
	step := frameMs * time.Millisecond
	start := now
	prodAt := func(k int) time.Time { return start.Add(time.Duration(k)*step + jit()) }
	nextProd, nextProdAt := 0, prodAt(0)
	produce := func() {
		c.push(seqFrame(nextProd, nextProdAt))
		nextProd++
		nextProdAt = prodAt(nextProd)
	}
	read := 0
	for k := 0; read < total; k++ {
		readAt := start.Add(time.Duration(k)*step + jit() + 5*time.Millisecond)
		for nextProd < total && !nextProdAt.After(readAt) {
			produce()
		}
		if len(c.frames)+len(c.pending) == 0 { // a blocking read wakes when the next frame lands
			if nextProd >= total {
				break
			}
			readAt = nextProdAt
			produce()
		}
		now = readAt
		f, err := c.nextFrame()
		if err != nil {
			t.Fatal(err)
		}
		if int(f.pcm[0]) != read {
			t.Fatalf("read %d: got frame %d (discard or reorder)", read, f.pcm[0])
		}
		read++
	}
	if read != total {
		t.Fatalf("read %d of %d frames", read, total)
	}
}

// Review Focus 2: after a long mute (reads stop, ring churns) resuming
// returns at most one stale frame before fresh audio.
func TestCaptureResumeAfterMuteNoBurst(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newTestCapture(t, &now)
	step := frameMs * time.Millisecond
	for i := 0; i < 5; i++ {
		c.push(seqFrame(i, now))
		now = now.Add(step)
		if f, err := c.nextFrame(); err != nil || int(f.pcm[0]) != i {
			t.Fatalf("warmup %d: got %d %v", i, f.pcm[0], err)
		}
	}
	last := 0
	for i := 5; i < 5+250; i++ { // 5s muted, producer keeps running
		c.push(seqFrame(i, now))
		now = now.Add(step)
		last = i
	}
	f, err := c.nextFrame()
	if err != nil {
		t.Fatal(err)
	}
	if stale := last - int(f.pcm[0]); stale > 1 {
		t.Fatalf("first frame after unmute is %d frames stale", stale)
	}
	if len(c.frames) != 0 {
		t.Fatal("backlog not drained after pause")
	}
	for i := last + 1; i < last+10; i++ {
		c.push(seqFrame(i, now))
		now = now.Add(step)
		f, err := c.nextFrame()
		if err != nil || int(f.pcm[0]) != i {
			t.Fatalf("after resume: want %d got %d (%v)", i, f.pcm[0], err)
		}
	}
}

func TestCaptureEOF(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newTestCapture(t, &now)
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

func TestCaptureEOFWithPending(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newTestCapture(t, &now)
	c.pending = append(c.pending, seqFrame(1, now), seqFrame(2, now))
	close(c.done)
	if _, err := c.nextFrame(); err != io.EOF {
		t.Fatalf("got %v, want io.EOF", err)
	}
}

// A consumer slower than the producer that never pauses must not grow
// latency: pending stays within captureRing and the newest audio wins.
func TestCaptureSlowConsumerBounded(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newTestCapture(t, &now)
	seq, prev := 0, -1
	for i := 0; i < 2000; i++ {
		// Consumer reads every 25ms; producer averages 1.25 frames per read
		// so it outpaces the consumer without any 100ms pause gap.
		c.push(seqFrame(seq, now))
		seq++
		if i%4 == 3 { // extra frame every 4th read: producer faster overall
			c.push(seqFrame(seq, now))
			seq++
		}
		now = now.Add(25 * time.Millisecond)
		f, err := c.nextFrame()
		if err != nil {
			t.Fatal(err)
		}
		if len(c.pending) > captureRing {
			t.Fatalf("pending %d exceeds captureRing", len(c.pending))
		}
		if int(f.pcm[0]) <= prev {
			t.Fatalf("out of order: %d after %d", f.pcm[0], prev)
		}
		prev = int(f.pcm[0])
		if lag := seq - 1 - prev; lag > captureRing {
			t.Fatalf("read %d is %d frames behind newest", i, lag)
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
