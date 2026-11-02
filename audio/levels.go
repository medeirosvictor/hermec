// Package audio is the hardware side of voice: microphone capture and
// speaker playback. It is the only package that imports malgo and gopus
// (all cgo or device code); client and server never import it. The math in
// this file is pure so it can be tested without devices.
package audio

import (
	"math"
	"sync"
	"time"
)

const (
	sampleRate = 48000
	frameMs    = 20
	frameSize  = sampleRate * frameMs / 1000 // 960 mono samples
	bitrate    = 32000

	// levelHalfLife is how fast a meter falls after the signal stops.
	levelHalfLife = 150 * time.Millisecond
)

// RMS returns the root-mean-square of pcm normalised to 0..1 (full scale
// square wave = 1). Empty input is 0.
func RMS(pcm []int16) float64 {
	if len(pcm) == 0 {
		return 0
	}
	var sum float64
	for _, s := range pcm {
		f := float64(s) / 32768
		sum += f * f
	}
	return math.Min(1, math.Sqrt(sum/float64(len(pcm))))
}

// Decay returns level after elapsed time of silence: exponential decay with
// levelHalfLife.
func Decay(level float64, elapsed time.Duration) float64 {
	if elapsed <= 0 || level <= 0 {
		return level
	}
	return level * math.Pow(0.5, float64(elapsed)/float64(levelHalfLife))
}

// Mix sums streams sample by sample into dst, scaling stream i by gains[i]
// (nil gains = unity), then soft-limits the sum. Streams shorter than dst
// count as silence.
func Mix(dst []int16, streams [][]int16, gains []float64) {
	for i := range dst {
		var acc float64
		for j, s := range streams {
			if i >= len(s) {
				continue
			}
			g := 1.0
			if j < len(gains) {
				g = gains[j]
			}
			acc += float64(s[i]) * g
		}
		dst[i] = softLimit(acc)
	}
}

// ApplyGain scales pcm in place by gain and soft-limits it. Unity gain is a
// no-op, so an untouched stream stays bit-exact.
func ApplyGain(pcm []int16, gain float64) {
	if gain == 1 {
		return
	}
	for i, s := range pcm {
		pcm[i] = softLimit(float64(s) * gain)
	}
}

// limiterKnee is where the soft limiter starts to bend, as a fraction of
// full scale.
const limiterKnee = 0.75

// softLimit maps a sample (int16 units, possibly far past full scale) into
// int16 without hard clipping. Below limiterKnee of full scale it is the
// identity; above, a tanh curve with matching slope at the knee approaches
// full scale asymptotically, so gains over 100% and summed speakers round
// off peaks instead of squaring them.
func softLimit(v float64) int16 {
	const top = math.MaxInt16
	const knee = limiterKnee * top
	a := math.Abs(v)
	if a > knee {
		a = knee + (top-knee)*math.Tanh((a-knee)/(top-knee))
	}
	if v < 0 {
		a = -a
	}
	return int16(math.Round(a))
}

// Meter is a smoothed 0..1 level: it jumps up instantly and decays over
// time. Safe for concurrent use.
type Meter struct {
	mu    sync.Mutex
	level float64
	at    time.Time
}

// Observe records a new RMS reading taken at now.
func (m *Meter) Observe(rms float64, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.levelAtLocked(now)
	if rms > cur {
		cur = rms
	}
	m.level, m.at = cur, now
}

func (m *Meter) levelAtLocked(now time.Time) float64 {
	if m.at.IsZero() {
		return 0
	}
	return Decay(m.level, now.Sub(m.at))
}

// LevelAt is Level evaluated at a given time.
func (m *Meter) LevelAt(now time.Time) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.levelAtLocked(now)
}

// Level returns the current smoothed level in 0..1. A nil Meter reads 0.
func (m *Meter) Level() float64 {
	if m == nil {
		return 0
	}
	return m.LevelAt(time.Now())
}
