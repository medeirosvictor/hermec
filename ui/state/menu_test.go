package state

import "testing"

func TestMenuOrigin(t *testing.T) {
	cases := []struct {
		name                     string
		ax, ay, w, h, winW, winH float64
		wantX, wantY             float64
	}{
		{"fits", 10, 20, 100, 50, 800, 600, 10, 20},
		{"overflows right", 750, 20, 100, 50, 800, 600, 700, 20},
		{"overflows bottom", 10, 580, 100, 50, 800, 600, 10, 550},
		{"overflows both", 790, 590, 100, 50, 800, 600, 700, 550},
		{"window smaller than menu", 30, 30, 100, 50, 80, 40, 0, 0},
	}
	for _, c := range cases {
		x, y := MenuOrigin(c.ax, c.ay, c.w, c.h, c.winW, c.winH)
		if x != c.wantX || y != c.wantY {
			t.Errorf("%s: got (%v,%v), want (%v,%v)", c.name, x, y, c.wantX, c.wantY)
		}
	}
}
