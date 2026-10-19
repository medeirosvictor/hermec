// Package video converts captured screens to I420 and encodes/decodes them
// as VP8 (pure-Go govpx).
package video

import "image"

// Frame is a tightly packed I420 (4:2:0 planar) picture: Y has stride W and
// U/V have stride (W+1)/2.
type Frame struct {
	W, H    int
	Y, U, V []byte
}

// NewFrame allocates a w×h frame.
func NewFrame(w, h int) *Frame {
	f := &Frame{W: w, H: h}
	f.ensure()
	return f
}

// ensure sizes the planes for W×H, reusing capacity.
func (f *Frame) ensure() {
	cw, ch := (f.W+1)/2, (f.H+1)/2
	f.Y = grow(f.Y, f.W*f.H)
	f.U = grow(f.U, cw*ch)
	f.V = grow(f.V, cw*ch)
}

func grow(b []byte, n int) []byte {
	if cap(b) >= n {
		return b[:n]
	}
	return make([]byte, n)
}

// FitSize returns the largest even size with the source aspect ratio that
// fits within maxW×maxH. It never upscales.
func FitSize(sw, sh, maxW, maxH int) (w, h int) {
	w, h = sw, sh
	if sw > maxW || sh > maxH {
		if sw*maxH > sh*maxW { // width-bound
			w, h = maxW, sh*maxW/sw
		} else {
			w, h = sw*maxH/sh, maxH
		}
	}
	w, h = w&^1, h&^1
	if w < 2 {
		w = 2
	}
	if h < 2 {
		h = 2
	}
	return w, h
}

// FromBGRA converts a BGRA image (stride bytes per row) into dst, box
// downscaling to dst.W×dst.H (set by the caller; planes are (re)allocated as
// needed). BT.601 limited range.
func FromBGRA(dst *Frame, src []byte, sw, sh, stride int) {
	if dst.W <= 0 || dst.H <= 0 || sw <= 0 || sh <= 0 || stride < sw*4 ||
		len(src) < (sh-1)*stride+sw*4 {
		return
	}
	dst.ensure()
	dw, dh := dst.W, dst.H
	cw := (dw + 1) / 2

	xs := make([]int, dw+1)
	for dx := 0; dx <= dw; dx++ {
		xs[dx] = dx * sw / dw
	}
	sumB := make([]uint32, dw)
	sumG := make([]uint32, dw)
	sumR := make([]uint32, dw)
	accU := make([]int32, cw)
	accV := make([]int32, cw)

	for dy := 0; dy < dh; dy++ {
		y0 := dy * sh / dh
		y1 := (dy + 1) * sh / dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for i := range sumB {
			sumB[i], sumG[i], sumR[i] = 0, 0, 0
		}
		for sy := y0; sy < y1; sy++ {
			row := src[sy*stride : sy*stride+sw*4]
			for dx := 0; dx < dw; dx++ {
				x0, x1 := xs[dx], xs[dx+1]
				if x1 <= x0 {
					x1 = x0 + 1
				}
				var b, g, r uint32
				p := row[x0*4 : x1*4]
				for i := 0; i+3 < len(p); i += 4 {
					b += uint32(p[i])
					g += uint32(p[i+1])
					r += uint32(p[i+2])
				}
				sumB[dx] += b
				sumG[dx] += g
				sumR[dx] += r
			}
		}
		if dy&1 == 0 {
			for i := range accU {
				accU[i], accV[i] = 0, 0
			}
		}
		yrow := dst.Y[dy*dw : dy*dw+dw]
		rows := int32(y1 - y0)
		for dx := 0; dx < dw; dx++ {
			x0, x1 := xs[dx], xs[dx+1]
			if x1 <= x0 {
				x1 = x0 + 1
			}
			n := uint32(rows) * uint32(x1-x0)
			b := int32((sumB[dx] + n/2) / n)
			g := int32((sumG[dx] + n/2) / n)
			r := int32((sumR[dx] + n/2) / n)
			yrow[dx] = byte(((66*r + 129*g + 25*b + 128) >> 8) + 16)
			accU[dx>>1] += ((-38*r - 74*g + 112*b + 128) >> 8) + 128
			accV[dx>>1] += ((112*r - 94*g - 18*b + 128) >> 8) + 128
		}
		if dy&1 == 1 || dy == dh-1 {
			cy := dy >> 1
			nrows := int32(dy&1) + 1
			for cx := 0; cx < cw; cx++ {
				cols := int32(2)
				if cx*2+1 >= dw {
					cols = 1
				}
				n := cols * nrows
				dst.U[cy*cw+cx] = byte((accU[cx] + n/2) / n)
				dst.V[cy*cw+cx] = byte((accV[cx] + n/2) / n)
			}
		}
	}
}

// RGBA converts the frame into dst (opaque), covering the overlap of both
// sizes. BT.601 limited range.
func (f *Frame) RGBA(dst *image.RGBA) {
	w, h := f.W, f.H
	if b := dst.Bounds(); b.Dx() < w {
		w = b.Dx()
	}
	if b := dst.Bounds(); b.Dy() < h {
		h = b.Dy()
	}
	cw := (f.W + 1) / 2
	for y := 0; y < h; y++ {
		yr := f.Y[y*f.W:]
		ur := f.U[(y>>1)*cw:]
		vr := f.V[(y>>1)*cw:]
		out := dst.Pix[y*dst.Stride:]
		for x := 0; x < w; x++ {
			c := 298 * (int32(yr[x]) - 16)
			d := int32(ur[x>>1]) - 128
			e := int32(vr[x>>1]) - 128
			o := out[x*4 : x*4+4]
			o[0] = clamp8((c + 409*e + 128) >> 8)
			o[1] = clamp8((c - 100*d - 208*e + 128) >> 8)
			o[2] = clamp8((c + 516*d + 128) >> 8)
			o[3] = 255
		}
	}
}

func clamp8(v int32) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}
