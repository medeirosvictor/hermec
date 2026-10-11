package audio

import (
	"testing"
	"time"
)

// streamWithGap writes n steady frames (pulling every tick), then goes quiet
// for gap (pulling every tick), then writes one more frame. It returns the
// total played and concealed slots in the sender's health window.
func streamWithGap(t *testing.T, n int, gap time.Duration) (played, concealed int) {
	t.Helper()
	m := newMixer()
	clock := t0
	m.now = func() time.Time { return clock }
	f := encodeFrames(t, 1)[0]
	dst := make([]int16, frameSize)
	for i := 0; i < n; i++ {
		m.Write("a", f)
		clock = clock.Add(frameGap)
		m.Pull(dst)
	}
	for el := time.Duration(0); el < gap-frameGap; el += frameGap {
		m.Pull(dst)
		clock = clock.Add(frameGap)
	}
	m.Write("a", f)
	for _, b := range m.senders["a"].hw.b {
		played += b.played
		concealed += b.concealed
	}
	return
}

func TestGapCountsMissingFramesBeyondPLC(t *testing.T) {
	_, baseConc := streamWithGap(t, 30, frameGap)
	if baseConc != 0 {
		t.Fatalf("steady flow concealed %d", baseConc)
	}
	played, concealed := streamWithGap(t, 30, 500*time.Millisecond)
	// 500ms gap = 25 slots; 1 is the late frame itself, 3 were PLC'd by
	// fill, and the remaining 21 are added on arrival: 3 + 21 = 24.
	if concealed != 24 {
		t.Fatalf("concealed %d, want 24 (played %d)", concealed, played)
	}
}

func TestGapOverJitterResetNotCounted(t *testing.T) {
	_, concealed := streamWithGap(t, 30, 1500*time.Millisecond)
	if concealed > plcBudget {
		t.Fatalf("1.5s gap concealed %d, want <= %d", concealed, plcBudget)
	}
}
