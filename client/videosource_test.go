package client

import (
	"errors"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

// A video_source of an older watch gen for the same sharer is not taken for the
// current one.
func TestVideoSourceStaleGenIgnored(t *testing.T) {
	c := &Client{}
	c.watchMu.Lock()
	c.watchFP, c.watchWire = "A", 3
	c.watchMu.Unlock()
	c.onVideoSource(proto.VideoSource{Fingerprint: "A", Seq: 10, Gen: 1})
	if w := c.watchSnapshot(); w.have {
		t.Fatal("video_source from an older watch of the same sharer was accepted")
	}
	c.onVideoSource(proto.VideoSource{Fingerprint: "B", Seq: 20, Gen: 3})
	if w := c.watchSnapshot(); w.have {
		t.Fatal("video_source for another sharer was accepted")
	}
	c.onVideoSource(proto.VideoSource{Fingerprint: "A", Seq: 30, Gen: 3})
	if w := c.watchSnapshot(); !w.have || w.seq != 30 {
		t.Fatalf("current video_source not recorded: %+v", w)
	}
	c.onVideoSource(proto.VideoSource{Fingerprint: "A", Seq: 5}) // gen-less server
	if w := c.watchSnapshot(); w.seq != 5 {
		t.Fatalf("gen-less video_source rejected: %+v", w)
	}
}

func watching(c *Client, fp string, wire uint32, seq uint16) {
	c.watchMu.Lock()
	c.watchFP, c.watchWire, c.wireCtr, c.srcHave, c.srcSeq = fp, wire, wire, true, seq
	c.watchMu.Unlock()
}

func refuse(c *Client, gen uint32) {
	c.onError(proto.ErrorMsg{Code: proto.CodeNotSharing, Message: "nope", Gen: gen})
}

// The server refuses a Watch with not_sharing: the pending Watch gets the
// error and the client is back on the source the server still sends. Gens stay
// unique: the next Watch does not reuse the refused one.
func TestWatchRefusedRestoresPrevious(t *testing.T) {
	c := &Client{}
	watching(c, "A", 1, 42)
	before := c.watchSnapshot()
	wire, wait := c.beginWatch("B")
	if wire != 2 || wait == nil {
		t.Fatalf("beginWatch = %d, %v", wire, wait)
	}
	refuse(c, wire)
	var se *ServerError
	if err := <-wait; !errors.As(err, &se) || se.Code != proto.CodeNotSharing || se.Gen != wire {
		t.Fatalf("pending watch got %v, want not_sharing gen %d", err, wire)
	}
	w := c.watchSnapshot()
	if w.fp != "A" || !w.have || w.seq != 42 || w.wire != 1 {
		t.Fatalf("not restored: %+v", w)
	}
	if w.gen == before.gen || w.id == before.id {
		t.Fatal("reader not told to restart on the restored boundary")
	}
	if next, _ := c.beginWatch("C"); next != 3 {
		t.Fatalf("next gen = %d, want 3 (no reuse of the refused 2)", next)
	}
}

// An error with no gen, or one for another request, never touches the pending
// watch.
func TestWatchErrorWithoutMatchingGenIgnored(t *testing.T) {
	c := &Client{}
	watching(c, "A", 1, 42)
	wire, wait := c.beginWatch("B")
	refuse(c, 0)
	refuse(c, wire-1)
	refuse(c, wire+5)
	select {
	case err := <-wait:
		t.Fatalf("pending watch completed by a foreign error: %v", err)
	default:
	}
	if w := c.watchSnapshot(); w.fp != "B" {
		t.Fatalf("target changed to %q by a foreign error", w.fp)
	}
}

// A late not_sharing for watch N (after N timed out) arriving while watch N+1
// is pending is ignored, and N+1 completes normally.
func TestLateRefusalIgnoredWhileNextPending(t *testing.T) {
	c := &Client{}
	watching(c, "A", 1, 42)
	n, waitN := c.beginWatch("B")
	c.endWatch(waitN) // Watch N timed out; the target B is kept
	n1, waitN1 := c.beginWatch("C")
	refuse(c, n) // N's late answer
	select {
	case err := <-waitN1:
		t.Fatalf("watch N+1 completed by N's refusal: %v", err)
	default:
	}
	if w := c.watchSnapshot(); w.fp != "C" {
		t.Fatalf("target = %q, want C", w.fp)
	}
	c.onVideoSource(proto.VideoSource{Fingerprint: "C", Seq: 900, Gen: n1})
	if err := <-waitN1; err != nil {
		t.Fatalf("N+1: %v", err)
	}
	if w := c.watchSnapshot(); w.fp != "C" || !w.have || w.seq != 900 {
		t.Fatalf("ended on %+v, want C at 900", w)
	}
}

// Two Watch calls back to back where the first is refused end on the second
// target.
func TestBackToBackWatchFirstRefused(t *testing.T) {
	c := &Client{}
	watching(c, "A", 1, 42)
	w1g, w1 := c.beginWatch("B")
	refuse(c, w1g)
	if err := <-w1; err == nil {
		t.Fatal("first watch not refused")
	}
	wire, w2 := c.beginWatch("C")
	c.onVideoSource(proto.VideoSource{Fingerprint: "B", Seq: 7, Gen: w1g})
	c.onVideoSource(proto.VideoSource{Fingerprint: "C", Seq: 900, Gen: wire})
	if err := <-w2; err != nil {
		t.Fatalf("second watch: %v", err)
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
