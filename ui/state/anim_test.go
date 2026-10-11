package state

import (
	"math"
	"testing"
)

func TestApproachConverges(t *testing.T) {
	const tau, dt = 0.15, 1.0 / 60
	cur := 0.0
	for i := 0; i < 45; i++ { // 45 frames = 0.75s = 5 tau
		cur = Approach(cur, 1, dt, tau)
	}
	if math.Abs(cur-1) > 0.01 {
		t.Fatalf("after 5 tau cur = %v, want within 1%% of 1", cur)
	}
}

func TestApproachNoOvershoot(t *testing.T) {
	for _, dt := range []float64{1.0 / 240, 1.0 / 60, 1.0 / 20, 1, 100} {
		up, down := 0.0, 1.0
		for i := 0; i < 200; i++ {
			up = Approach(up, 1, dt, 0.15)
			down = Approach(down, 0, dt, 0.15)
			if up > 1 || down < 0 {
				t.Fatalf("dt=%v overshoot: up=%v down=%v", dt, up, down)
			}
		}
	}
}

func TestEnvelopeAttackFasterThanRelease(t *testing.T) {
	const dt = 0.001
	e := Envelope{Attack: 0.08, Release: 0.4}
	var tr float64
	for i := 0; ; i++ {
		if e.Step(1, dt) >= 1-1/math.E {
			tr = float64(i+1) * dt
			break
		}
	}
	if math.Abs(tr-0.08) > 0.008 {
		t.Errorf("rise to 63%% took %v, want ~0.08", tr)
	}
	e = Envelope{Attack: 0.08, Release: 0.4}
	for i := 0; i < 10000; i++ {
		e.Step(1, dt)
	}
	var tf float64
	for i := 0; ; i++ {
		if e.Step(0, dt) <= 1/math.E {
			tf = float64(i+1) * dt
			break
		}
	}
	if math.Abs(tf-0.4) > 0.04 {
		t.Errorf("fall to 37%% took %v, want ~0.4", tf)
	}
	if tr >= tf {
		t.Errorf("attack %v not faster than release %v", tr, tf)
	}
}

func TestEnvelopeZeroDtNoChange(t *testing.T) {
	e := Envelope{Attack: 0.08, Release: 0.4}
	e.Step(1, 0.05)
	before := e.Value()
	if got := e.Step(0, 0); got != before || e.Value() != before {
		t.Fatalf("zero dt changed value: %v -> %v", before, got)
	}
	if got := e.Step(1, 0); got != before {
		t.Fatalf("zero dt changed value rising: %v -> %v", before, got)
	}
}
