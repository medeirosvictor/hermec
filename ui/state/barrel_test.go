package state

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestBarrelCentreFixed(t *testing.T) {
	x, y := Barrel(480, 300, 960, 600, 0.015)
	if !near(x, 480) || !near(y, 300) {
		t.Fatalf("centre moved to %v,%v", x, y)
	}
}

func TestBarrelZeroIdentity(t *testing.T) {
	x, y := Barrel(123, 456, 960, 600, 0)
	if !near(x, 123) || !near(y, 456) {
		t.Fatalf("k=0 gave %v,%v", x, y)
	}
}

func TestBarrelCornerPullsOutward(t *testing.T) {
	x, y := Barrel(0, 0, 960, 600, 0.015)
	if !near(x, -14.4) || !near(y, -9) {
		t.Fatalf("corner = %v,%v want -14.4,-9", x, y)
	}
}

func TestBarrelResize(t *testing.T) {
	ax, ay := Barrel(0.25*960, 0.75*600, 960, 600, 0.015)
	bx, by := Barrel(0.25*1920, 0.75*1080, 1920, 1080, 0.015)
	if !near(ax/960, bx/1920) || !near(ay/600, by/1080) {
		t.Fatalf("relative positions differ: %v,%v vs %v,%v", ax/960, ay/600, bx/1920, by/1080)
	}
}
