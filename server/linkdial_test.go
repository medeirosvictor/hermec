package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

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
		_, _, err := mc.ReadMessage()
		if err == nil {
			continue
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Fatal("M's connection on B was not closed after the ban")
		}
		break
	}
	if !b.banned(fp) {
		t.Fatal("B's group state does not show the ban")
	}
}

func TestLinkPasswordSync(t *testing.T) {
	_, b, aURL, bURL, _, _, ac := linkPair(t, "pw")
	var hooked atomic.Pointer[[]byte] // what the app is told to persist
	b.SetPasswordKeyHook(func(k []byte) { hooked.Store(&k) })
	dialLink(t, aURL, b)
	waitFor(t, "key on B", func() bool { return len(b.passwordKey(nil)) > 0 })
	id := ac.ID()
	want, _ := proto.PasswordKey("pw", id[:])
	waitFor(t, "key hook", func() bool { k := hooked.Load(); return k != nil && string(*k) == string(want) })
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

// The standby takes the password key from the first link_sync, so the active
// host must put it there (before any history page).
func TestLinkFirstSyncCarriesPasswordKey(t *testing.T) {
	_, _, aURL, _, _, bk, ac := linkPair(t, "pw")
	lk := linkUp(t, aURL, bk, proto.LinkHello{Name: "beta"})
	env := readType(t, lk, proto.TypeLinkSync)
	var ls proto.LinkSync
	if err := json.Unmarshal(env.Data, &ls); err != nil {
		t.Fatal(err)
	}
	id := ac.ID()
	want, _ := proto.PasswordKey("pw", id[:])
	if len(ls.PasswordKey) == 0 || string(ls.PasswordKey) != string(want) {
		t.Fatalf("first link_sync key = %x, want %x", ls.PasswordKey, want)
	}
	if len(ls.Messages) != 0 {
		t.Fatalf("first link_sync carries %d messages", len(ls.Messages))
	}
}

// Garbage link_role and hosts frames are ignored (and logged): the link stays
// up and a valid frame after them still takes effect.
func TestLinkIgnoresMalformedRoleAndHosts(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for _, f := range []string{
			`{"v":2,"type":"link_role","data":[1]}`,
			`{"v":2,"type":"hosts","data":"junk"}`,
		} {
			_ = ws.WriteMessage(websocket.TextMessage, []byte(f))
		}
		raw, _ := proto.Encode(proto.TypeLinkRole, proto.LinkRole{Standby: true})
		_ = ws.WriteMessage(websocket.TextMessage, raw)
		for { // hold the link open until the client goes
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b := New(Config{Logf: func(string, ...any) {}})
	l := &Link{s: b, ws: ws, done: make(chan struct{}), roleCh: make(chan struct{}, 1), handover: make(chan struct{}, 1)}
	l.lastBeat.Store(time.Now().UnixNano())
	t.Cleanup(l.Close)
	go l.Run()
	select {
	case <-l.RoleChanged():
	case <-l.Done():
		t.Fatalf("link ended on a malformed frame: %v", l.Err())
	case <-time.After(3 * time.Second):
		t.Fatal("the valid link_role after the garbage never arrived")
	}
	if !l.Standby() {
		t.Fatal("valid link_role not applied")
	}
	if _, ok := l.Hosts(); ok {
		t.Fatal("a malformed hosts frame was taken")
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
