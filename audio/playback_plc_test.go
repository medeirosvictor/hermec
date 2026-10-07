package audio

import (
	"sync"
	"testing"
)

// Exercises the real Opus PLC path through the mixer: after the queue
// drains, the next plcBudget slots carry concealment audio, then silence.
func TestMixerPLCThroughOpus(t *testing.T) {
	m := newMixer()
	frames := encodeFrames(t, 8)
	dst := make([]int16, frameSize)
	for _, f := range frames[:6] {
		m.Write("a", f)
	}
	// Burst trimmed to targetDepth(3): three real slots...
	for i := 0; i < targetDepth; i++ {
		m.Pull(dst)
		if RMS(dst) < 0.01 {
			t.Fatalf("real slot %d silent", i)
		}
	}
	// ...then PLC for the budget, audible...
	plcRMS := 0.0
	for i := 0; i < plcBudget; i++ {
		m.Pull(dst)
		plcRMS = max(plcRMS, RMS(dst))
		if i == 0 && RMS(dst) < 0.01 {
			t.Fatal("first PLC slot silent: concealment not exercised")
		}
	}
	// ...then silence.
	m.Pull(dst)
	if RMS(dst) != 0 {
		t.Fatalf("expected silence after PLC budget, rms=%v", RMS(dst))
	}
	// Real audio resumes cleanly after a refill.
	m.Write("a", frames[6])
	m.Write("a", frames[7])
	m.Pull(dst)
	if RMS(dst) < 0.01 {
		t.Fatal("did not resume")
	}
}

// Write (network goroutine) and Pull (audio callback) run concurrently;
// run with -race.
func TestMixerConcurrentWritePull(t *testing.T) {
	m := newMixer()
	frames := encodeFrames(t, 20)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		dst := make([]int16, frameSize)
		for {
			select {
			case <-stop:
				return
			default:
				m.Pull(dst)
			}
		}
	}()
	for r := 0; r < 20; r++ {
		for _, f := range frames {
			m.Write("a", f)
			m.Write("b", f)
			m.level("a")
		}
		m.Write("a", nil)
		m.Remove("b")
	}
	close(stop)
	wg.Wait()
}
