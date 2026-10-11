package state

import "testing"

func TestTitleZoneButtons(t *testing.T) {
	const w, h, bar = 960.0, 600.0, 32.0
	cases := []struct {
		x    float64
		want Zone
	}{
		{959, ZoneClose}, {914, ZoneClose}, {913, ZoneMax}, {868, ZoneMax},
		{867, ZoneMin}, {822, ZoneMin}, {821, ZoneCaption},
	}
	for _, c := range cases {
		if got := TitleZone(c.x, 10, w, h, bar, 6, false); got != c.want {
			t.Errorf("x=%v: got %v, want %v", c.x, got, c.want)
		}
	}
	if got := TitleZone(930, 31, w, h, bar, 6, false); got != ZoneClose {
		t.Errorf("y=31 close: got %v", got)
	}
}

func TestTitleZoneCaption(t *testing.T) {
	if got := TitleZone(400, 10, 960, 600, 32, 6, false); got != ZoneCaption {
		t.Fatalf("got %v", got)
	}
	if got := TitleZone(400, 300, 960, 600, 32, 6, false); got != ZoneNone {
		t.Fatalf("scene area: got %v", got)
	}
}

func TestTitleZoneEdges(t *testing.T) {
	const w, h, bar = 960.0, 600.0, 32.0
	cases := []struct {
		x, y float64
		want Zone
	}{
		{2, 300, ZoneLeft}, {957, 300, ZoneRight}, {400, 2, ZoneTop}, {400, 597, ZoneBottom},
		{2, 2, ZoneTopLeft}, {957, 597, ZoneBottomRight}, {2, 597, ZoneBottomLeft},
		{957, 2, ZoneTopRight},
		{6, 300, ZoneNone}, {953, 300, ZoneNone}, {954, 300, ZoneRight}, {5, 300, ZoneLeft},
	}
	for _, c := range cases {
		if got := TitleZone(c.x, c.y, w, h, bar, 6, false); got != c.want {
			t.Errorf("(%v,%v): got %v, want %v", c.x, c.y, got, c.want)
		}
	}
}

func TestTitleZoneButtonBeatsTopEdge(t *testing.T) {
	if got := TitleZone(930, 2, 960, 600, 32, 6, false); got != ZoneClose {
		t.Fatalf("got %v", got)
	}
}

func TestTitleZoneMaximizedNoEdges(t *testing.T) {
	for _, p := range [][2]float64{{2, 300}, {957, 300}, {400, 597}, {2, 2}, {400, 2}} {
		z := TitleZone(p[0], p[1], 960, 600, 32, 6, true)
		if z >= ZoneLeft {
			t.Errorf("(%v,%v): edge zone %v while maximized", p[0], p[1], z)
		}
	}
	if got := TitleZone(400, 2, 960, 600, 32, 6, true); got != ZoneCaption {
		t.Fatalf("top strip maximized: got %v", got)
	}
}

// Physical-pixel hit testing at 150%: bar 48px, edge 9px, buttons 69px wide.
func TestTitleZonePhysical(t *testing.T) {
	const w, h, s = 1440.0, 900.0, 1.5
	cases := []struct {
		x, y float64
		want Zone
	}{
		{1439, 47, ZoneClose}, {1439, 48, ZoneRight}, {1371, 20, ZoneClose}, {1370, 20, ZoneMax},
		{700, 47, ZoneCaption}, {700, 48, ZoneNone}, {8, 400, ZoneLeft}, {9, 400, ZoneNone},
		{700, 8, ZoneTop}, {700, 9, ZoneCaption}, {5, 5, ZoneTopLeft},
	}
	for _, c := range cases {
		if got := TitleZonePhysical(c.x, c.y, w, h, s, false); got != c.want {
			t.Errorf("(%v,%v): got %v want %v", c.x, c.y, got, c.want)
		}
	}
}

func TestHTFor(t *testing.T) {
	want := map[Zone]uintptr{
		ZoneNone: 0, ZoneMin: 0, ZoneMax: 0, ZoneClose: 0, ZoneCaption: 2,
		ZoneLeft: 10, ZoneRight: 11, ZoneTop: 12, ZoneTopLeft: 13, ZoneTopRight: 14,
		ZoneBottom: 15, ZoneBottomLeft: 16, ZoneBottomRight: 17,
	}
	for z, ht := range want {
		if got := HTFor(z); got != ht {
			t.Errorf("HTFor(%v) = %d, want %d", z, got, ht)
		}
	}
}
