package state

import "testing"

func TestHitsAtInside(t *testing.T) {
	var h Hits
	h.Add(Hit{X0: 10, Y0: 20, X1: 30, Y1: 40, Kind: HitChannel, ID: 3, Key: "k"})
	got, ok := h.At(15, 25)
	if !ok || got.Kind != HitChannel || got.ID != 3 || got.Key != "k" {
		t.Fatalf("At = %+v, %v", got, ok)
	}
}

func TestHitsAtOutside(t *testing.T) {
	var h Hits
	h.Add(Hit{X0: 10, Y0: 20, X1: 30, Y1: 40, Kind: HitGear})
	for _, p := range [][2]float64{{9, 25}, {15, 19}, {31, 25}, {15, 41}} {
		if _, ok := h.At(p[0], p[1]); ok {
			t.Fatalf("hit at %v", p)
		}
	}
}

func TestHitsLaterWins(t *testing.T) {
	var h Hits
	h.Add(Hit{X0: 0, Y0: 0, X1: 100, Y1: 100, Kind: HitChannel})
	h.Add(Hit{X0: 10, Y0: 10, X1: 20, Y1: 20, Kind: HitBanner})
	if got, _ := h.At(15, 15); got.Kind != HitBanner {
		t.Fatalf("overlay should win, got %v", got.Kind)
	}
	if got, _ := h.At(50, 50); got.Kind != HitChannel {
		t.Fatalf("outside overlay should fall to base, got %v", got.Kind)
	}
}

func TestHitsResetClears(t *testing.T) {
	var h Hits
	h.Add(Hit{X0: 0, Y0: 0, X1: 10, Y1: 10})
	h.Reset()
	if _, ok := h.At(5, 5); ok {
		t.Fatal("hit after Reset")
	}
}

func TestHitsEdgesHalfOpen(t *testing.T) {
	var h Hits
	h.Add(Hit{X0: 10, Y0: 20, X1: 30, Y1: 40})
	if _, ok := h.At(10, 20); !ok {
		t.Fatal("top-left corner should be inside")
	}
	if _, ok := h.At(30, 30); ok {
		t.Fatal("x1 should be exclusive")
	}
	if _, ok := h.At(20, 40); ok {
		t.Fatal("y1 should be exclusive")
	}
}
