package server

import (
	"context"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

// linkPair starts host A (active, founder, password pw) and host B (standby,
// its own copy of the chain) and returns them with their URLs and keys.
func linkPair(t *testing.T, pw string) (a, b *Server, aURL, bURL string, ak, bk *identity.Identity, ac *chain.Chain) {
	t.Helper()
	ac, ak, bk = twoHostChain(t)
	a, aURL = startLinkServer(t, ac, ak, pw)
	bc, err := chain.FromEntries(ac.Entries())
	if err != nil {
		t.Fatal(err)
	}
	b, bURL = startModeServer(t, Config{
		HostKey: bk, Chain: bc, Mode: ModeStandby, HostName: "beta",
		Advertise: []string{"ws://b.example:2"},
		Roles:     roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels:  []string{"general"},
	})
	return
}

func dialLink(t *testing.T, aURL string, b *Server) *Link {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, err := DialLink(ctx, aURL, b)
	if err != nil {
		t.Fatalf("DialLink: %v", err)
	}
	t.Cleanup(l.Close)
	go l.Run()
	return l
}

func appendAs(t *testing.T, url string, author *identity.Identity, ac *chain.Chain, op chain.Op, body any) {
	t.Helper()
	f, _ := authAs(t, url, author, "pw")
	head := ac.Head()
	e, err := chain.NewEntry(author, &head, op, body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sendAppend(t, f, e)
}

func TestLinkMirrorsChatAndChain(t *testing.T) {
	a, b, aURL, _, ak, bk, ac := linkPair(t, "pw")
	cl := fullClient(t, aURL)
	sendEnv(t, cl, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "before"})
	waitFor(t, "history on A", func() bool { return len(histAll(a)) == 1 })
	dialLink(t, aURL, b)
	sendEnv(t, cl, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "after"})
	waitFor(t, "chats on B", func() bool { return len(histAll(b)) == 2 })
	msgs, _ := b.hist.Page("general", "", time.Time{}, 10)
	if len(msgs) != 2 || msgs[0].Text != "before" || msgs[1].Text != "after" {
		t.Fatalf("B history = %+v", msgs)
	}

	appendAs(t, aURL, ak, ac, chain.OpGrant, chain.GrantBody{Fingerprint: identity.Fingerprint(bk.PublicKey()), Roles: []string{"admin"}})
	want := ac.Head().Seq + 1
	waitFor(t, "grant on B", func() bool { return b.chain.Head().Seq == want })
	if !b.roles().Has(identity.Fingerprint(bk.PublicKey()), roles.PermManage) {
		t.Fatal("B did not refresh its roles after the grant")
	}

	// B's own chats sort after the replicated ones.
	last, _ := b.hist.Last()
	b.chMu.Lock()
	seeded := b.lastChatTS
	b.chMu.Unlock()
	if seeded.Before(last) {
		t.Fatalf("lastChatTS %v is before the newest merged line %v", seeded, last)
	}
}

func TestLinkReplicatesBan(t *testing.T) {
	_, b, aURL, bURL, ak, _, ac := linkPair(t, "pw")
	m := mustID(t)
	mc, env := authWarm(t, bURL, m, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("warm auth on B: %q", env.Type)
	}
	dialLink(t, aURL, b)
	fp := identity.Fingerprint(m.PublicKey())
	appendAs(t, aURL, ak, ac, chain.OpBan, chain.FPBody{Fingerprint: fp})
	waitFor(t, "ban on B", func() bool { st := b.chain.State(); return st.Banned(fp) })
	_ = mc.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := mc.ReadMessage(); err != nil {
			break
		}
	}
	if !b.banned(fp) {
		t.Fatal("B's group state does not show the ban")
	}
}

func TestLinkPasswordSync(t *testing.T) {
	_, b, aURL, bURL, _, _, _ := linkPair(t, "pw")
	dialLink(t, aURL, b)
	waitFor(t, "key on B", func() bool { return len(b.passwordKey(nil)) > 0 })
	b.SetMode(ModeActive)
	c, env := authAs(t, bURL, mustID(t), "")
	requireAuthFailed(t, c, env)
	c, env = authAs(t, bURL, mustID(t), "pw")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("with the password: %q", env.Type)
	}
	_ = c
}

func TestLinkActiveAlive(t *testing.T) {
	a, b, aURL, _, _, _, _ := linkPair(t, "pw")
	l := dialLink(t, aURL, b)
	if !l.ActiveAlive(time.Now()) {
		t.Fatal("not alive right after linking")
	}
	waitFor(t, "role", func() bool { return l.Standby() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.Shutdown(ctx)
	select {
	case <-l.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done did not close after the active host shut down")
	}
	if l.ActiveAlive(time.Now().Add(heartbeatGrace)) {
		t.Fatal("still alive a grace period after the last beat")
	}
}

func TestLinkConflictClosesLink(t *testing.T) {
	_, b, aURL, _, ak, _, ac := linkPair(t, "pw")
	l := dialLink(t, aURL, b)
	// B's chain moves on by itself, so A's next entry no longer extends it.
	head := b.chain.Head()
	e, err := chain.NewEntry(ak, &head, chain.OpGrant, chain.GrantBody{Fingerprint: "x", Roles: []string{"user"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := b.chain.Append(e); err != nil {
		t.Fatal(err)
	}
	appendAs(t, aURL, ak, ac, chain.OpGrant, chain.GrantBody{Fingerprint: "y", Roles: []string{"user"}})
	select {
	case <-l.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("link stayed up on a forked chain")
	}
	if l.Err() == nil {
		t.Fatal("Err is nil after a conflict")
	}
}

func TestProbeHost(t *testing.T) {
	a, b, aURL, bURL, _, _, _ := linkPair(t, "pw")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := ProbeHost(ctx, aURL, b)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Active || r.ServerID != a.chain.ID().String() || r.Self.Name != "alpha" {
		t.Fatalf("probe of A = %+v", r)
	}
	r, err = ProbeHost(ctx, bURL, a)
	if err != nil {
		t.Fatal(err)
	}
	if r.Active || r.Self.Name != "beta" {
		t.Fatalf("probe of B = %+v", r)
	}
}
