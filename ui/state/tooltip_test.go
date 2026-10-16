package state

import (
	"testing"
	"time"
)

func TestHoverStep(t *testing.T) {
	var h Hover
	t0 := time.Unix(100, 0)
	if h.Step(3, t0) {
		t.Fatal("shown on first sight")
	}
	if h.Step(3, t0.Add(399*time.Millisecond)) {
		t.Fatal("shown before the delay")
	}
	if !h.Step(3, t0.Add(TooltipDelay)) {
		t.Fatal("not shown at the delay")
	}
	if !h.Step(3, t0.Add(5*time.Second)) {
		t.Fatal("dropped while held")
	}
	t1 := t0.Add(6 * time.Second)
	if h.Step(4, t1) {
		t.Fatal("new id shown at once")
	}
	if h.Step(4, t1.Add(399*time.Millisecond)) {
		t.Fatal("timing did not restart")
	}
	if !h.Step(4, t1.Add(TooltipDelay)) {
		t.Fatal("new id not shown after the delay")
	}
	h.Step(-1, t1.Add(time.Second))
	if h.Step(4, t1.Add(2*time.Second)) {
		t.Fatal("reset did not restart timing")
	}
}

func TestTooltipOrigin(t *testing.T) {
	x, y := TooltipOrigin(56, 14, 70, 100, 20, 960, 600)
	if x != 62 || y != 32 {
		t.Fatalf("origin = (%v,%v), want (62,32)", x, y)
	}
	if _, y := TooltipOrigin(56, 560, 616, 100, 40, 960, 600); y+40 > 600 {
		t.Errorf("not clamped at the bottom: y=%v", y)
	}
	if _, y := TooltipOrigin(56, -30, 26, 100, 40, 960, 600); y < 0 {
		t.Errorf("not clamped at the top: y=%v", y)
	}
	if x, _ := TooltipOrigin(56, 14, 70, 100, 20, 120, 600); x+100 > 120 {
		t.Errorf("not clamped at the right: x=%v", x)
	}
	if x, y := TooltipOrigin(56, 14, 70, 100, 20, 50, 10); x != 0 || y != 0 {
		t.Errorf("tiny window = (%v,%v), want (0,0)", x, y)
	}
}

func TestEllipsize(t *testing.T) {
	for _, c := range []struct {
		in   string
		cols int
		want string
	}{{"abc", 5, "abc"}, {"abcdef", 4, "abc" + Ellipsis}, {"é", 1, "é"}, {"abc", 0, ""}, {"abc", -2, ""}} {
		if got := Ellipsize(c.in, c.cols); got != c.want {
			t.Errorf("Ellipsize(%q,%d) = %q, want %q", c.in, c.cols, got, c.want)
		}
	}
}
