package client

import (
	"errors"
	"testing"
)

func TestFailedUnmuteKeepsLocalCaptureMuted(t *testing.T) {
	cl := &call{}
	cl.muted.Store(true)
	c := &Client{call: cl, closed: true}
	if err := c.SetMuted(false); !errors.Is(err, ErrClosed) {
		t.Fatalf("SetMuted = %v, want ErrClosed", err)
	}
	if !cl.muted.Load() {
		t.Fatal("failed unmute enabled local capture")
	}
}

func TestQueuedMuteCannotChangeReplacementCall(t *testing.T) {
	oldCall, newCall := &call{}, &call{}
	c := &Client{call: oldCall, closed: true}
	token := c.CurrentMuteToken()
	c.mu.Lock()
	c.call = newCall // same client, after a leave and rejoin
	c.mu.Unlock()
	if err := c.SetMutedFor(token, true); !errors.Is(err, ErrNotInVoice) {
		t.Fatalf("stale mute = %v, want ErrNotInVoice", err)
	}
	if newCall.muted.Load() {
		t.Fatal("old queued mute changed replacement call's local sender")
	}
}
