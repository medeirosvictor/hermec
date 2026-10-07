package audio

import (
	"math"
	"testing"
	"time"
)

func TestRMS(t *testing.T) {
	if RMS(nil) != 0 {
		t.Fatal("empty should be 0")
	}
	if RMS(make([]int16, 100)) != 0 {
		t.Fatal("silence should be 0")
	}
	sq := make([]int16, 100)
	for i := range sq {
		sq[i] = math.MinInt16
	}
	if got := RMS(sq); math.Abs(got-1) > 1e-9 {
		t.Fatalf("full-scale square = %v, want 1", got)
	}
	half := make([]int16, 100)
	for i := range half {
		half[i] = 16384
	}
	if got := RMS(half); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("half-scale = %v, want 0.5", got)
	}
}

func TestDecay(t *testing.T) {
	if got := Decay(0.8, 0); got != 0.8 {
		t.Fatalf("no elapsed time = %v", got)
	}
	if got := Decay(0.8, levelHalfLife); math.Abs(got-0.4) > 1e-9 {
		t.Fatalf("one half-life = %v, want 0.4", got)
	}
	if got := Decay(0.8, 10*time.Second); got > 1e-6 {
		t.Fatalf("long silence = %v, want ~0", got)
	}
}

func TestMeter(t *testing.T) {
	var m Meter
	t0 := time.Unix(1000, 0)
	if m.LevelAt(t0) != 0 {
		t.Fatal("fresh meter should read 0")
	}
	m.Observe(0.6, t0)
	if got := m.LevelAt(t0); got != 0.6 {
		t.Fatalf("instant attack = %v", got)
	}
	if got := m.LevelAt(t0.Add(levelHalfLife)); math.Abs(got-0.3) > 1e-9 {
		t.Fatalf("decay = %v, want 0.3", got)
	}
	// A quieter reading must not pull the level below the decayed peak.
	m.Observe(0.1, t0.Add(levelHalfLife))
	if got := m.LevelAt(t0.Add(levelHalfLife)); math.Abs(got-0.3) > 1e-9 {
		t.Fatalf("quiet reading lowered level: %v", got)
	}
	var nilM *Meter
	if nilM.Level() != 0 {
		t.Fatal("nil meter should read 0")
	}
}
