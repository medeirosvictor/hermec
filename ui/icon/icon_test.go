package icon

import "testing"

func TestIconSizes(t *testing.T) {
	for _, s := range []int{16, 32, 48, 256} {
		b := Render(s).Bounds()
		if b.Dx() != s || b.Dy() != s {
			t.Errorf("Render(%d) bounds = %v", s, b)
		}
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func TestIconSymmetric(t *testing.T) {
	for _, s := range []int{32, 256} {
		im := Render(s)
		for y := 0; y < s; y++ {
			for x := 0; x < s/2; x++ {
				a := im.RGBAAt(x, y)
				b := im.RGBAAt(s-1-x, y)
				if abs(int(a.R)-int(b.R)) > 2 || abs(int(a.G)-int(b.G)) > 2 ||
					abs(int(a.B)-int(b.B)) > 2 || abs(int(a.A)-int(b.A)) > 2 {
					t.Fatalf("size %d: (%d,%d)=%v vs mirror %v", s, x, y, a, b)
				}
			}
		}
	}
}

func TestIconCornersTransparent(t *testing.T) {
	if a := Render(256).RGBAAt(0, 0).A; a != 0 {
		t.Errorf("corner alpha = %d, want 0", a)
	}
}

func TestIconHasWhiteAndDark(t *testing.T) {
	im := Render(256)
	white, dark := false, false
	for y := 0; y < 256; y++ {
		c := im.RGBAAt(128, y)
		if c.R > 240 && c.G > 240 && c.B > 240 && c.A > 240 {
			white = true
		}
		if c.R == 0x1E && c.G == 0x1F && c.B == 0x22 && c.A == 255 {
			dark = true
		}
	}
	if !white || !dark {
		t.Errorf("centre column white=%v dark=%v", white, dark)
	}
}

func TestICO(t *testing.T) {
	b, err := ICO([]int{16, 32, 48, 256})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 6+4*16 || b[2] != 1 || b[4] != 4 {
		t.Fatalf("bad ICONDIR: % x", b[:6])
	}
	if b[6] != 16 || b[6+16] != 32 || b[6+32] != 48 || b[6+48] != 0 {
		t.Errorf("bad entry sizes")
	}
}
