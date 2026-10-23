package server

import (
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

// A standby that dies without closing its TCP connection stops sending hb;
// the active host drops its link within the heartbeat grace and picks again.
func TestSilentLinkIsDropped(t *testing.T) {
	t.Cleanup(SetHeartbeatForTest(50*time.Millisecond, 150*time.Millisecond))
	c, a, b := twoHostChain(t)
	s, url := startLinkServer(t, c, a, "pw")
	lk := linkUp(t, url, b, proto.LinkHello{Name: "b"}) // stays open, never sends hb
	waitFor(t, "link", func() bool { return len(s.Links()) == 1 })
	if s.standby.Load() == nil {
		t.Fatal("the link is not the standby")
	}
	start := time.Now()
	waitFor(t, "silent link dropped", func() bool { return len(s.Links()) == 0 })
	if d := time.Since(start); d > 3*heartbeatGrace {
		t.Fatalf("silent link dropped after %v", d)
	}
	if s.standby.Load() != nil {
		t.Fatal("standby still names the dropped link")
	}
	_ = lk.Close()
}

// A link that keeps sending hb is kept.
func TestBeatingLinkIsKept(t *testing.T) {
	t.Cleanup(SetHeartbeatForTest(50*time.Millisecond, 150*time.Millisecond))
	c, a, b := twoHostChain(t)
	s, url := startLinkServer(t, c, a, "pw")
	lk := linkUp(t, url, b, proto.LinkHello{Name: "b"})
	defer lk.Close()
	waitFor(t, "link", func() bool { return len(s.Links()) == 1 })
	end := time.Now().Add(3 * heartbeatGrace)
	for time.Now().Before(end) {
		sendEnv(t, lk, proto.TypeHeartbeat, proto.Heartbeat{})
		time.Sleep(40 * time.Millisecond)
	}
	if len(s.Links()) != 1 {
		t.Fatal("a beating link was dropped")
	}
}
