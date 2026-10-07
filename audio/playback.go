package audio

import (
	"encoding/binary"
	"io"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/hraban/opus"

	"github.com/medeirosvictor/hermec/client"
)

const (
	// maxQueued caps one sender's decoded backlog; older audio is dropped.
	maxQueued = 10 * frameSize // 200ms
	// prefill is how much a sender must have queued before it is heard
	// (after start or an underrun); it absorbs network jitter.
	prefill = 2 * frameSize // 40ms
	// senderIdle: a sender silent this long (and drained) is forgotten and
	// its decoder freed.
	senderIdle = 3 * time.Second
	// decodeMax is the largest Opus frame (120ms) at 48kHz.
	decodeMax = 5760
)

type take struct {
	s *sender
	n int
}

type sender struct {
	dec    *opus.Decoder
	queue  []int16
	primed bool
	last   time.Time
	meter  *Meter
}

// mixer decodes per-sender Opus into queues and mixes them on demand. It has
// no device dependency: Pull is what the oto reader calls.
type mixer struct {
	mu      sync.Mutex
	senders map[string]*sender
	pcm     [decodeMax]int16
	scratch [][]int16
	taken   []take
	now     func() time.Time
}

func newMixer() *mixer {
	return &mixer{senders: map[string]*sender{}, now: time.Now}
}

// Write decodes one Opus frame from fp. An empty frame means "this frame was
// lost" and is concealed with Opus PLC. A frame that fails to decode is
// concealed the same way, so a corrupt packet never stalls the stream.
func (m *mixer) Write(fp string, frame []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.senders[fp]
	if s == nil {
		dec, err := opus.NewDecoder(sampleRate, 1)
		if err != nil {
			return
		}
		s = &sender{dec: dec, meter: &Meter{}}
		m.senders[fp] = s
	}
	now := m.now()
	s.last = now
	var pcm []int16
	if len(frame) > 0 {
		n, err := s.dec.Decode(frame, m.pcm[:])
		if err == nil {
			pcm = m.pcm[:n]
		}
	}
	if pcm == nil {
		if err := s.dec.DecodePLC(m.pcm[:frameSize]); err != nil {
			return
		}
		pcm = m.pcm[:frameSize]
	}
	s.meter.Observe(RMS(pcm), now)
	s.queue = append(s.queue, pcm...)
	if over := len(s.queue) - maxQueued; over > 0 {
		s.queue = append(s.queue[:0], s.queue[over:]...)
	}
}

// Pull fills dst with the mix of all senders, silence where none have audio.
func (m *mixer) Pull(dst []int16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.scratch = m.scratch[:0]
	m.taken = m.taken[:0]
	for _, s := range m.senders {
		if !s.primed && len(s.queue) >= prefill {
			s.primed = true
		}
		if !s.primed {
			continue
		}
		n := min(len(dst), len(s.queue))
		m.scratch = append(m.scratch, s.queue[:n])
		m.taken = append(m.taken, take{s, n})
		if n < len(dst) {
			s.primed = false // underrun: rebuffer
		}
	}
	Mix(dst, m.scratch...)
	for _, t := range m.taken {
		t.s.queue = append(t.s.queue[:0], t.s.queue[t.n:]...)
	}
	for fp, s := range m.senders {
		// An idle sender is forgotten when drained, or when it never
		// primed (a stray sub-prefill frame is dropped, not played stale).
		if (len(s.queue) == 0 || !s.primed) && now.Sub(s.last) > senderIdle {
			delete(m.senders, fp)
		}
	}
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

// playback is the speaker AudioSink. Close stops the player; frames
// written afterwards are ignored.
type playback struct {
	mix    *mixer
	player *oto.Player
	closed chan struct{}
	once   sync.Once
	buf    []int16
}

var (
	otoOnce sync.Once
	otoCtx  *oto.Context
	otoErr  error
)

// otoContext returns the process-wide oto context (oto allows only one).
func otoContext() (*oto.Context, error) {
	otoOnce.Do(func() {
		ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
			SampleRate:   sampleRate,
			ChannelCount: 1,
			Format:       oto.FormatSignedInt16LE,
			BufferSize:   60 * time.Millisecond,
		})
		if err != nil {
			otoErr = err
			return
		}
		<-ready
		if err := ctx.Err(); err != nil {
			otoErr = err
			return
		}
		otoCtx = ctx
	})
	return otoCtx, otoErr
}

// Playback opens the default speaker (48kHz mono) and returns an AudioSink
// that decodes and mixes Opus frames from any number of senders, plus
// per-speaker level meters. Type-assert the sink to io.Closer to stop it.
func Playback() (client.AudioSink, *Meters, error) {
	ctx, err := otoContext()
	if err != nil {
		return nil, nil, err
	}
	p := &playback{mix: newMixer(), closed: make(chan struct{})}
	p.player = ctx.NewPlayer(p)
	p.player.Play()
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

// Read implements io.Reader for oto: always a full buffer, silence when idle.
func (p *playback) Read(b []byte) (int, error) {
	select {
	case <-p.closed:
		return 0, io.EOF
	default:
	}
	n := len(b) / 2
	if cap(p.buf) < n {
		p.buf = make([]int16, n)
	}
	buf := p.buf[:n]
	p.mix.Pull(buf)
	for i, s := range buf {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(s))
	}
	return 2 * n, nil
}

// Close stops playback. Safe to call more than once. The shared oto context
// stays alive for a later Playback().
func (p *playback) Close() error {
	p.once.Do(func() {
		close(p.closed)
		p.player.PauseAndStopReading()
	})
	return nil
}

// Forget drops a sender's decoder and meter right away (otherwise they are
// cleaned up after a few seconds of silence).
func (p *playback) Forget(fp string) { p.mix.Remove(fp) }

var _ io.Closer = (*playback)(nil)
