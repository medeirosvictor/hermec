package video

import (
	"errors"
	"fmt"

	"github.com/thesyncim/govpx"
)

// Encoder encodes I420 frames as raw VP8 payloads (one per call), using
// realtime CBR rate control with a keyframe at least every 5 seconds.
type Encoder struct {
	enc      *govpx.VP8Encoder
	w, h     int
	keyEvery int
	sinceKey int
	// pendingKey: a keyframe was requested/due but not yet emitted.
	pendingKey bool
	n          uint64
	buf        []byte
}

// NewEncoder creates a w×h (even) encoder at fps and a CBR target of kbps.
func NewEncoder(w, h, fps, kbps int) (*Encoder, error) {
	if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 || fps <= 0 || kbps <= 0 {
		return nil, errors.New("video: invalid encoder parameters")
	}
	e, err := govpx.NewVP8Encoder(govpx.EncoderOptions{
		Width:               w,
		Height:              h,
		FPS:                 fps,
		RateControlMode:     govpx.RateControlCBR,
		TargetBitrateKbps:   kbps,
		MinQuantizer:        encMinQ,
		MaxQuantizer:        encMaxQ,
		QuantizerRangeSet:   true,
		UndershootPct:       100,
		OvershootPct:        15,
		BufferSizeMs:        1000,
		BufferInitialSizeMs: 500,
		BufferOptimalSizeMs: 600,
		Deadline:            govpx.DeadlineRealtime,
		CpuUsed:             encCPUUsed,
		Threads:             encThreads,
		ScreenContentMode:   encScreen,
		StaticThreshold:     encStatic,
		// We force keyframes ourselves so the cadence is exact; keep
		// govpx's own interval well out of the way.
		KeyFrameInterval: 100 * fps,
	})
	if err != nil {
		return nil, fmt.Errorf("video: vp8 encoder: %w", err)
	}
	return &Encoder{
		enc: e, w: w, h: h,
		keyEvery: 5 * fps,
		buf:      make([]byte, 1<<20),
	}, nil
}

// Encode compresses f (which must match the encoder size). The returned
// packet aliases an internal buffer and is ONLY valid until the next Encode
// call: copy it before handing it to another goroutine or keeping it. pkt is
// nil when rate control dropped the frame; a requested keyframe is then
// carried over to the next encoded frame.
func (e *Encoder) Encode(f *Frame, forceKey bool) (pkt []byte, key bool, err error) {
	if f.W != e.w || f.H != e.h {
		return nil, false, fmt.Errorf("video: frame %dx%d, encoder %dx%d", f.W, f.H, e.w, e.h)
	}
	cw := (e.w + 1) / 2
	if len(f.Y) < e.w*e.h || len(f.U) < cw*((e.h+1)/2) || len(f.V) < cw*((e.h+1)/2) {
		return nil, false, errors.New("video: frame planes too small")
	}
	if forceKey {
		e.pendingKey = true
	}
	var flags govpx.EncodeFlags
	if e.pendingKey || e.n == 0 || e.sinceKey+1 >= e.keyEvery {
		flags = govpx.EncodeForceKeyFrame
		e.pendingKey = true
	}
	img := govpx.Image{
		Width: e.w, Height: e.h,
		Y: f.Y, U: f.U, V: f.V,
		YStride: e.w, UStride: cw, VStride: cw,
	}
	res, err := e.enc.EncodeInto(e.buf, img, e.n, 1, flags)
	e.n++
	if err != nil {
		return nil, false, fmt.Errorf("video: vp8 encode: %w", err)
	}
	if res.Dropped || len(res.Data) == 0 {
		e.noteResult(false, false)
		return nil, false, nil
	}
	e.noteResult(true, res.KeyFrame)
	return res.Data, res.KeyFrame, nil
}

// noteResult updates keyframe bookkeeping after an encode attempt. A dropped
// frame leaves pendingKey set so the next emitted frame is a keyframe.
func (e *Encoder) noteResult(emitted, key bool) {
	if !emitted {
		e.sinceKey++
		return
	}
	if key {
		e.sinceKey = 0
		e.pendingKey = false
	} else {
		e.sinceKey++
	}
}

// Close releases the encoder.
func (e *Encoder) Close() { e.enc.Close() }

// Decoder decodes raw VP8 payloads into I420 frames.
type Decoder struct {
	dec *govpx.VP8Decoder
}

// NewDecoder creates a VP8 decoder.
func NewDecoder() (*Decoder, error) {
	d, err := govpx.NewVP8Decoder(govpx.DecoderOptions{Threads: decThreads})
	if err != nil {
		return nil, fmt.Errorf("video: vp8 decoder: %w", err)
	}
	return &Decoder{dec: d}, nil
}

// Close releases the decoder.
func (d *Decoder) Close() { d.dec.Close() }

// Decode decodes one packet. The returned frame is newly allocated and owned
// by the caller (safe to hand to another goroutine); it is nil when no
// visible frame is ready.
func (d *Decoder) Decode(pkt []byte) (*Frame, error) {
	if err := d.dec.Decode(pkt); err != nil {
		return nil, fmt.Errorf("video: vp8 decode: %w", err)
	}
	img, ok := d.dec.NextFrame()
	if !ok {
		return nil, nil
	}
	out := NewFrame(img.Width, img.Height)
	cw, ch := (img.Width+1)/2, (img.Height+1)/2
	copyPlane(out.Y, img.Y, img.YStride, img.Width, img.Height)
	copyPlane(out.U, img.U, img.UStride, cw, ch)
	copyPlane(out.V, img.V, img.VStride, cw, ch)
	return out, nil
}

func copyPlane(dst, src []byte, stride, w, h int) {
	for y := 0; y < h; y++ {
		copy(dst[y*w:y*w+w], src[y*stride:y*stride+w])
	}
}
