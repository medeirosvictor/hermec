package client

import (
	"errors"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

// A screen source of an older subscription gen, another publisher, or no gen
// at all is not taken for the current subscription.
func TestScreenSourceStaleGenIgnored(t *testing.T) {
	c := &Client{}
	c.watchMu.Lock()
	c.watchFP, c.watchWire = "A", 3
	c.watchMu.Unlock()
	c.onScreenSource("A", 10, 1)
	if w := c.watchSnapshot(); w.have {
		t.Fatal("source from an older subscription of the same publisher was accepted")
	}
	c.onScreenSource("B", 20, 3)
	if w := c.watchSnapshot(); w.have {
		t.Fatal("source for another publisher was accepted")
	}
	c.onScreenSource("A", 5, 0)
	if w := c.watchSnapshot(); w.have {
		t.Fatal("gen-less source was accepted")
	}
	c.onScreenSource("A", 30, 3)
	if w := c.watchSnapshot(); !w.have || w.seq != 30 {
		t.Fatalf("current source not recorded: %+v", w)
	}
}

func subscribed(c *Client, fp string, wire uint32, seq uint16) {
	c.watchMu.Lock()
	c.watchFP, c.watchWire, c.wireCtr, c.srcHave, c.srcSeq = fp, wire, wire, true, seq
	c.watchMu.Unlock()
}

func refuse(c *Client, gen uint32) {
	c.onError(proto.ErrorMsg{Code: proto.CodeNotPublishing, Message: "nope", Gen: gen})
}

// The server refuses a subscription with not_publishing: the pending call gets
// the error and the client is back on the source the server still sends. Gens
// stay unique: the next subscription does not reuse the refused one.
func TestSubscriptionRefusedRestoresPrevious(t *testing.T) {
	c := &Client{}
	subscribed(c, "A", 1, 42)
	before := c.watchSnapshot()
	wire, wait := c.beginWatch("B", false)
	if wire != 2 || wait == nil {
		t.Fatalf("beginWatch = %d, %v", wire, wait)
	}
	refuse(c, wire)
	var se *ServerError
	if err := <-wait; !errors.As(err, &se) || se.Code != proto.CodeNotPublishing || se.Gen != wire {
		t.Fatalf("pending subscription got %v, want not_publishing gen %d", err, wire)
	}
	w := c.watchSnapshot()
	if w.fp != "A" || !w.have || w.seq != 42 || w.wire != 1 {
		t.Fatalf("not restored: %+v", w)
	}
	if w.gen == before.gen || w.id == before.id {
		t.Fatal("reader not told to restart on the restored boundary")
	}
	if next, _ := c.beginWatch("C", false); next != 3 {
		t.Fatalf("next gen = %d, want 3 (no reuse of the refused 2)", next)
	}
}

// An error with no gen, or one for another request, never touches the pending
// subscription.
func TestSubscriptionErrorWithoutMatchingGenIgnored(t *testing.T) {
	c := &Client{}
	subscribed(c, "A", 1, 42)
	wire, wait := c.beginWatch("B", false)
	refuse(c, 0)
	refuse(c, wire-1)
	refuse(c, wire+5)
	select {
	case err := <-wait:
		t.Fatalf("pending subscription completed by a foreign error: %v", err)
	default:
	}
	if w := c.watchSnapshot(); w.fp != "B" {
		t.Fatalf("target changed to %q by a foreign error", w.fp)
	}
}

// A late refusal for subscription N (after N timed out) arriving while N+1 is
// pending is ignored, and N+1 completes normally.
func TestLateRefusalIgnoredWhileNextPending(t *testing.T) {
	c := &Client{}
	subscribed(c, "A", 1, 42)
	n, waitN := c.beginWatch("B", false)
	c.endWatch(waitN) // N timed out; the target B is kept
	n1, waitN1 := c.beginWatch("C", false)
	refuse(c, n) // N's late answer
	select {
	case err := <-waitN1:
		t.Fatalf("subscription N+1 completed by N's refusal: %v", err)
	default:
	}
	if w := c.watchSnapshot(); w.fp != "C" {
		t.Fatalf("target = %q, want C", w.fp)
	}
	c.onScreenSource("C", 900, n1)
	if err := <-waitN1; err != nil {
		t.Fatalf("N+1: %v", err)
	}
	if w := c.watchSnapshot(); w.fp != "C" || !w.have || w.seq != 900 {
		t.Fatalf("ended on %+v, want C at 900", w)
	}
}

// Two subscriptions back to back where the first is refused end on the second
// target.
func TestBackToBackSubscriptionFirstRefused(t *testing.T) {
	c := &Client{}
	subscribed(c, "A", 1, 42)
	w1g, w1 := c.beginWatch("B", false)
	refuse(c, w1g)
	if err := <-w1; err == nil {
		t.Fatal("first subscription not refused")
	}
	wire, w2 := c.beginWatch("C", false)
	c.onScreenSource("B", 7, w1g)
	c.onScreenSource("C", 900, wire)
	if err := <-w2; err != nil {
		t.Fatalf("second subscription: %v", err)
	}
	if w := c.watchSnapshot(); w.fp != "C" || !w.have || w.seq != 900 {
		t.Fatalf("ended on %+v, want C at 900", w)
	}
}

// A gated reader that never sees a keyframe asks for one at most about once a
// second, however many packets arrive.
func TestPLILimiterSpacing(t *testing.T) {
	var l pliLimiter
	t0 := time.Unix(1000, 0)
	sent := 0
	for i := 0; i < 5000; i++ { // 5 s of 1000 packets/s
		if l.allow(t0.Add(time.Duration(i)*time.Millisecond), false) {
			sent++
		}
	}
	if sent < 4 || sent > 6 {
		t.Fatalf("%d PLIs in 5 s, want about 5", sent)
	}
	if !l.allow(t0.Add(5001*time.Millisecond), true) {
		t.Fatal("forced PLI refused")
	}
}
