package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/hshake"
	"github.com/medeirosvictor/hermec/core/proto"
)

func badChain(format string, args ...any) *ServerError { return hshake.BadChain(format, args...) }

// onChain applies a live chain frame to the local chain, resolves the
// ChainAppend calls whose entry it carries and emits an Event per entry. It
// reports false when the frame does not verify: the host has then broken its
// chain, bad_chain is emitted as the final event and the read loop must stop.
func (c *Client) onChain(env proto.Envelope) bool {
	var cf proto.ChainFrame
	if err := json.Unmarshal(env.Data, &cf); err != nil {
		c.emit(Event{Err: badChain("malformed chain frame")})
		return false
	}
	es, err := hshake.DecodeEntries(cf.Entries)
	if err != nil {
		c.emit(Event{Err: err})
		return false
	}
	for _, e := range es {
		if err := c.chain.Append(e); err != nil {
			c.emit(Event{Err: badChain("live entry %d rejected: %v", e.Seq, err)})
			return false
		}
		c.resolveChain(e.Sig)
		e := e
		c.emit(Event{Chain: &e})
	}
	return true
}

func (c *Client) resolveChain(sig []byte) {
	c.mu.Lock()
	var hit []*joinWaiter
	rest := c.waiters[:0]
	for _, w := range c.waiters {
		if w.sig != nil && bytes.Equal(w.sig, sig) {
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

// chainRetryBudget bounds how long ChainAppend keeps rebuilding on a newer
// head after chain_conflict. A count does not work: the loser hears of its
// conflict one frame after the winner hears of its success, so an admin
// appending back to back can win every race until it stops.
const chainRetryBudget = 5 * time.Second

// ChainAppend signs an entry on the local head with the client's identity,
// sends it and returns once the server has broadcast it back (so Chain()
// already holds it) or refused it with a *ServerError. If another entry won
// the head (chain_conflict) it waits for the local chain to catch up and
// rebuilds the entry, for up to chainRetryBudget.
func (c *Client) ChainAppend(ctx context.Context, op chain.Op, body any) error {
	c.chainCallMu.Lock()
	defer c.chainCallMu.Unlock()
	giveUp := time.Now().Add(chainRetryBudget)
	for {
		behind := c.chain.Head().Seq
		err := c.chainAppendOnce(ctx, op, body)
		var se *ServerError
		if !errors.As(err, &se) || se.Code != proto.CodeChainConflict || time.Now().After(giveUp) {
			return err
		}
		// The winning entry normally reached us before the refusal; wait for
		// it if it is still in flight.
		limit := time.NewTimer(2 * time.Second)
		for c.chain.Head().Seq == behind {
			select {
			case <-time.After(10 * time.Millisecond):
			case <-limit.C:
				return err
			case <-ctx.Done():
				limit.Stop()
				return ctx.Err()
			case <-c.loopDone:
				limit.Stop()
				return ErrClosed
			}
		}
		limit.Stop()
	}
}

func (c *Client) chainAppendOnce(ctx context.Context, op chain.Op, body any) error {
	head := c.chain.Head()
	e, err := chain.NewEntry(c.id, &head, op, body, time.Now())
	if err != nil {
		return err
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	var rb [8]byte
	if _, err := rand.Read(rb[:]); err != nil {
		return err
	}
	ref := hex.EncodeToString(rb[:])
	w := &joinWaiter{sig: e.Sig, ref: ref, res: make(chan error, 1)}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.waiters = append(c.waiters, w)
	c.mu.Unlock()
	if err := c.send(proto.TypeChainAppend, proto.ChainAppend{Entry: raw, Ref: ref}); err != nil {
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
