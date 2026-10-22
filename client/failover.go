package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

// takeoverKey marks the joinWaiter of a pending Takeover (no real channel has
// this name), so a refusal reaches it and a promotion resolves it.
const takeoverKey = "\x00takeover"

// Warm reports whether this is a warm (paused) session: it receives only
// chain and heartbeat frames until Takeover promotes it.
func (c *Client) Warm() bool { return c.warm.Load() }

// ChainSeq returns the Seq of the head of the client's chain copy.
func (c *Client) ChainSeq() uint64 { return c.chain.Head().Seq }

// Takeover asks the standby this warm client is connected to to become the
// active host. It returns nil once the host has promoted the session, after
// which the client is a full session (Warm is false) and events flow
// normally. A refusal comes back as a *ServerError: active_alive, not_standby
// or host_not_authorized.
func (c *Client) Takeover(ctx context.Context) error {
	if !c.Warm() {
		return nil
	}
	w := &joinWaiter{channel: takeoverKey, res: make(chan error, 1)}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.waiters = append(c.waiters, w)
	c.mu.Unlock()
	if err := c.send(proto.TypeTakeover, struct{}{}); err != nil {
		c.removeWaiter(w)
		return err
	}
	select {
	case err := <-w.res:
		return err
	case <-ctx.Done():
		c.removeWaiter(w)
		return ctx.Err()
	}
}

// ReportReach tells the active host whether this client's warm dial to the
// host with fingerprint fp succeeded, so it can pick a standby clients reach.
func (c *Client) ReportReach(fp string, ok bool) error {
	return c.send(proto.TypeReach, proto.Reach{Fingerprint: fp, OK: ok})
}

// failTakeover fails a pending Takeover with se when se is a code Takeover can
// be answered with; any other error is not its answer.
func (c *Client) failTakeover(se *ServerError) bool {
	switch se.Code {
	case proto.CodeActiveAlive, proto.CodeNotStandby, proto.CodeHostNotAuthorized, proto.CodeBanned:
	default:
		return false
	}
	c.mu.Lock()
	for i, w := range c.waiters {
		if w.channel == takeoverKey {
			c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
			c.mu.Unlock()
			w.res <- se
			return true
		}
	}
	c.mu.Unlock()
	return false
}

// promoted marks the session full: a warm session receives roster and hosts
// frames only when the host has promoted it. It resolves a pending Takeover.
func (c *Client) promoted() {
	if !c.warm.Swap(false) {
		return
	}
	c.mu.Lock()
	var hit []*joinWaiter
	rest := c.waiters[:0]
	for _, w := range c.waiters {
		if w.channel == takeoverKey {
			hit = append(hit, w)
		} else {
			rest = append(rest, w)
		}
	}
	c.waiters = rest
	c.mu.Unlock()
	for _, w := range hit {
		w.res <- nil
	}
}

// onHosts handles a hosts frame.
func (c *Client) onHosts(env proto.Envelope) {
	var h proto.Hosts
	if err := json.Unmarshal(env.Data, &h); err != nil {
		c.emit(Event{Err: fmt.Errorf("client: bad hosts: %w", err)})
		return
	}
	c.promoted()
	c.emit(Event{Hosts: &h})
}

// onMove handles a move frame.
func (c *Client) onMove(env proto.Envelope) {
	var m proto.Move
	if err := json.Unmarshal(env.Data, &m); err != nil {
		c.emit(Event{Err: fmt.Errorf("client: bad move: %w", err)})
		return
	}
	c.emit(Event{Move: &m})
}

// watchdog reports ActiveLost once when no hb has arrived for proto.HeartbeatGrace.
// It stays quiet until the first hb: hosts before v0.7 send none. now is the
// clock, and must be the one stamping c.lastHB.
func (c *Client) watchdog(now func() time.Time) {
	grace := proto.HeartbeatGrace
	t := time.NewTicker(grace / 5)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-c.loopDone:
			return
		case <-t.C:
			last := c.lastHB.Load()
			if last != 0 && now().Sub(time.Unix(0, last)) >= grace {
				c.activeLost(false)
				return
			}
		}
	}
}

// activeLost emits ActiveLost once. block is true on the read loop, which may
// wait for the consumer; the watchdog never blocks.
func (c *Client) activeLost(block bool) {
	if !c.lostFired.CompareAndSwap(false, true) {
		return
	}
	if block {
		c.emit(Event{ActiveLost: true})
	} else {
		c.emitNB(Event{ActiveLost: true})
	}
}

// connLost counts a dropped connection as silence once heartbeats were seen.
func (c *Client) connLost() {
	if c.lastHB.Load() != 0 {
		c.activeLost(true)
	}
}
