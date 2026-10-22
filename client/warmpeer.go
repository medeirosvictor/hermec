package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/proto"
)

// A warm session may keep a data-channel-only PeerConnection open to the
// standby, so ICE and DTLS are already done when the standby takes over.
// AdoptCall then moves a live call onto it with one offer/answer.

const (
	warmChannelLabel = "hermec-warm"
	warmPingEvery    = 5 * time.Second
	warmGatherWait   = 5 * time.Second
)

// adoptTeardownWait is how long AdoptCall waits for the dead call to stop
// before joining anyway. A variable so tests can shorten it.
var adoptTeardownWait = 2 * time.Second

// ErrStillWarm is returned by AdoptCall on a session that has not been
// promoted by Takeover yet.
var ErrStillWarm = errors.New("client: session is still warm; Takeover first")

// warmPeer is the standby-facing PeerConnection of a warm session.
type warmPeer struct {
	pc       *webrtc.PeerConnection
	dc       *webrtc.DataChannel
	answer   chan string   // the SDP of warm_answer; buffered(1)
	stop     chan struct{} // closed when the peer is dropped or adopted
	stopOnce sync.Once
}

func (w *warmPeer) halt() { w.stopOnce.Do(func() { close(w.stop) }) }

// close stops the pings and closes the PeerConnection.
func (w *warmPeer) close() {
	w.halt()
	_ = w.pc.Close()
}

// ready reports whether the data channel is open over a connected transport.
func (w *warmPeer) ready() bool {
	return w.dc.ReadyState() == webrtc.DataChannelStateOpen &&
		w.pc.ConnectionState() == webrtc.PeerConnectionStateConnected
}

// ping keeps the channel (and the NAT binding under it) busy.
func (w *warmPeer) ping() {
	t := time.NewTicker(warmPingEvery)
	defer t.Stop()
	for {
		if w.dc.SendText("ping") != nil {
			return
		}
		select {
		case <-w.stop:
			return
		case <-t.C:
		}
	}
}

// OpenWarmPeer connects a data-channel-only PeerConnection to the standby this
// warm client is attached to. It returns once the standby's answer is applied;
// WarmPeerReady reports when the channel is open. Call SetLoopbackICE first
// in tests.
func (c *Client) OpenWarmPeer(ctx context.Context) error {
	if !c.Warm() {
		return errors.New("client: warm peer needs a warm session")
	}
	pc, err := c.newAPI().NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	dc, err := pc.CreateDataChannel(warmChannelLabel, nil)
	if err != nil {
		_ = pc.Close()
		return err
	}
	w := &warmPeer{pc: pc, dc: dc, answer: make(chan string, 1), stop: make(chan struct{})}
	dc.OnOpen(func() { go w.ping() })
	fail := func(err error) error {
		w.close()
		return err
	}
	offer, err := pc.CreateOffer(nil)
	if err == nil {
		err = pc.SetLocalDescription(offer)
	}
	if err != nil {
		return fail(fmt.Errorf("client: warm offer: %w", err))
	}
	// Our candidates go inside the offer: the standby takes warm_candidate only
	// once it has answered.
	select {
	case <-webrtc.GatheringCompletePromise(pc):
	case <-time.After(warmGatherWait):
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	c.wpMu.Lock()
	old := c.wp
	c.wp = w
	c.wpMu.Unlock()
	if old != nil {
		old.close()
	}
	if err := c.send(proto.TypeWarmOffer, proto.WarmOffer{SDP: pc.LocalDescription().SDP}); err != nil {
		c.dropWarmPeer(w)
		return err
	}
	select {
	case sdp := <-w.answer:
		if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
			c.dropWarmPeer(w)
			return fmt.Errorf("client: warm answer: %w", err)
		}
		return nil
	case <-ctx.Done():
		c.dropWarmPeer(w)
		return ctx.Err()
	case <-c.done:
		c.dropWarmPeer(w)
		return ErrClosed
	}
}

// WarmPeerReady reports whether the warm PeerConnection is connected with its
// data channel open: a takeover can now move a call onto it.
func (c *Client) WarmPeerReady() bool {
	c.wpMu.Lock()
	w := c.wp
	c.wpMu.Unlock()
	return w != nil && w.ready()
}

// dropWarmPeer closes w and forgets it if it is still the current peer.
func (c *Client) dropWarmPeer(w *warmPeer) {
	c.wpMu.Lock()
	if c.wp == w {
		c.wp = nil
	}
	c.wpMu.Unlock()
	w.close()
}

// closeWarmPeer closes the current warm PeerConnection, if any.
func (c *Client) closeWarmPeer() {
	c.wpMu.Lock()
	w := c.wp
	c.wp = nil
	c.wpMu.Unlock()
	if w != nil {
		w.close()
	}
}

// takeReadyWarmPeer hands the warm peer to the caller (which owns it from
// then on) if it is ready; a peer that is not ready is closed. Nil if none.
func (c *Client) takeReadyWarmPeer() *warmPeer {
	c.wpMu.Lock()
	w := c.wp
	c.wp = nil
	c.wpMu.Unlock()
	if w == nil {
		return nil
	}
	if !w.ready() {
		w.close()
		return nil
	}
	w.halt() // no more pings: the call's media owns the PeerConnection now
	return w
}

// onWarmAnswer routes warm_answer to the pending OpenWarmPeer.
func (c *Client) onWarmAnswer(env proto.Envelope) {
	var m proto.WarmAnswer
	if json.Unmarshal(env.Data, &m) != nil {
		return
	}
	c.wpMu.Lock()
	w := c.wp
	c.wpMu.Unlock()
	if w == nil {
		return
	}
	select {
	case w.answer <- m.SDP:
	default:
	}
}

// AdoptCall moves the live call of from, the dead session this one replaces,
// onto this promoted session: same channel, mute state, source, sink and video
// wants. With a ready warm peer the call's media rides on that
// already-connected PeerConnection after one offer/answer; otherwise it joins
// from scratch. It returns once audio can flow. from's call stops using the
// source and sink and its PeerConnection is closed.
func (c *Client) AdoptCall(ctx context.Context, from *Client) error {
	if c.Warm() {
		return ErrStillWarm
	}
	// Every check that can fail comes before from's call is touched.
	if c.getCall() != nil {
		return ErrInVoice
	}
	if from.getCall() == nil {
		return ErrNotInVoice
	}
	var pc *webrtc.PeerConnection
	if w := c.takeReadyWarmPeer(); w != nil {
		pc = w.pc
	}
	from.mu.Lock()
	old := from.call
	from.call = nil
	from.mu.Unlock()
	if old == nil { // raced with a leave
		if pc != nil {
			_ = pc.Close()
		}
		return ErrNotInVoice
	}
	_ = from.send(proto.TypeVoiceLeave, proto.VoiceLeave{}) // fails on the dead connection
	tctx, cancel := context.WithTimeout(ctx, adoptTeardownWait)
	old.teardownCtx(tctx)
	cancel()
	vid := false
	select {
	case <-old.tdDone:
		vid = old.vidWanted // the actor has exited, so its state is ours to read
	default: // a stuck teardown must not cost the user the call: join anyway
	}
	return c.joinCall(ctx, old.channel, old.src, old.sink, old.muted.Load(), vid, pc)
}
