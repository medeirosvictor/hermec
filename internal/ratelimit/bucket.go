package ratelimit

import "time"

// Bucket is a token bucket. It is not safe for concurrent use; each
// connection's readLoop owns its buckets. Time is passed in so callers can
// inject a clock.
type Bucket struct {
	rate     float64 // tokens per second; <= 0 disables limiting
	capacity float64
	tokens   float64
	last     time.Time
}

// NewBucket returns a full bucket refilling at rate tokens/sec up to burst.
// A rate or burst of zero or less yields a bucket that always allows.
func NewBucket(rate, burst int, now time.Time) *Bucket {
	return &Bucket{
		rate:     float64(rate),
		capacity: float64(burst),
		tokens:   float64(burst),
		last:     now,
	}
}

// Allow spends one token, refilling first from the time elapsed since the
// last call. It reports whether the message is within the limit.
func (b *Bucket) Allow(now time.Time) bool {
	if b.rate <= 0 || b.capacity <= 0 {
		return true
	}
	if el := now.Sub(b.last); el > 0 {
		b.tokens += el.Seconds() * b.rate
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
