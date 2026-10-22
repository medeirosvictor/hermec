package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

// hostChain is a chain founded by a with a host_add for each of others, at
// priorities 2, 3, ...
func hostChain(t *testing.T, others ...*identity.Identity) (*chain.Chain, *identity.Identity) {
	t.Helper()
	a := mustID(t)
	c, err := newChain(a, Config{
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, o := range others {
		head := c.Head()
		e, err := chain.NewEntry(a, &head, chain.OpHostAdd,
			chain.HostAddBody{PubKey: []byte(o.PublicKey()), Priority: i + 2}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	return c, a
}

func twoHostChain(t *testing.T) (c *chain.Chain, a, b *identity.Identity) {
	t.Helper()
	b = mustID(t)
	c, a = hostChain(t, b)
	return c, a, b
}

func startLinkServer(t *testing.T, c *chain.Chain, a *identity.Identity, password string) (*Server, string) {
	t.Helper()
	return startModeServer(t, Config{
		HostKey: a, Chain: c, Password: password,
		HostName: "alpha", Advertise: []string{"ws://a.example:1"},
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"},
	})
}

// readType reads frames until one of type typ arrives and returns it.
func readType(t *testing.T, c *websocket.Conn, typ string) proto.Envelope {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q: %v", typ, err)
		}
		env, err := proto.Decode(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if env.Type == typ {
			return env
		}
	}
}

// readNext reads the next frame that is not a roster, hosts, link_role or hb.
func readNext(t *testing.T, c *websocket.Conn) proto.Envelope {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		env, err := proto.Decode(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		switch env.Type {
		case proto.TypeRoster, proto.TypeHosts, proto.TypeHeartbeat, proto.TypeLinkRole:
			continue
		}
		return env
	}
}

// linkUp authenticates id warm and sends link_hello.
func linkUp(t *testing.T, url string, id *identity.Identity, hello proto.LinkHello) *websocket.Conn {
	t.Helper()
	c, env := authWarm(t, url, id, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("warm auth: %q", env.Type)
	}
	sendEnv(t, c, proto.TypeLinkHello, hello)
	return c
}

func fullClient(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	c, env := authAs(t, url, mustID(t), "pw")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("auth: %q", env.Type)
	}
	sendEnv(t, c, proto.TypeJoin, proto.Join{Channel: "general"})
	return c
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func histAll(s *Server) []proto.ChatMessage {
	msgs, _ := s.hist.After(time.Time{}, "", 100)
	return msgs
}

func TestLinkHelloStreamsChainHistoryPassword(t *testing.T) {
	c, a, b := twoHostChain(t)
	s, url := startLinkServer(t, c, a, "pw")
	cl := fullClient(t, url)
	for _, txt := range []string{"one", "two", "three"} {
		sendEnv(t, cl, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: txt})
	}
	waitFor(t, "history", func() bool { return len(histAll(s)) == 3 })

	lk := linkUp(t, url, b, proto.LinkHello{Name: "beta", Addrs: []string{"ws://b.example:2"}})
	want := len(c.Since(0))
	got := 0
	for got < want {
		env := readNext(t, lk)
		if env.Type != proto.TypeChain {
			t.Fatalf("want chain frame, got %q", env.Type)
		}
		var f proto.ChainFrame
		if err := json.Unmarshal(env.Data, &f); err != nil {
			t.Fatal(err)
		}
		got += len(f.Entries)
	}
	var sync proto.LinkSync
	env := readNext(t, lk)
	if env.Type != proto.TypeLinkSync {
		t.Fatalf("want link_sync, got %q", env.Type)
	}
	_ = json.Unmarshal(env.Data, &sync)
	id := c.ID()
	wantKey, _ := proto.PasswordKey("pw", id[:])
	if !reflect.DeepEqual(sync.PasswordKey, wantKey) || len(sync.PasswordKey) == 0 {
		t.Fatalf("password key = %x, want %x", sync.PasswordKey, wantKey)
	}
	var texts []string
	for len(texts) < 3 {
		env := readNext(t, lk)
		var m proto.LinkSync
		if env.Type != proto.TypeLinkSync || json.Unmarshal(env.Data, &m) != nil {
			t.Fatalf("want link_sync, got %q", env.Type)
		}
		for _, msg := range m.Messages {
			texts = append(texts, msg.Text)
		}
	}
	if !reflect.DeepEqual(texts, []string{"one", "two", "three"}) {
		t.Fatalf("history = %v", texts)
	}
	sendEnv(t, cl, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "live"})
	for {
		env = readType(t, lk, proto.TypeLinkSync)
		var live proto.LinkSync
		_ = json.Unmarshal(env.Data, &live)
		if len(live.Messages) == 0 {
			continue // the end-of-history marker
		}
		if len(live.Messages) != 1 || live.Messages[0].Text != "live" {
			t.Fatalf("live = %+v", live)
		}
		break
	}
	readType(t, lk, proto.TypeHeartbeat)
}

func TestLinkRefusesNonHost(t *testing.T) {
	c, a, _ := twoHostChain(t)
	_, url := startLinkServer(t, c, a, "pw")
	// A non-host can only warm-auth with the password.
	oc, env := authWarm(t, url, mustID(t), "pw")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("auth: %q", env.Type)
	}
	sendEnv(t, oc, proto.TypeLinkHello, proto.LinkHello{})
	if code := errCode(readType(t, oc, proto.TypeError)); code != proto.CodeForbidden {
		t.Fatalf("code = %q", code)
	}
}

func TestLinkRefusesOlderChain(t *testing.T) {
	c, a, b := twoHostChain(t)
	_, url := startLinkServer(t, c, a, "pw")
	lk := linkUp(t, url, b, proto.LinkHello{ChainSeq: c.Head().Seq + 5})
	if code := errCode(readType(t, lk, proto.TypeError)); code != proto.CodeBadChain {
		t.Fatalf("code = %q", code)
	}
}

// hostsUntil reads hosts frames until one satisfies ok and returns it.
func hostsUntil(t *testing.T, c *websocket.Conn, ok func(proto.Hosts) bool) proto.Hosts {
	t.Helper()
	for {
		env := readType(t, c, proto.TypeHosts)
		var h proto.Hosts
		_ = json.Unmarshal(env.Data, &h)
		if ok(h) {
			return h
		}
	}
}

func TestStandbyPickAndHostsBroadcast(t *testing.T) {
	b, cc := mustID(t), mustID(t)
	c, a := hostChain(t, b, cc)
	_, url := startLinkServer(t, c, a, "pw")
	cl := fullClient(t, url)
	lb := linkUp(t, url, b, proto.LinkHello{Name: "b"})
	fpB, fpC := identity.Fingerprint(b.PublicKey()), identity.Fingerprint(cc.PublicKey())
	h := hostsUntil(t, cl, func(h proto.Hosts) bool { return h.Standby != nil && h.Standby.Fingerprint == fpB })
	if h.Active.Fingerprint != identity.Fingerprint(a.PublicKey()) || h.Active.Name != "alpha" {
		t.Fatalf("active = %+v", h.Active)
	}
	lc := linkUp(t, url, cc, proto.LinkHello{Name: "c"})
	var r proto.LinkRole
	_ = json.Unmarshal(readType(t, lb, proto.TypeLinkRole).Data, &r)
	if !r.Standby {
		t.Fatal("B should be standby")
	}
	r = proto.LinkRole{}
	_ = json.Unmarshal(readType(t, lc, proto.TypeLinkRole).Data, &r)
	if r.Standby {
		t.Fatal("C should not be standby")
	}
	lb.Close()
	hostsUntil(t, cl, func(h proto.Hosts) bool { return h.Standby != nil && h.Standby.Fingerprint == fpC })
}

func TestStandbySkipsUnreachable(t *testing.T) {
	b, cc := mustID(t), mustID(t)
	c, a := hostChain(t, b, cc)
	s, url := startLinkServer(t, c, a, "pw")
	var clients []*websocket.Conn
	for i := 0; i < 3; i++ {
		clients = append(clients, fullClient(t, url))
	}
	linkUp(t, url, b, proto.LinkHello{Name: "b"})
	linkUp(t, url, cc, proto.LinkHello{Name: "c"})
	fpB, fpC := identity.Fingerprint(b.PublicKey()), identity.Fingerprint(cc.PublicKey())
	waitFor(t, "links", func() bool { return len(s.Links()) == 2 })
	waitFor(t, "standby B", func() bool { return standbyFP(s) == fpB })
	for _, cl := range clients[:2] {
		sendEnv(t, cl, proto.TypeReach, proto.Reach{Fingerprint: fpB, OK: false})
	}
	sendEnv(t, clients[2], proto.TypeReach, proto.Reach{Fingerprint: fpB, OK: true})
	for _, cl := range clients {
		sendEnv(t, cl, proto.TypeReach, proto.Reach{Fingerprint: fpC, OK: true})
	}
	waitFor(t, "standby C", func() bool { return standbyFP(s) == fpC })
}

// standbyFP is the fingerprint of the host the server currently names standby.
func standbyFP(s *Server) string {
	if h := s.standby.Load(); h != nil {
		return h.Fingerprint
	}
	return ""
}

func TestLinkProbe(t *testing.T) {
	c, a, b := twoHostChain(t)
	_, url := startLinkServer(t, c, a, "pw")
	fullClient(t, url)
	pc, env := authWarm(t, url, b, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("auth: %q", env.Type)
	}
	sendEnv(t, pc, proto.TypeLinkProbe, struct{}{})
	var r proto.LinkProbeReply
	_ = json.Unmarshal(readType(t, pc, proto.TypeLinkProbeReply).Data, &r)
	if !r.Active || r.VoiceMembers != 0 || r.Clients != 1 || r.Priority != 1 || r.Self.Name != "alpha" || r.ServerID != c.ID().String() {
		t.Fatalf("reply = %+v", r)
	}

	oc, env := authWarm(t, url, mustID(t), "pw")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("auth: %q", env.Type)
	}
	sendEnv(t, oc, proto.TypeLinkProbe, struct{}{})
	if code := errCode(readType(t, oc, proto.TypeError)); code != proto.CodeForbidden {
		t.Fatalf("non-host probe code = %q", code)
	}
}

func TestHostRemoveDropsLink(t *testing.T) {
	c, a, b := twoHostChain(t)
	s, url := startLinkServer(t, c, a, "pw")
	lk := linkUp(t, url, b, proto.LinkHello{Name: "b"})
	waitFor(t, "link", func() bool { return len(s.Links()) == 1 })

	f, env := authAs(t, url, a, "pw")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("founder auth: %q", env.Type)
	}
	head := c.Head()
	e, err := chain.NewEntry(a, &head, chain.OpHostRemove, chain.FPBody{Fingerprint: identity.Fingerprint(b.PublicKey())}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sendAppend(t, f, e)
	_ = lk.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := lk.ReadMessage(); err != nil {
			break
		}
	}
	waitFor(t, "link gone", func() bool { return len(s.Links()) == 0 })
}

// A long history takes several frames; live chats and heartbeats flowing
// meanwhile must not get the link dropped as slow, and every line arrives.
func TestLinkSyncMultiPageWithLiveTraffic(t *testing.T) {
	t.Cleanup(SetHeartbeatForTest(10*time.Millisecond, proto.HeartbeatGrace))
	c, a, b := twoHostChain(t)
	s, url := startLinkServer(t, c, a, "pw")
	cl := fullClient(t, url)
	text := strings.Repeat("x", 4000)
	want := map[string]bool{}
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 120; i++ {
		m := proto.ChatMessage{ID: fmt.Sprintf("%032d", i), Channel: "general", Text: text, TS: base.Add(time.Duration(i) * time.Second)}
		if err := s.hist.Add(m); err != nil {
			t.Fatal(err)
		}
		want[m.ID] = true
	}
	lk := linkUp(t, url, b, proto.LinkHello{Name: "b"})
	for i := 0; i < 5; i++ {
		sendEnv(t, cl, proto.TypeChatSend, proto.ChatSend{Channel: "general", Text: "live"})
	}
	got := map[string]bool{}
	live := 0
	for len(got) < len(want) || live < 5 {
		env := readType(t, lk, proto.TypeLinkSync)
		var m proto.LinkSync
		_ = json.Unmarshal(env.Data, &m)
		for _, msg := range m.Messages {
			got[msg.ID] = true
			if msg.Text == "live" {
				live++
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("history line %s never arrived", id)
		}
	}
	if len(s.Links()) != 1 {
		t.Fatal("link was dropped")
	}
}

func TestBanClosesLink(t *testing.T) {
	c, a, b := twoHostChain(t)
	s, url := startLinkServer(t, c, a, "pw")
	lk := linkUp(t, url, b, proto.LinkHello{Name: "b"})
	waitFor(t, "link", func() bool { return len(s.Links()) == 1 })
	f, _ := authAs(t, url, a, "pw")
	head := c.Head()
	e, err := chain.NewEntry(a, &head, chain.OpBan, chain.FPBody{Fingerprint: identity.Fingerprint(b.PublicKey())}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sendAppend(t, f, e)
	_ = lk.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := lk.ReadMessage(); err != nil {
			break
		}
	}
	waitFor(t, "link gone", func() bool { return len(s.Links()) == 0 })
}

// The share of clients that must reach a host changes as clients come and go.
func TestStandbyRecomputedWhenClientsJoin(t *testing.T) {
	b, cc := mustID(t), mustID(t)
	c, a := hostChain(t, b, cc)
	s, url := startLinkServer(t, c, a, "pw")
	fpB, fpC := identity.Fingerprint(b.PublicKey()), identity.Fingerprint(cc.PublicKey())
	first := fullClient(t, url)
	linkUp(t, url, b, proto.LinkHello{Name: "b"})
	linkUp(t, url, cc, proto.LinkHello{Name: "c"})
	waitFor(t, "links", func() bool { return len(s.Links()) == 2 })
	sendEnv(t, first, proto.TypeReach, proto.Reach{Fingerprint: fpB, OK: false})
	waitFor(t, "standby C", func() bool { return standbyFP(s) == fpC })
	hostsUntil(t, first, func(h proto.Hosts) bool { return h.Standby != nil && h.Standby.Fingerprint == fpC })
	fullClient(t, url)
	fullClient(t, url)
	hostsUntil(t, first, func(h proto.Hosts) bool { return h.Standby != nil && h.Standby.Fingerprint == fpB })
}
