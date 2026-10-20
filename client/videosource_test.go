package client

import (
	"testing"

	"github.com/medeirosvictor/hermec/core/proto"
)

// Watch(A); Watch(B); Watch(A) in quick succession: the server answers each, in
// order, and the first A answer (older gen) must not be taken for the last one.
func TestVideoSourceStaleGenIgnored(t *testing.T) {
	c := &Client{}
	c.watchMu.Lock()
	c.watchFP, c.watchWire = "A", 3 // third Watch call
	c.watchMu.Unlock()
	c.onVideoSource(proto.VideoSource{Fingerprint: "A", Seq: 10, Gen: 1})
	if w := c.watchSnapshot(); w.have {
		t.Fatal("video_source from an older watch of the same sharer was accepted")
	}
	c.onVideoSource(proto.VideoSource{Fingerprint: "B", Seq: 20, Gen: 2})
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

// A refused Watch (not_sharing) goes back to the source the server still sends.
func TestWatchRefusedRestoresPrevious(t *testing.T) {
	c := &Client{}
	c.watchFP, c.watchWire, c.srcHave, c.srcSeq = "A", 1, true, 42
	// Watch's state change, minus the network send.
	c.watchMu.Lock()
	prev := c.watchStateLocked()
	c.prevWatch = &prev
	c.watchFP, c.watchWire, c.srcHave = "B", 2, false
	c.watchGen++
	c.watchMu.Unlock()
	before := c.watchSnapshot()
	c.watchRefused()
	w := c.watchSnapshot()
	if w.fp != "A" || !w.have || w.seq != 42 || w.wire != 1 {
		t.Fatalf("not restored: %+v", w)
	}
	if w.gen == before.gen || w.id == before.id {
		t.Fatal("reader not told to restart on the restored boundary")
	}
	c.watchRefused() // a second call is a no-op
	if c.watchSnapshot().fp != "A" {
		t.Fatal("double restore changed state")
	}
}
