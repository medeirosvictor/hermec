package video

import (
	"image"
	"math"
	"testing"
)

// synthFrame draws a desktop-like I420 frame: light background, text-like
// stripes, and a textured window that moves 12 px per frame.
func synthFrame(f *Frame, i int) {
	w, h := f.W, f.H
	cw := (w + 1) / 2
	for y := 0; y < h; y++ {
		row := f.Y[y*w : y*w+w]
		line, inLine := y/16, y%16 < 10
		for x := 0; x < w; x++ {
			v := byte(235)
			if inLine && x > 40 && x < w-40 {
				hsh := uint32(line*7919+x/6*104729) * 2654435761
				if (hsh>>20)%5 != 0 && (x/6+line)%7 != 0 {
					v = byte(30 + (hsh>>12)%40)
				}
			}
			row[x] = v
		}
	}
	for y := 0; y < (h+1)/2; y++ {
		for x := 0; x < cw; x++ {
			f.U[y*cw+x], f.V[y*cw+x] = 128, 128
		}
	}
	// moving window
	ww, wh := w/3, h/3
	x0 := (i*12)%(w-ww-2) + 1
	y0 := h/4 + (i*3)%(h/4)
	for y := y0; y < y0+wh && y < h; y++ {
		for x := x0; x < x0+ww && x < w; x++ {
			f.Y[y*w+x] = byte(90 + (x-x0)/4 + ((x^y)&7)*3)
		}
	}
	for y := y0 / 2; y < (y0+wh)/2 && y < (h+1)/2; y++ {
		for x := x0 / 2; x < (x0+ww)/2 && x < cw; x++ {
			f.U[y*cw+x], f.V[y*cw+x] = 90, 170
		}
	}
}

func synthBGRA(sw, sh, i int) []byte {
	f := NewFrame(sw, sh)
	synthFrame(f, i)
	pix := make([]byte, sw*sh*4)
	img := image.NewRGBA(image.Rect(0, 0, sw, sh))
	f.RGBA(img)
	for p := 0; p < sw*sh; p++ {
		pix[p*4], pix[p*4+1], pix[p*4+2], pix[p*4+3] = img.Pix[p*4+2], img.Pix[p*4+1], img.Pix[p*4], 255
	}
	return pix
}

func psnrY(a, b *Frame) float64 {
	var se float64
	for i := range a.Y[:a.W*a.H] {
		d := float64(a.Y[i]) - float64(b.Y[i])
		se += d * d
	}
	if se == 0 {
		return 99
	}
	return 10 * math.Log10(255*255/(se/float64(a.W*a.H)))
}

func TestFitSize(t *testing.T) {
	cases := []struct{ sw, sh, mw, mh, w, h int }{
		{2560, 1440, 1280, 720, 1280, 720},
		{1920, 1200, 1280, 720, 1152, 720},
		{800, 600, 1280, 720, 800, 600},
		{801, 601, 1280, 720, 800, 600},
		{1921, 1081, 1920, 1080, 1918, 1080},
		{3840, 2160, 1920, 1080, 1920, 1080},
	}
	for _, c := range cases {
		w, h := FitSize(c.sw, c.sh, c.mw, c.mh)
		if w != c.w || h != c.h {
			t.Errorf("FitSize(%d,%d,%d,%d)=%d,%d want %d,%d", c.sw, c.sh, c.mw, c.mh, w, h, c.w, c.h)
		}
		if w%2 != 0 || h%2 != 0 {
			t.Errorf("odd result %d,%d", w, h)
		}
	}
}

func TestFromBGRAColors(t *testing.T) {
	cases := []struct {
		name    string
		b, g, r byte
		y, u, v int
	}{
		{"red", 0, 0, 255, 82, 90, 240},
		{"green", 0, 255, 0, 145, 54, 34},
		{"blue", 255, 0, 0, 41, 240, 110},
	}
	near := func(got byte, want int) bool { d := int(got) - want; return d >= -2 && d <= 2 }
	for _, c := range cases {
		sw, sh := 64, 48
		src := make([]byte, sw*sh*4)
		for p := 0; p < sw*sh; p++ {
			src[p*4], src[p*4+1], src[p*4+2], src[p*4+3] = c.b, c.g, c.r, 255
		}
		dst := NewFrame(32, 24) // also exercises downscale
		FromBGRA(dst, src, sw, sh, sw*4)
		if !near(dst.Y[100], c.y) || !near(dst.U[50], c.u) || !near(dst.V[50], c.v) {
			t.Errorf("%s: got Y%d U%d V%d want %d %d %d", c.name, dst.Y[100], dst.U[50], dst.V[50], c.y, c.u, c.v)
		}
	}
}

func TestFromBGRAStride(t *testing.T) {
	// stride larger than width*4: padding must be ignored
	sw, sh, stride := 8, 8, 8*4+16
	src := make([]byte, stride*sh)
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			o := y*stride + x*4
			src[o], src[o+1], src[o+2], src[o+3] = 0, 0, 255, 255
		}
		for x := sw * 4; x < stride; x++ {
			src[y*stride+x] = 7
		}
	}
	dst := NewFrame(8, 8)
	FromBGRA(dst, src, sw, sh, stride)
	if dst.Y[63] < 80 || dst.Y[63] > 84 {
		t.Fatalf("padding leaked: Y=%d", dst.Y[63])
	}
}

func TestRGBARoundTripColor(t *testing.T) {
	src := make([]byte, 16*16*4)
	for p := 0; p < 256; p++ {
		src[p*4], src[p*4+1], src[p*4+2], src[p*4+3] = 20, 200, 90, 255
	}
	f := NewFrame(16, 16)
	FromBGRA(f, src, 16, 16, 64)
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	f.RGBA(img)
	p := img.Pix[8*64+8*4:]
	d := func(a byte, b int) bool { x := int(a) - b; return x < -4 || x > 4 }
	if d(p[0], 90) || d(p[1], 200) || d(p[2], 20) || p[3] != 255 {
		t.Fatalf("got %v want ~[90 200 20 255]", p[:4])
	}
}

func TestRoundTrip(t *testing.T) {
	enc, err := NewEncoder(1280, 720, 15, 1500)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	f := NewFrame(1280, 720)
	var out *Frame
	for i := 0; i < 30; i++ {
		synthFrame(f, i)
		pkt, key, err := enc.Encode(f, false)
		if err != nil {
			t.Fatal(err)
		}
		if (i == 0) != key {
			t.Fatalf("frame %d key=%v", i, key)
		}
		if len(pkt) == 0 {
			t.Fatalf("frame %d empty packet", i)
		}
		out, err = dec.Decode(pkt)
		if err != nil {
			t.Fatal(err)
		}
		if out == nil {
			t.Fatalf("frame %d: no output", i)
		}
	}
	if out.W != 1280 || out.H != 720 {
		t.Fatalf("size %dx%d", out.W, out.H)
	}
	if p := psnrY(f, out); p < 30 {
		t.Fatalf("PSNR %.1f dB < 30", p)
	} else {
		t.Logf("PSNR %.1f dB", p)
	}
}

func TestForceKey(t *testing.T) {
	enc, err := NewEncoder(320, 240, 15, 500)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	f := NewFrame(320, 240)
	for i := 0; i < 5; i++ {
		synthFrame(f, i)
		_, key, err := enc.Encode(f, i == 3)
		if err != nil {
			t.Fatal(err)
		}
		if want := i == 0 || i == 3; key != want {
			t.Fatalf("frame %d key=%v want %v", i, key, want)
		}
	}
}

func TestKeyEvery5s(t *testing.T) {
	enc, err := NewEncoder(320, 240, 15, 500)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	f := NewFrame(320, 240)
	for i := 0; i <= 76; i++ {
		synthFrame(f, i)
		_, key, err := enc.Encode(f, false)
		if err != nil {
			t.Fatal(err)
		}
		if want := i == 0 || i == 75; key != want {
			t.Fatalf("frame %d key=%v want %v", i, key, want)
		}
	}
}

func TestDecodeGarbage(t *testing.T) {
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error for garbage packet")
	}
}
