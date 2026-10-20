package client

import (
	"errors"
	"testing"

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
	c.watchFP, c.watchWire, c.srcHave, c.srcSeq = fp, wire, true, seq
	c.watchMu.Unlock()
}

// The server refuses a Watch with not_sharing: the pending Watch gets the
// error and the client is back on the source the server still sends.
func TestWatchRefusedRestoresPrevious(t *testing.T) {
	c := &Client{}
	watching(c, "A", 1, 42)
	before := c.watchSnapshot()
	wire, wait := c.beginWatch("B")
	if wire != 2 || wait == nil {
		t.Fatalf("beginWatch = %d, %v", wire, wait)
	}
	c.onError(proto.ErrorMsg{Code: proto.CodeNotSharing, Message: "nope"})
	var se *ServerError
	if err := <-wait; !errors.As(err, &se) || se.Code != proto.CodeNotSharing {
		t.Fatalf("pending watch got %v, want not_sharing", err)
	}
	w := c.watchSnapshot()
	if w.fp != "A" || !w.have || w.seq != 42 || w.wire != 1 {
		t.Fatalf("not restored: %+v", w)
	}
	if w.gen == before.gen || w.id == before.id {
		t.Fatal("reader not told to restart on the restored boundary")
	}
	c.onError(proto.ErrorMsg{Code: proto.CodeNotSharing}) // late duplicate: nothing pending
	if c.watchSnapshot().fp != "A" {
		t.Fatal("an error with no watch pending changed the target")
	}
}

// Two Watch calls back to back where the first is refused end on the second
// target; the first refusal cannot touch the second watch.
func TestBackToBackWatchFirstRefused(t *testing.T) {
	c := &Client{}
	watching(c, "A", 1, 42)
	_, w1 := c.beginWatch("B")
	c.onError(proto.ErrorMsg{Code: proto.CodeNotSharing})
	if err := <-w1; err == nil {
		t.Fatal("first watch not refused")
	}
	wire, w2 := c.beginWatch("C")
	// A stale announcement for B (older gen) and the real one for C.
	c.onVideoSource(proto.VideoSource{Fingerprint: "B", Seq: 7, Gen: 2})
	c.onVideoSource(proto.VideoSource{Fingerprint: "C", Seq: 900, Gen: wire})
	if err := <-w2; err != nil {
		t.Fatalf("second watch: %v", err)
	}
	if w := c.watchSnapshot(); w.fp != "C" || !w.have || w.seq != 900 {
		t.Fatalf("ended on %+v, want C at 900", w)
	}
}
