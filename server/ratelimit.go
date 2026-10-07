package server

import "time"

// Default inbound message limits. Post-auth limits are configurable
// (Config.MsgRate / Config.MsgBurst); the pre-auth bucket is fixed and only
// has to cover the auth exchange.
const (
	DefaultMsgRate  = 30
	DefaultMsgBurst = 60

	preAuthMsgRate  = 5
	preAuthMsgBurst = 10
)

// bucket is a token bucket. It is not safe for concurrent use; each
// connection's readLoop owns its buckets. Time is passed in so callers can
// inject a clock.
type bucket struct {
	rate     float64 // tokens per second; <= 0 disables limiting
	capacity float64
	tokens   float64
	last     time.Time
}

// newBucket returns a full bucket refilling at rate tokens/sec up to burst.
// A rate or burst of zero or less yields a bucket that always allows.
func newBucket(rate, burst int, now time.Time) *bucket {
	return &bucket{
		rate:     float64(rate),
		capacity: float64(burst),
		tokens:   float64(burst),
		last:     now,
	}
}

// allow spends one token, refilling first from the time elapsed since the
// last call. It reports whether the message is within the limit.
func (b *bucket) allow(now time.Time) bool {
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
