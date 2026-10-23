package server

import (
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
)

// extend appends one founder-signed entry to c.
func extend(t *testing.T, c *chain.Chain, founder *identity.Identity, op chain.Op, body any) {
	t.Helper()
	head := c.Head()
	e, err := chain.NewEntry(founder, &head, op, body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Append(e); err != nil {
		t.Fatal(err)
	}
}

func extraHost(t *testing.T, c *chain.Chain, founder *identity.Identity) {
	t.Helper()
	extend(t, c, founder, chain.OpHostAdd, chain.HostAddBody{PubKey: []byte(mustID(t).PublicKey()), Priority: 9})
}

// An active host serving a prefix of our chain is behind us: we do not link
// to it, we start as active ourselves.
func TestColdStartIgnoresActiveHostBehindOurChain(t *testing.T) {
	fastHostTimers(t)
	base, a, b, _ := threeHostChain(t)
	ha := newTestHost(t, base, a, "alpha")
	ha.start(t)
	waitRole(t, ha, RoleActive, time.Second)
	ahead, err := chain.FromEntries(base.Entries())
	if err != nil {
		t.Fatal(err)
	}
	extraHost(t, ahead, a)
	hb := newTestHost(t, ahead, b, "beta", ha.url)
	hb.start(t)
	waitRole(t, hb, RoleActive, 4*coldStartWindow+time.Second)
	if hb.hadLink {
		t.Fatal("linked to a host behind our chain")
	}
	if n := len(ha.Server().Links()); n != 0 {
		t.Fatalf("stale host has %d links", n)
	}
}

// A host removed on our chain is not followed even when its chain is as long
// as ours.
func TestColdStartIgnoresRemovedHost(t *testing.T) {
	fastHostTimers(t)
	base, a, b, d := threeHostChain(t)
	hd := newTestHost(t, base, d, "delta")
	extraHost(t, hd.Server().chain, a) // d's chain: base + one entry
	hd.start(t)
	waitRole(t, hd, RoleActive, time.Second)
	mine, err := chain.FromEntries(base.Entries())
	if err != nil {
		t.Fatal(err)
	}
	extend(t, mine, a, chain.OpHostRemove, chain.FPBody{Fingerprint: identity.Fingerprint(d.PublicKey())})
	hb := newTestHost(t, mine, b, "beta", hd.url)
	hb.start(t)
	waitRole(t, hb, RoleActive, 4*coldStartWindow+time.Second)
	if hb.hadLink {
		t.Fatal("linked to a removed host")
	}
	if n := len(hd.Server().Links()); n != 0 {
		t.Fatalf("removed host has %d links", n)
	}
}
