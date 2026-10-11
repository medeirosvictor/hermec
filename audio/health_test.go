package audio

import (
	"math"
	"testing"
	"time"
)

var t0 = time.Unix(1_700_000_000, 0)

func TestHealthWindowRatio(t *testing.T) {
	var w healthWindow
	w.add(t0, 90, 0)
	w.add(t0.Add(time.Second), 0, 10)
	got, ok := w.ratio(t0.Add(time.Second))
	if !ok || math.Abs(got-0.10) > 1e-9 {
		t.Fatalf("got %v ok=%v want 0.10", got, ok)
	}
}

func TestHealthWindowRollsOff(t *testing.T) {
	var w healthWindow
	w.add(t0, 90, 10)
	if _, ok := w.ratio(t0.Add(4 * time.Second)); !ok {
		t.Fatal("data should still be inside the 5s window")
	}
	if _, ok := w.ratio(t0.Add(6 * time.Second)); ok {
		t.Fatal("OK should be false after 6 empty seconds")
	}
}

func TestJitterSteady20msIsZero(t *testing.T) {
	var e jitterEst
	for i := 0; i < 100; i++ {
		e.update(20 * time.Millisecond)
	}
	if e.j != 0 {
		t.Fatalf("j=%v", e.j)
	}
}

func TestJitterAlternating10_30(t *testing.T) {
	var e jitterEst
	for i := 0; i < 400; i++ {
		e.update(time.Duration(10+20*(i%2)) * time.Millisecond)
	}
	if d := time.Duration(e.j); d < 9*time.Millisecond || d > 11*time.Millisecond {
		t.Fatalf("jitter %v, want ~10ms", d)
	}
}

func TestMixerJitterViaWrite(t *testing.T) {
	m := newMixer()
	clock := t0
	m.now = func() time.Time { return clock }
	frames := encodeFrames(t, 1)
	for i := 0; i < 200; i++ {
		clock = clock.Add(time.Duration(10+20*(i%2)) * time.Millisecond)
		m.Write("a", frames[0])
	}
	h := (&Meters{m: m}).Health("a")
	if h.Jitter < 8*time.Millisecond || h.Jitter > 11*time.Millisecond {
		t.Fatalf("jitter %v", h.Jitter)
	}
}

func TestMixerQuietSenderNotConcealing(t *testing.T) {
	m := newMixer()
	clock := t0
	m.now = func() time.Time { return clock }
	ms := &Meters{m: m}
	frames := encodeFrames(t, 5)
	dst := make([]int16, frameSize)
	for _, f := range frames {
		m.Write("a", f)
		clock = clock.Add(20 * time.Millisecond)
		m.Pull(dst)
	}
	// Sender stops. Pull for 2s: only the PLC budget may count as concealed.
	for i := 0; i < 100; i++ {
		clock = clock.Add(20 * time.Millisecond)
		m.Pull(dst)
	}
	if h := ms.Health("a"); !h.OK {
		t.Fatal("expected data")
	}
	concealed := 0
	for _, b := range m.senders["a"].hw.b {
		concealed += b.concealed
	}
	if concealed > plcBudget {
		t.Fatalf("concealed %d slots, budget is %d", concealed, plcBudget)
	}
	// Swept after senderIdle.
	clock = clock.Add(senderIdle + time.Second)
	m.Pull(dst)
	if h := ms.Health("a"); h.OK {
		t.Fatalf("swept sender still OK: %+v", h)
	}
}

func TestMetersSendersDropsDeparted(t *testing.T) {
	m := newMixer()
	clock := t0
	m.now = func() time.Time { return clock }
	ms := &Meters{m: m}
	f := encodeFrames(t, 1)[0]
	m.Write("b", f)
	m.Write("a", f)
	if got := ms.Senders(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
	clock = clock.Add(senderIdle + time.Second)
	m.Pull(make([]int16, frameSize))
	if got := ms.Senders(); len(got) != 0 {
		t.Fatalf("departed senders listed: %v", got)
	}
}

func TestPullNoAllocWithHealth(t *testing.T) {
	m := newMixer()
	frames := encodeFrames(t, 5)
	dst := make([]int16, frameSize)
	for _, f := range frames {
		m.Write("a", f)
	}
	m.Pull(dst) // warm: grows s.out
	// The runs play queued frames, then PLC, then silence: every branch.
	if a := testing.AllocsPerRun(20, func() { m.Pull(dst) }); a != 0 {
		t.Fatalf("Pull allocates %v", a)
	}
}
