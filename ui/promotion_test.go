package ui

import (
	"context"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

// Another client's takeover promotes the standby before this session's own
// watchdog fires: the session still ends on the standby with its call, never
// on the reconnect screen.
func TestSessionFollowsPromotionByOtherClient(t *testing.T) {
	g, p, s := pairGame(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.c.JoinVoice(ctx, "voice", nopSource{}, nopSink{}); err != nil {
		t.Fatalf("JoinVoice: %v", err)
	}
	d := &duplexNop{}
	g.vc = voiceCall{start: time.Now(), src: d, sink: d}
	g.callKey = s.key
	s.st.SetInCall("voice")
	warm := s.warm
	other, err := client.DialWith(ctx, p.bURL, mustIdentity(t), "other", "", client.DialOpts{Warm: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	p.killA(t)
	for { // the other client wins the race: our session is not pumped meanwhile
		if other.Takeover(ctx) == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for i := 0; warm.Warm(); i++ {
		if i > 500 {
			t.Fatal("our warm session never saw the promotion")
		}
		time.Sleep(10 * time.Millisecond)
	}
	g.drainWarm(s) // runs before drainEvents in a tick
	pumpUntil(t, g, func() bool {
		if s.failed || s.st.Phase == state.PhaseDisconnected {
			t.Fatalf("reconnect screen shown: failed=%v phase=%v", s.failed, s.st.Phase)
		}
		return s.c == warm && !s.switching
	})
	if g.callKey != s.key || s.st.InCall != "voice" {
		t.Fatalf("call lost: callKey %q InCall %q", g.callKey, s.st.InCall)
	}
}

// The founder's hosted session takes no chain floor for its own host, but a
// friend host it is sent to must not be behind the chain the founder hosts.
func TestHostedDialRefusesStaleFriendHost(t *testing.T) {
	g := newTestGame(t)
	fk := mustIdentity(t)
	ge, err := chain.NewEntry(fk, nil, chain.OpGenesis, chain.GenesisBody{
		Founder: []byte(fk.PublicKey()), Salt: []byte("0123456789abcdef"),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	short, err := chain.New(ge)
	if err != nil {
		t.Fatal(err)
	}
	xk, yk := mustIdentity(t), mustIdentity(t)
	add := func(c *chain.Chain, k *identity.Identity, prio int) {
		head := c.Head()
		e, err := chain.NewEntry(fk, &head, chain.OpHostAdd, chain.HostAddBody{PubKey: []byte(k.PublicKey()), Priority: prio}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	add(short, xk, 1)
	add(short, yk, 2)
	long, err := chain.FromEntries(short.Entries())
	if err != nil {
		t.Fatal(err)
	}
	add(long, mustIdentity(t), 3)
	_, staleURL := startServerChain(t, "127.0.0.1:0", "stale", xk, short)
	sb := server.New(server.Config{
		HostKey: yk, Chain: short, Addr: "127.0.0.1:0", Mode: server.ModeStandby,
		Channels: []string{"general"}, Roles: roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
	})
	if err := sb.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Shutdown(context.Background()) })
	sb.SetHosts(proto.Hosts{Active: proto.HostInfo{Addrs: []string{staleURL}}})
	g.hosted = &hostedServer{chain: long, id: short.ID().String()}
	s := &session{key: hostedKey}
	_, o := g.dialCreds(s)
	if o.MinChainSeq != 0 {
		t.Fatalf("local floor = %d, want 0", o.MinChainSeq)
	}
	if c, err := g.dialAny([]string{staleURL}, "me", "", o); err != nil {
		t.Fatalf("local dial refused: %v", err)
	} else {
		_ = c.Close()
	}
	c, err := g.dialAny([]string{"ws://" + sb.Addr() + "/"}, "me", "", o)
	if err == nil {
		_ = c.Close()
		t.Fatal("followed a not_active to a host behind the hosted chain")
	}
}
