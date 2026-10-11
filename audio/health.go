package audio

import (
	"sort"
	"time"
)

const (
	healthBuckets = 5
	// frameGap is the nominal arrival spacing of 20ms Opus frames.
	frameGap = 20 * time.Millisecond
	// jitterReset: an arrival gap longer than this is a talk-spurt restart or
	// an outage, not jitter, and is excluded so a pause does not spike the
	// estimate.
	jitterReset = time.Second
)

// Health is one sender's recent audio quality.
type Health struct {
	Concealed float64       // fraction of recent slots that were concealed, 0..1
	Jitter    time.Duration // smoothed |arrival gap - 20ms|
	OK        bool          // false when there is no recent data
}

type healthBucket struct{ played, concealed int }

// healthWindow keeps 1-second buckets of played/concealed slot counts for
// the last healthBuckets seconds. It is pure (the caller supplies the time)
// and not synchronised: the owner serialises access (sender.mu).
type healthWindow struct {
	b   [healthBuckets]healthBucket
	sec int64 // second of the newest bucket
	set bool
}

// advance rolls the window forward to now's second, clearing expired buckets.
func (w *healthWindow) advance(now time.Time) {
	s := now.Unix()
	if !w.set {
		w.set, w.sec = true, s
		return
	}
	if s <= w.sec {
		return
	}
	if s-w.sec >= healthBuckets {
		w.b = [healthBuckets]healthBucket{}
	} else {
		for i := w.sec + 1; i <= s; i++ {
			w.b[i%healthBuckets] = healthBucket{}
		}
	}
	w.sec = s
}

func (w *healthWindow) add(now time.Time, played, concealed int) {
	w.advance(now)
	b := &w.b[w.sec%healthBuckets]
	b.played += played
	b.concealed += concealed
}

// ratio returns the concealed fraction over the window; ok is false when no
// slots were recorded.
func (w *healthWindow) ratio(now time.Time) (frac float64, ok bool) {
	w.advance(now)
	var p, c int
	for _, b := range w.b {
		p += b.played
		c += b.concealed
	}
	if p+c == 0 {
		return 0, false
	}
	return float64(c) / float64(p+c), true
}

// jitterEst is the RFC 3550 style estimator j += (|gap-20ms| - j)/16.
type jitterEst struct{ j float64 } // nanoseconds

func (e *jitterEst) update(gap time.Duration) {
	d := gap - frameGap
	if d < 0 {
		d = -d
	}
	e.j += (float64(d) - e.j) / 16
}

// Health reports fp's recent concealment and jitter. Unknown or departed
// senders read as not OK.
func (ms *Meters) Health(fp string) Health {
	if ms == nil {
		return Health{}
	}
	m := ms.m
	m.mu.Lock()
	s := m.senders[fp]
	now := m.now()
	m.mu.Unlock()
	if s == nil {
		return Health{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	frac, ok := s.hw.ratio(now)
	return Health{Concealed: frac, Jitter: time.Duration(s.jit.j), OK: ok}
}

// Senders lists the fingerprints currently being heard, sorted.
func (ms *Meters) Senders() []string {
	if ms == nil {
		return nil
	}
	ms.m.mu.Lock()
	out := make([]string, 0, len(ms.m.senders))
	for fp := range ms.m.senders {
		out = append(out, fp)
	}
	ms.m.mu.Unlock()
	sort.Strings(out)
	return out
}
