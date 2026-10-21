package video

// FromBGRALetterbox is FromBGRA that keeps the source aspect ratio: the
// picture is scaled to fit inside dst and centred, and the rest is black.
// scratch (may be nil) is reused for the scaled picture and returned for the
// next call.
func FromBGRALetterbox(dst *Frame, src []byte, sw, sh, stride int, scratch *Frame) *Frame {
	dw, dh := dst.W, dst.H
	if dw <= 0 || dh <= 0 || sw <= 0 || sh <= 0 {
		return scratch
	}
	w, h := dw, dh
	if sw*dh > sh*dw { // wider than dst: width-bound
		h = sh * dw / sw
	} else {
		w = sw * dh / sh
	}
	w, h = w&^1, h&^1
	if w < 2 {
		w = 2
	}
	if h < 2 {
		h = 2
	}
	if w >= dw-1 && h >= dh-1 { // same aspect within rounding: no bars
		FromBGRA(dst, src, sw, sh, stride)
		return scratch
	}
	if w > dw {
		w = dw &^ 1
	}
	if h > dh {
		h = dh &^ 1
	}
	if scratch == nil {
		scratch = &Frame{}
	}
	scratch.W, scratch.H = w, h
	FromBGRA(scratch, src, sw, sh, stride)

	dst.ensure()
	for i := range dst.Y {
		dst.Y[i] = 16
	}
	for i := range dst.U {
		dst.U[i], dst.V[i] = 128, 128
	}
	x0, y0 := ((dw-w)/2)&^1, ((dh-h)/2)&^1
	for r := 0; r < h; r++ {
		copy(dst.Y[(y0+r)*dw+x0:], scratch.Y[r*w:(r+1)*w])
	}
	dcw, cw := (dw+1)/2, (w+1)/2
	for r := 0; r < (h+1)/2; r++ {
		o := (y0/2+r)*dcw + x0/2
		copy(dst.U[o:], scratch.U[r*cw:(r+1)*cw])
		copy(dst.V[o:], scratch.V[r*cw:(r+1)*cw])
	}
	return scratch
}
