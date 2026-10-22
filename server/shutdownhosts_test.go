package server

import (
	"testing"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// A host that is shutting down closes its links and clients in no set order.
// It must not announce a new standby (or none) to clients meanwhile: they would
// drop the warm session they need to fail over.
func TestShutdownDoesNotReannounceStandby(t *testing.T) {
	b := mustID(t)
	c, a := hostChain(t, b)
	s, url := startLinkServer(t, c, a, "pw")
	cl := fullClient(t, url)
	lb := linkUp(t, url, b, proto.LinkHello{Name: "b"})
	fpB := identity.Fingerprint(b.PublicKey())
	hostsUntil(t, cl, func(h proto.Hosts) bool { return h.Standby != nil && h.Standby.Fingerprint == fpB })

	s.shutdown.Store(true)
	lb.Close()
	waitFor(t, "link gone", func() bool { return len(s.Links()) == 0 })
	if st := s.standby.Load(); st == nil || st.Fingerprint != fpB {
		t.Fatalf("standby = %+v after the link closed during shutdown, want it kept", st)
	}
}
