package audio

import (
	"io"
	"testing"
	"time"
)

// The buzzsaw regression: 20ms frames in, 20ms pulls out, with the network
// delivering a frame late. No pull after start may be silent; the one empty
// slot is concealed and the stream carries on without a re-prime gap.
func TestMixerSteadyFlowNoSilenceAfterStart(t *testing.T) {
	m := newMixer()
	clock := time.Unix(1000, 0)
	m.now = func() time.Time { return clock }
	frames := encodeFrames(t, 40)
	dst := make([]int16, frameSize)

	silent, started := 0, false
	for i, f := range frames {
		// Frame 20 is delivered one tick late, together with frame 21.
		switch i {
		case 20:
			continue
		case 21:
			m.Write("a", frames[20])
		}
		m.Write("a", f)
		m.Pull(dst)
		clock = clock.Add(20 * time.Millisecond)
		if RMS(dst) > 0.01 {
			started = true
		} else if started {
			silent++
		}
	}
	if !started {
		t.Fatal("never became audible")
	}
	if silent != 0 {
		t.Fatalf("%d silent pulls after start (buzz)", silent)
	}
}

// A 500ms outage: at most plcBudget concealment frames then silence; on
// resume the burst is trimmed and audio is clean.
func TestMixerOutageThenResume(t *testing.T) {
	m := newMixer()
	clock := time.Unix(1000, 0)
	m.now = func() time.Time { return clock }
	frames := encodeFrames(t, 60)
	dst := make([]int16, frameSize)
	for i := 0; i < 10; i++ {
		m.Write("a", frames[i])
		m.Pull(dst)
		clock = clock.Add(20 * time.Millisecond)
	}
	for i := 0; i < 25; i++ { // outage
		m.Pull(dst)
		clock = clock.Add(20 * time.Millisecond)
	}
	if RMS(dst) != 0 {
		t.Fatal("outage should end in silence")
	}
	for i := 10; i < 35; i++ { // backlog arrives at once
		m.Write("a", frames[i])
	}
	if d := m.senders["a"].js.depth; d > targetDepth {
		t.Fatalf("depth %d after burst, want <= %d", d, targetDepth)
	}
	for i := 35; i < 55; i++ {
		m.Write("a", frames[i])
		m.Pull(dst)
		if RMS(dst) < 0.01 {
			t.Fatalf("silence at resume tick %d", i)
		}
	}
}

func TestMixerMultiSenderMixes(t *testing.T) {
	frames := encodeFrames(t, 4)
	one := func(fps ...string) float64 {
		mm := newMixer()
		for _, fp := range fps {
			mm.Write(fp, frames[0])
			mm.Write(fp, frames[1])
			mm.Write(fp, frames[2])
		}
		d := make([]int16, frameSize)
		mm.Pull(d)
		return RMS(d)
	}
	if a, b := one("a"), one("a", "b"); !(b > a*1.5) {
		t.Fatalf("two senders should sum louder: %v vs %v", a, b)
	}
}

// Cautious 2s loopback: mic -> Opus -> playback on the default devices.
// Skipped without both devices. It checks plumbing and lifecycle, not
// audibility (the speaker may be muted, the mic quiet).
func TestLoopbackSelfTest(t *testing.T) {
	src, mic, err := Capture()
	if err != nil {
		t.Skipf("no capture device: %v", err)
	}
	defer src.(io.Closer).Close()
	sink, spk, err := Playback("")
	if err != nil {
		t.Skipf("no playback device: %v", err)
	}
	defer sink.(io.Closer).Close()

	done := make(chan int)
	go func() {
		n := 0
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			f, err := src.ReadOpusFrame()
			if err != nil {
				break
			}
			sink.WriteOpusFrame("loop", f)
			n++
		}
		done <- n
	}()
	peak := 0.0
	for {
		select {
		case n := <-done:
			t.Logf("looped %d frames in 2s, mic peak/last %.4f, speaker peak %.4f", n, mic.Level(), peak)
			if n < 40 {
				t.Fatalf("only %d frames looped", n)
			}
			return
		case <-time.After(50 * time.Millisecond):
			peak = max(peak, spk.Level("loop"))
		}
	}
}
