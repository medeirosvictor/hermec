package audio

import (
	"encoding/binary"
	"io"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/hraban/opus"

	"github.com/medeirosvictor/hermec/client"
)

const (
	// captureRing is how many 20ms PCM frames are kept while nobody is
	// reading (muted). Older frames are dropped as new ones arrive.
	captureRing = 3
	// captureMaxAge: a queued frame older than this is stale and skipped,
	// so unmuting never replays the pause.
	captureMaxAge = 3 * frameMs * time.Millisecond
)

type pcmFrame struct {
	pcm [frameSize]int16
	at  time.Time
}

// capture is the microphone AudioSource. The device callback only slices
// samples into 20ms PCM frames and pushes them into a tiny drop-oldest
// ring; encoding happens in ReadOpusFrame, so frames dropped while muted
// cost nothing and the encoder never sees a discontinuity burst.
//
// Shutdown: the returned value also implements io.Closer. Close stops the
// device and makes ReadOpusFrame return io.EOF (a blocked call is woken).
type capture struct {
	mctx   *malgo.AllocatedContext
	dev    *malgo.Device
	enc    *opus.Encoder
	meter  *Meter
	frames chan pcmFrame
	done   chan struct{}
	once   sync.Once

	// device-callback-owned
	cur  pcmFrame
	curN int

	obuf []byte
}

// Capture opens the default microphone (48kHz mono) and returns an
// AudioSource of 20ms Opus frames at 32kbps, plus a Meter of the mic level.
// Type-assert the source to io.Closer to stop it.
func Capture() (client.AudioSource, *Meter, error) {
	enc, err := opus.NewEncoder(sampleRate, 1, opus.AppVoIP)
	if err != nil {
		return nil, nil, err
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return nil, nil, err
	}
	_ = enc.SetInBandFEC(true)
	_ = enc.SetPacketLossPerc(10)

	mctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, nil, err
	}
	c := &capture{
		mctx:   mctx,
		enc:    enc,
		meter:  &Meter{},
		frames: make(chan pcmFrame, captureRing),
		done:   make(chan struct{}),
		obuf:   make([]byte, 1500),
	}
	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = 1
	cfg.SampleRate = sampleRate
	cfg.PeriodSizeInMilliseconds = frameMs
	dev, err := malgo.InitDevice(mctx.Context, cfg, malgo.DeviceCallbacks{Data: c.onData})
	if err != nil {
		c.freeCtx()
		return nil, nil, err
	}
	c.dev = dev
	if err := dev.Start(); err != nil {
		dev.Uninit()
		c.freeCtx()
		return nil, nil, err
	}
	return c, c.meter, nil
}

func (c *capture) freeCtx() {
	_ = c.mctx.Uninit()
	c.mctx.Free()
}

// onData runs on the audio thread: no blocking, no encoding.
func (c *capture) onData(_, in []byte, _ uint32) {
	for len(in) >= 2 {
		c.cur.pcm[c.curN] = int16(binary.LittleEndian.Uint16(in))
		in = in[2:]
		c.curN++
		if c.curN < frameSize {
			continue
		}
		c.curN = 0
		c.cur.at = time.Now()
		c.meter.Observe(RMS(c.cur.pcm[:]), c.cur.at)
		c.push(c.cur)
	}
}

// push enqueues f, dropping the oldest frame when the ring is full.
func (c *capture) push(f pcmFrame) {
	for {
		select {
		case c.frames <- f:
			return
		default:
		}
		select {
		case <-c.frames:
		default:
		}
	}
}

// ReadOpusFrame blocks for the next fresh 20ms frame and returns it encoded.
// Stale frames (captured while the caller was paused) are discarded. It
// returns io.EOF after Close.
func (c *capture) ReadOpusFrame() ([]byte, error) {
	for {
		select {
		case <-c.done:
			return nil, io.EOF
		case f := <-c.frames:
			if time.Since(f.at) > captureMaxAge {
				continue
			}
			n, err := c.enc.Encode(f.pcm[:], c.obuf)
			if err != nil {
				return nil, err
			}
			out := make([]byte, n)
			copy(out, c.obuf[:n])
			return out, nil
		}
	}
}

// Close stops the microphone. Safe to call more than once.
func (c *capture) Close() error {
	c.once.Do(func() {
		close(c.done)
		c.dev.Uninit() // waits for the callback to finish
		c.freeCtx()
	})
	return nil
}

var _ io.Closer = (*capture)(nil)
