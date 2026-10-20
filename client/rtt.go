package client

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	pingInterval = 2 * time.Second
	pingWrite    = 1 * time.Second
	pongTimeout  = 6 * time.Second
	ewmaAlpha    = 0.2
)

// rttEWMA is an exponentially weighted moving average of RTT samples.
type rttEWMA struct {
	v  time.Duration
	ok bool
}

func (e *rttEWMA) observe(sample time.Duration) {
	if !e.ok {
		e.v, e.ok = sample, true
		return
	}
	e.v = time.Duration((1-ewmaAlpha)*float64(e.v) + ewmaAlpha*float64(sample))
}

// rttTracker holds the smoothed RTT and the oldest unanswered ping. It is safe
// for concurrent use (pinger, read goroutine, GUI).
type rttTracker struct {
	mu          sync.Mutex
	ewma        rttEWMA
	outstanding time.Time // send time of the oldest unanswered ping; zero if none
	timedOut    time.Time // send time of the newest ping already counted as timed out
}

func newRTTTracker() *rttTracker { return &rttTracker{} }

// sent records a ping sent at t and returns its payload.
func (r *rttTracker) sent(t time.Time) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(t.UnixNano()))
	r.mu.Lock()
	if r.outstanding.IsZero() {
		r.outstanding = t
	}
	r.mu.Unlock()
	return string(b[:])
}

// pong handles a pong payload received at now. Payloads that are not one of
// our 8-byte timestamps (e.g. answers to someone else's pings) are ignored.
func (r *rttTracker) pong(payload string, now time.Time) {
	if len(payload) != 8 {
		return
	}
	sent := time.Unix(0, int64(binary.BigEndian.Uint64([]byte(payload))))
	d := now.Sub(sent)
	if d < 0 {
		return
	}
	r.mu.Lock()
	if !sent.After(r.timedOut) { // already counted as a timeout sample
		r.mu.Unlock()
		return
	}
	r.ewma.observe(d)
	if !r.outstanding.IsZero() && !sent.Before(r.outstanding) {
		r.outstanding = time.Time{}
	}
	r.mu.Unlock()
}

// check records a timeout sample if the oldest ping has gone unanswered for
// at least timeout.
func (r *rttTracker) check(now time.Time, timeout time.Duration) {
	r.mu.Lock()
	if !r.outstanding.IsZero() && now.Sub(r.outstanding) >= timeout {
		r.ewma.observe(timeout)
		r.timedOut = r.outstanding
		r.outstanding = time.Time{}
	}
	r.mu.Unlock()
}

func (r *rttTracker) get() (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ewma.v, r.ewma.ok
}

// RTT returns the smoothed round-trip time to the server. ok is false until
// the first pong arrives. It is cheap and never blocks on I/O.
func (c *Client) RTT() (rtt time.Duration, ok bool) { return c.rtt.get() }

// pingOnce samples a timeout and sends one ping. The timeout check runs before
// (and independent of) writeMu, so a data write stalled for up to writeTimeout
// cannot delay it. The ping itself is stamped while holding writeMu, so time
// spent queued behind a slow write is not counted as network latency; that
// wait only delays this ping, not the next timeout sample.
func (c *Client) pingOnce(timeout time.Duration, now func() time.Time) {
	c.rtt.check(now(), timeout)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	t := now()
	payload := c.rtt.sent(t)
	_ = c.ws.WriteControl(websocket.PingMessage, []byte(payload), t.Add(pingWrite))
}

// startPinger sends a ping immediately and then every interval until the
// client closes or the read loop exits. Pongs are handled by the pong handler
// installed in Dial (on the read goroutine). The server's own pings are still
// answered by gorilla's default ping handler.
func (c *Client) startPinger(interval, timeout time.Duration, now func() time.Time) {
	go func() {
		defer close(c.pingDone)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			c.pingOnce(timeout, now)
			select {
			case <-c.done:
				return
			case <-c.loopDone:
				return
			case <-tick.C:
			}
		}
	}()
}
