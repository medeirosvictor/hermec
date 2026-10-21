package video

import "testing"

func solid(w, h int) []byte {
	b := make([]byte, w*h*4)
	for i := range b {
		b[i] = 255 // white
	}
	return b
}

func TestLetterboxKeepsAspectWithBlackBars(t *testing.T) {
	cases := []struct {
		name   string
		sw, sh int
		bars   string // "side" or "top"
	}{
		{"4:3", 400, 300, "side"},
		{"portrait", 300, 600, "side"},
		{"ultrawide", 800, 200, "top"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dst := NewFrame(160, 90)
			FromBGRALetterbox(dst, solid(c.sw, c.sh), c.sw, c.sh, c.sw*4, nil)
			minX, maxX, minY, maxY := dst.W, -1, dst.H, -1
			for y := 0; y < dst.H; y++ {
				for x := 0; x < dst.W; x++ {
					if dst.Y[y*dst.W+x] > 100 {
						minX, maxX = min(minX, x), max(maxX, x)
						minY, maxY = min(minY, y), max(maxY, y)
					}
				}
			}
			w, h := maxX-minX+1, maxY-minY+1
			if w*c.sh < h*c.sw-2*c.sh || w*c.sh > h*c.sw+2*c.sh { // aspect within a pixel or two
				t.Fatalf("picture %dx%d does not keep %dx%d", w, h, c.sw, c.sh)
			}
			if c.bars == "side" && (minX == 0 || w >= dst.W) || c.bars == "top" && (minY == 0 || h >= dst.H) {
				t.Fatalf("no bars: box x %d-%d y %d-%d", minX, maxX, minY, maxY)
			}
			if dst.Y[0] != 16 || dst.U[0] != 128 || dst.V[0] != 128 {
				t.Fatalf("corner = %d/%d/%d, want 16/128/128", dst.Y[0], dst.U[0], dst.V[0])
			}
		})
	}
}

func TestLetterboxSameAspectFillsFrame(t *testing.T) {
	dst := NewFrame(160, 90)
	FromBGRALetterbox(dst, solid(320, 180), 320, 180, 320*4, nil)
	if dst.Y[0] < 200 || dst.Y[len(dst.Y)-1] < 200 {
		t.Fatalf("same aspect left bars: %d %d", dst.Y[0], dst.Y[len(dst.Y)-1])
	}
}
