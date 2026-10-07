package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/hraban/opus"

	"github.com/medeirosvictor/hermec/client"
)

const (
	// senderIdle: a sender silent this long (and drained) is forgotten and
	// its decoder freed.
	senderIdle = 3 * time.Second
	// decodeMax is the largest Opus frame (120ms) at 48kHz.
	decodeMax = 5760
)

// sender is one remote speaker: an Opus decoder, a ring of decoded 20ms
// frames, and the jitter state machine that decides what each output slot
// plays.
//
// Locking (order: decMu before mu; mixer.mu is never held while taking
// either; the one reverse acquisition, fill taking decMu while holding mu,
// uses TryLock and so can never block or deadlock):
//   - decMu guards dec and pcm. Write holds it across the slow Opus decode.
//     The audio callback only TryLocks it (for PLC), so it never waits on a
//     decode: if a real frame is mid-decode the slot is left silent and
//     that frame arrives next.
//   - mu guards everything below it: the jitter state, ring, cur and last.
//     It is held only for short copies, never across a decode.
type sender struct {
	decMu sync.Mutex
	dec   *opus.Decoder
	pcm   [decodeMax]int16

	mu    sync.Mutex
	js    jitterState
	ring  []int16 // maxFrames * frameSize decoded samples
	head  int     // index (in frames) of the oldest queued frame
	cur   [frameSize]int16
	curN  int // valid samples in cur
	curAt int // next unread sample in cur
	out   []int16
	last  time.Time
	meter *Meter
}

// touch marks the sender active at now.
func (s *sender) touch(now time.Time) {
	s.mu.Lock()
	s.last = now
	s.mu.Unlock()
}

// push queues one decoded 20ms frame (len(pcm) <= frameSize, zero padded).
// s.mu must be held.
func (s *sender) push(pcm []int16) {
	slot := (s.head + s.js.depth) % maxFrames
	dst := s.ring[slot*frameSize : (slot+1)*frameSize]
	n := copy(dst, pcm)
	clear(dst[n:])
	s.head = (s.head + s.js.Arrive()) % maxFrames
}

// mixer decodes per-sender Opus into jitter buffers and mixes them on
// demand. It has no device dependency: Pull is what the audio callback
// calls. mixer.mu guards only the sender map; Opus decoding happens under
// the per-sender decMu, never under mixer.mu.
type mixer struct {
	mu      sync.Mutex
	senders map[string]*sender
	snap    []fpSender // Pull-owned scratch
	scratch [][]int16  // Pull-owned scratch
	now     func() time.Time
}

type fpSender struct {
	fp string
	s  *sender
}

func newMixer() *mixer {
	return &mixer{senders: map[string]*sender{}, now: time.Now}
}

// lookup returns fp's sender, creating it (decoder and ring are allocated
// outside the map lock) if needed.
func (m *mixer) lookup(fp string) *sender {
	m.mu.Lock()
	s := m.senders[fp]
	m.mu.Unlock()
	if s != nil {
		return s
	}
	dec, err := opus.NewDecoder(sampleRate, 1)
	if err != nil {
		return nil
	}
	// last starts at now so Pull's idle sweep cannot collect the sender in
	// the window before its first frame is pushed.
	ns := &sender{dec: dec, meter: &Meter{}, ring: make([]int16, maxFrames*frameSize), last: m.now()}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s = m.senders[fp]; s == nil {
		s = ns
		m.senders[fp] = ns
	}
	return s
}

// Write decodes one Opus frame from fp into its jitter buffer. An empty
// frame means "this frame was lost" and is concealed with Opus PLC; a frame
// that fails to decode is concealed the same way, so a corrupt packet never
// stalls the stream. Longer packets are split into 20ms frames.
func (m *mixer) Write(fp string, frame []byte) {
	s := m.lookup(fp)
	if s == nil {
		return
	}
	now := m.now()
	s.touch(now) // before the decode: an idle sender must not be swept mid-Write
	s.decMu.Lock()
	defer s.decMu.Unlock()
	var pcm []int16
	if len(frame) > 0 {
		if n, err := s.dec.Decode(frame, s.pcm[:]); err == nil && n > 0 {
			pcm = s.pcm[:n]
		}
	}
	if pcm == nil {
		if err := s.dec.DecodePLC(s.pcm[:frameSize]); err != nil {
			return
		}
		pcm = s.pcm[:frameSize]
	}
	s.meter.Observe(RMS(pcm), now)
	s.mu.Lock()
	s.last = now
	for len(pcm) > 0 {
		n := min(len(pcm), frameSize)
		s.push(pcm[:n])
		pcm = pcm[n:]
	}
	s.mu.Unlock()
}

// fill writes up to len(out) samples of s's audio into out, asking the
// jitter state machine for each 20ms slot, and returns how many it wrote
// (the rest of out is silence). s.mu must be held. It runs on the audio
// thread: no allocation once out has grown to the callback size.
//
// PLC decodes run here (bounded to plcBudget per outage, ~0.1ms each). The
// decoder is shared with Write, so it is TryLocked: if a real frame is
// mid-decode the slot is left silent rather than waiting.
func (s *sender) fill(out []int16) int {
	pos := 0
	for pos < len(out) {
		if s.curAt >= s.curN {
			switch s.js.Next() {
			case actPlay:
				copy(s.cur[:], s.ring[s.head*frameSize:(s.head+1)*frameSize])
				s.head = (s.head + 1) % maxFrames
			case actPLC:
				if !s.decMu.TryLock() {
					return pos
				}
				err := s.dec.DecodePLC(s.cur[:])
				s.decMu.Unlock()
				if err != nil {
					return pos
				}
			default:
				return pos
			}
			s.curN, s.curAt = frameSize, 0
		}
		n := copy(out[pos:], s.cur[s.curAt:s.curN])
		s.curAt += n
		pos += n
	}
	return pos
}

// idle reports whether s may be forgotten: drained (or never started, so a
// stray sub-start frame is dropped rather than played stale) and silent for
// senderIdle. s.mu must be held.
func (s *sender) idle(now time.Time) bool {
	return (s.js.depth == 0 || !s.js.started) && now.Sub(s.last) > senderIdle
}

// Pull fills dst with the mix of all senders, silence where none have audio.
// Locks are brief: the map lock only to snapshot, then each sender's mu for
// a copy (plus a bounded PLC decode).
func (m *mixer) Pull(dst []int16) {
	now := m.now()
	m.mu.Lock()
	clear(m.snap) // drop references so forgotten senders can be freed
	m.snap = m.snap[:0]
	for fp, s := range m.senders {
		m.snap = append(m.snap, fpSender{fp, s})
	}
	m.mu.Unlock()

	m.scratch = m.scratch[:0]
	anyIdle := false
	for _, e := range m.snap {
		s := e.s
		s.mu.Lock()
		if cap(s.out) < len(dst) {
			s.out = make([]int16, len(dst))
		}
		out := s.out[:len(dst)]
		if n := s.fill(out); n > 0 {
			clear(out[n:])
			m.scratch = append(m.scratch, out)
		}
		if s.idle(now) {
			anyIdle = true
		}
		s.mu.Unlock()
	}
	Mix(dst, m.scratch...)
	if !anyIdle {
		return
	}
	m.mu.Lock()
	for _, e := range m.snap {
		e.s.mu.Lock()
		gone := e.s.idle(now)
		e.s.mu.Unlock()
		if gone && m.senders[e.fp] == e.s {
			delete(m.senders, e.fp)
		}
	}
	m.mu.Unlock()
}

// Remove forgets fp's decoder immediately.
func (m *mixer) Remove(fp string) {
	m.mu.Lock()
	delete(m.senders, fp)
	m.mu.Unlock()
}

func (m *mixer) level(fp string) float64 {
	m.mu.Lock()
	s := m.senders[fp]
	now := m.now()
	m.mu.Unlock()
	if s == nil {
		return 0
	}
	return s.meter.LevelAt(now)
}

// Meters reports per-speaker output levels, keyed by sender fingerprint.
type Meters struct{ m *mixer }

// Level returns fp's smoothed 0..1 level; unknown or departed senders read 0.
func (ms *Meters) Level(fp string) float64 {
	if ms == nil {
		return 0
	}
	return ms.m.level(fp)
}

// playback is the speaker AudioSink, backed by a malgo playback device whose
// period is one 20ms frame, so each device callback consumes exactly one
// jitter-buffer slot (no gulp/sip mismatch).
//
// Audio-thread discipline (same as capture): the device callback only calls
// mixer.Pull, which holds each lock briefly (decoding happens outside, in
// Write), never blocks on a channel and allocates nothing once warm.
//
// Shutdown: Close marks the sink closed (later WriteOpusFrame calls are
// dropped) and Uninit()s the device, which stops it and waits for any
// in-flight callback to return. Do not call Close from the callback.
type playback struct {
	mix    *mixer
	mctx   *malgo.AllocatedContext
	dev    *malgo.Device
	closed chan struct{}
	once   sync.Once
	buf    []int16
}

// Playback opens the output device named device (48kHz mono; "" = system
// default) and returns an AudioSink that decodes, jitter-buffers and mixes
// Opus frames from any number of senders, plus per-speaker level meters.
// Type-assert the sink to io.Closer to stop it.
func Playback(device string) (client.AudioSink, *Meters, error) {
	mctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, nil, err
	}
	free := func() {
		_ = mctx.Uninit()
		mctx.Free()
	}
	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = 1
	cfg.SampleRate = sampleRate
	cfg.PeriodSizeInMilliseconds = frameMs
	if device != "" {
		infos, err := mctx.Devices(malgo.Playback)
		if err != nil {
			free()
			return nil, nil, err
		}
		found := false
		for i := range infos {
			if infos[i].Name() == device {
				cfg.Playback.DeviceID = infos[i].ID.Pointer()
				found = true
				break
			}
		}
		if !found {
			free()
			return nil, nil, fmt.Errorf("output device %q not found", device)
		}
	}
	p := &playback{
		mix:    newMixer(),
		mctx:   mctx,
		closed: make(chan struct{}),
		buf:    make([]int16, 4*frameSize),
	}
	dev, err := malgo.InitDevice(mctx.Context, cfg, malgo.DeviceCallbacks{Data: p.onData})
	if err != nil {
		free()
		return nil, nil, err
	}
	p.dev = dev
	if err := dev.Start(); err != nil {
		dev.Uninit()
		free()
		return nil, nil, err
	}
	return p, &Meters{m: p.mix}, nil
}

// WriteOpusFrame implements client.AudioSink.
func (p *playback) WriteOpusFrame(fromFP string, frame []byte) {
	select {
	case <-p.closed:
		return
	default:
	}
	p.mix.Write(fromFP, frame)
}

// onData runs on the audio thread: pull, mix, convert. Nothing else.
func (p *playback) onData(out, _ []byte, _ uint32) {
	n := len(out) / 2
	if n > len(p.buf) {
		p.buf = make([]int16, n) // device asked for a bigger period; rare
	}
	buf := p.buf[:n]
	p.mix.Pull(buf)
	for i, s := range buf {
		binary.LittleEndian.PutUint16(out[2*i:], uint16(s))
	}
}

// Close stops playback. Safe to call more than once.
func (p *playback) Close() error {
	p.once.Do(func() {
		close(p.closed)
		p.dev.Uninit() // stops the device and waits for the callback
		_ = p.mctx.Uninit()
		p.mctx.Free()
	})
	return nil
}

// Forget drops a sender's decoder and meter right away (otherwise they are
// cleaned up after a few seconds of silence).
func (p *playback) Forget(fp string) { p.mix.Remove(fp) }

var _ io.Closer = (*playback)(nil)
