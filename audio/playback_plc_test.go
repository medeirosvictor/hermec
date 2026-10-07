package audio

import (
	"sync"
	"testing"
	"time"
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
	for i := 0; i < plcBudget; i++ {
		m.Pull(dst)
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

// N1 regression: a new sender, or one idle for longer than senderIdle, must
// survive a Pull that lands between lookup and the first push of Write.
func TestMixerNewSenderNotSweptMidWrite(t *testing.T) {
	m := newMixer()
	clock := time.Unix(1000, 0)
	m.now = func() time.Time { return clock }
	dst := make([]int16, frameSize)

	// Old sender, then Write begins: lookup + touch, Pull in the window.
	m.Write("old", encodeFrames(t, 1)[0])
	clock = clock.Add(senderIdle + time.Second)
	s := m.lookup("old")
	s.touch(m.now())
	m.Pull(dst)
	if m.senders["old"] != s {
		t.Fatal("sender swept between lookup and first push")
	}

	// And a fresh lookup at the current time is never idle at creation.
	s = m.lookup("fresh")
	m.Pull(dst)
	if m.senders["fresh"] != s {
		t.Fatal("fresh sender swept before its first frame")
	}
}

// A PLC slot that cannot take the decoder lock (a real decode is in
// flight) is silent, not blocked, and PLC works again afterwards.
func TestMixerPLCTryLockMiss(t *testing.T) {
	m := newMixer()
	frames := encodeFrames(t, 4)
	dst := make([]int16, frameSize)
	m.Write("a", frames[0])
	m.Write("a", frames[1])
	m.Pull(dst)
	m.Pull(dst) // queue drained
	s := m.senders["a"]
	s.decMu.Lock()
	m.Pull(dst) // wants PLC, decoder busy
	busy := RMS(dst)
	s.decMu.Unlock()
	if busy != 0 {
		t.Fatalf("slot should be silent while decoder busy, rms=%v", busy)
	}
	m.Pull(dst)
	if RMS(dst) < 0.01 {
		t.Fatal("PLC did not resume after the lock was free")
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
