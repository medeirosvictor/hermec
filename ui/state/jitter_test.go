package state

import (
	"testing"
	"time"
)

func TestBackoffJitterBounds(t *testing.T) {
	for attempt := 0; attempt < 8; attempt++ {
		base := NextBackoff(attempt)
		lo, hi := time.Duration(float64(base)*0.75), time.Duration(float64(base)*1.25)
		for _, r := range []float64{0, 0.25, 0.5, 0.999999, 1} {
			got := JitteredBackoff(attempt, func() float64 { return r })
			if got < lo || got > hi {
				t.Fatalf("attempt %d r=%v: %v outside [%v,%v]", attempt, r, got, lo, hi)
			}
		}
		if got := JitteredBackoff(attempt, func() float64 { return 0.5 }); got != base {
			t.Fatalf("midpoint = %v, want %v", got, base)
		}
	}
	if d := JitteredBackoff(0, nil); d < 750*time.Millisecond || d > 1250*time.Millisecond {
		t.Fatalf("default rand: %v", d)
	}
}
