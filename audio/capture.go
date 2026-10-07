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
	// captureRing is how many 20ms PCM frames are kept. It absorbs normal
	// producer/consumer jitter; when nobody reads (muted) older frames are
	// dropped as new ones arrive, bounding latency at 4*20ms = 80ms.
	captureRing = 4
	// pauseGap: if the previous read was longer ago than this, the caller
	// was paused (muted/stalled) and the backlog is skipped to the newest
	// frame. Below it, frames are served strictly in order.
	pauseGap = 100 * time.Millisecond
)

// dropCount decides how many of the oldest queued frames to discard before
// serving, given the current time, the previous read time, and the capture
// times of the queued frames (oldest first). After a pause (gap > pauseGap,
// or no previous read) everything but the newest is dropped so unmuting
// never replays the pause; during continuous reading nothing is dropped.
func dropCount(now, lastReadAt time.Time, queued []time.Time) int {
	if len(queued) < 2 {
		return 0
	}
	if lastReadAt.IsZero() || now.Sub(lastReadAt) > pauseGap {
		return len(queued) - 1
	}
	return 0
}

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

	obuf  []byte
	clock func() time.Time

	// reader-owned: when the previous frame was served (zero before first)
	lastReadAt time.Time
	pending    []pcmFrame  // drained from frames, not yet served
	times      []time.Time // scratch for dropCount
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
		clock:  time.Now,
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

// nextFrame blocks for the next PCM frame to send. Frames are returned in
// order; the backlog is skipped to the newest only when resuming after a
// pause (see dropCount). It returns io.EOF after Close.
func (c *capture) nextFrame() (pcmFrame, error) {
	// Check done first: with done closed and frames buffered, a bare
	// select would pick randomly and could serve a frame after Close.
	select {
	case <-c.done:
		return pcmFrame{}, io.EOF
	default:
	}
	if len(c.pending) == 0 {
		select {
		case <-c.done:
			return pcmFrame{}, io.EOF
		case f := <-c.frames:
			c.pending = append(c.pending, f)
		}
	}
	// Move whatever else is queued into pending (oldest first).
drain:
	for {
		select {
		case g := <-c.frames:
			c.pending = append(c.pending, g)
		default:
			break drain
		}
	}
	// Draining bypasses the channel's drop-oldest, so enforce the same hard
	// cap here: a slow-but-never-pausing consumer must not grow latency.
	if over := len(c.pending) - captureRing; over > 0 {
		c.pending = append(c.pending[:0], c.pending[over:]...)
	}
	now := c.clock()
	// pending (not just the channel) is what dropCount sees.
	c.times = c.times[:0]
	for _, p := range c.pending {
		c.times = append(c.times, p.at)
	}
	drop := dropCount(now, c.lastReadAt, c.times)
	f := c.pending[drop]
	c.pending = append(c.pending[:0], c.pending[drop+1:]...)
	select {
	case <-c.done:
		return pcmFrame{}, io.EOF
	default:
	}
	c.lastReadAt = now
	return f, nil
}

// ReadOpusFrame blocks for the next 20ms frame and returns it encoded. It
// returns io.EOF after Close.
func (c *capture) ReadOpusFrame() ([]byte, error) {
	f, err := c.nextFrame()
	if err != nil {
		return nil, err
	}
	n, err := c.enc.Encode(f.pcm[:], c.obuf)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n)
	copy(out, c.obuf[:n])
	return out, nil
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
