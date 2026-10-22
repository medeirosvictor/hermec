package ui

import (
	"context"
	"errors"
	"net"
	"strings"
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

// Fast heartbeats for every server in this package's tests, set before any
// goroutine runs (the timing is a plain variable).
func init() { server.SetHeartbeatForTest(50*time.Millisecond, 150*time.Millisecond) }

// uiPair is an active host "a" and a standby "b" on one chain, b linked to a.
type uiPair struct {
	a, b       *server.Server
	aURL, bURL string
	link       *server.Link
}

func freeLoopback(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func startUIPair(t *testing.T) *uiPair {
	t.Helper()
	ak, bk := mustIdentity(t), mustIdentity(t)
	cfg := server.Config{
		Roles:         roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels:      []string{"general", "random"},
		VoiceChannels: []string{"voice"}, AllowLoopbackICE: true,
	}
	g, err := chain.NewEntry(ak, nil, chain.OpGenesis, chain.GenesisBody{
		Founder: []byte(ak.PublicKey()), Salt: []byte("0123456789abcdef"),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ac, err := chain.New(g)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.ApplyTOML(ac, ak, cfg, []string{"channels", "roles", "default_roles"}); err != nil {
		t.Fatal(err)
	}
	for i, k := range []*identity.Identity{ak, bk} {
		head := ac.Head()
		e, err := chain.NewEntry(ak, &head, chain.OpHostAdd, chain.HostAddBody{PubKey: []byte(k.PublicKey()), Priority: i + 1}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := ac.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	bc, err := chain.FromEntries(ac.Entries())
	if err != nil {
		t.Fatal(err)
	}
	addrA, addrB := freeLoopback(t), freeLoopback(t)
	p := &uiPair{aURL: "ws://" + addrA + "/", bURL: "ws://" + addrB + "/"}
	acfg, bcfg := cfg, cfg
	acfg.HostKey, acfg.Chain, acfg.HostName, acfg.Addr, acfg.Advertise = ak, ac, "a", addrA, []string{p.aURL}
	bcfg.HostKey, bcfg.Chain, bcfg.HostName, bcfg.Addr, bcfg.Advertise = bk, bc, "b", addrB, []string{p.bURL}
	bcfg.Mode = server.ModeStandby
	start := func(c server.Config) *server.Server {
		s := server.New(c)
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.Shutdown(ctx)
		})
		return s
	}
	p.a, p.b = start(acfg), start(bcfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.link, err = server.DialLink(ctx, p.aURL, p.b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.link.Close)
	go p.link.Run()
	deadline := time.Now().Add(5 * time.Second)
	for !p.link.Standby() {
		if time.Now().After(deadline) {
			t.Fatal("b never became standby")
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.b.SetLinkView(p.link.ActiveAlive, p.link.Standby)
	return p
}

func mustIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (p *uiPair) killA(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.a.Shutdown(ctx)
}

// pairGame opens a session on the pair's active host and waits for the warm
// standby session.
func pairGame(t *testing.T) (*game, *uiPair, *session) {
	t.Helper()
	g := newTestGame(t)
	g.onDialed = func(c *client.Client) { c.SetLoopbackICE(true) }
	p := startUIPair(t)
	s := g.openSession(p.aURL, "me")
	g.view(s.key)
	pumpUntil(t, g, joinedAll(s))
	start := time.Now()
	pumpUntil(t, g, func() bool { return s.warm != nil })
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("warm session took %v", d)
	}
	return g, p, s
}

func TestSessionFailoverSwapsClient(t *testing.T) {
	g, p, s := pairGame(t)
	warm := s.warm
	ch := s.st.Channels[s.st.Active]
	p.killA(t)
	pumpUntil(t, g, func() bool {
		if s.failed || s.st.Phase == state.PhaseDisconnected {
			t.Fatalf("reconnect screen shown: failed=%v phase=%v", s.failed, s.st.Phase)
		}
		return s.c == warm && !s.c.Warm() && s.ms.confirmed[ch]
	})
	if s.warm != nil {
		t.Fatal("warm session still set after promotion")
	}
	if p.b.Mode() != server.ModeActive {
		t.Fatalf("b mode = %v, want active", p.b.Mode())
	}
}

func TestSessionActiveAliveRedials(t *testing.T) {
	g, p, s := pairGame(t)
	old := s.c
	_ = old.Close() // only the game's connection to a drops
	pumpUntil(t, g, func() bool { return s.c != nil && s.c != old && connected(s)() && s.st.HostedBy == "a" })
	if p.b.Mode() != server.ModeStandby {
		t.Fatalf("b mode = %v, want standby", p.b.Mode())
	}
	if s.failed {
		t.Fatal("tile shows failed")
	}
}

func TestSessionMoveFollows(t *testing.T) {
	g, p, s := pairGame(t)
	old := s.c
	if !p.b.Promote() {
		t.Fatal("b did not promote")
	}
	p.a.Demote(proto.HostInfo{Name: "b", Addrs: []string{p.bURL}}, "handover", nil)
	pumpUntil(t, g, func() bool {
		if s.failed {
			t.Fatal("tile shows failed")
		}
		return s.c != nil && s.c != old && connected(s)() && s.st.HostedBy == "b"
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := old.Join(ctx, "general"); err == nil {
		t.Fatal("old client still usable after the move")
	}
}

func TestHostedByLine(t *testing.T) {
	g, p, s := pairGame(t)
	g.servers = []state.ServerEntry{{URL: s.key, Label: "primos"}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.c.JoinVoice(ctx, "voice", nopSource{}, nopSink{}); err != nil {
		t.Fatalf("JoinVoice: %v", err)
	}
	callOn(g, s, "voice")
	pumpUntil(t, g, func() bool { return s.st.HostedBy == "a" })
	if got := g.voicePanelLine(); got != "voice / primos · hosted by a" {
		t.Fatalf("line = %q", got)
	}
	warm := s.warm
	p.killA(t)
	pumpVoiceUntil(t, g, func() bool {
		return s.c == warm && !g.vc.busy && s.st.HostedBy == "b" && s.c.VoiceChannel() == "voice"
	})
	if got := g.voicePanelLine(); !strings.Contains(got, "hosted by b") {
		t.Fatalf("line = %q, want hosted by b", got)
	}
	if g.callKey != s.key || s.st.InCall != "voice" {
		t.Fatalf("call lost: callKey %q InCall %q", g.callKey, s.st.InCall)
	}
}

func TestDialNotActiveFollowsHosts(t *testing.T) {
	g := newTestGame(t)
	p := startUIPair(t)
	// the saved URL is the standby: the dial is refused not_active and must
	// go to the active host named by the hosts frame at once.
	s := g.openSession(p.bURL, "me")
	pumpUntil(t, g, connected(s))
	if s.st.HostedBy != "a" {
		t.Fatalf("hosted by %q, want a", s.st.HostedBy)
	}
}

func TestDialTriesKnownHosts(t *testing.T) {
	g := newTestGame(t)
	p := startUIPair(t)
	dead := "ws://" + freeLoopback(t) + "/"
	g.servers = []state.ServerEntry{{URL: dead, Label: "x", Hosts: []string{dead, p.aURL}}}
	s := g.openSession(dead, "me")
	pumpUntil(t, g, connected(s))
	if s.failed {
		t.Fatal("failed despite a live known host")
	}
}

// warmOn dials a warm client to url and parks it on s as its warm session.
func warmOn(t *testing.T, g *game, s *session, url string) *client.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	w, err := client.DialWith(ctx, url, g.id, "me", "", client.DialOpts{Warm: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	s.warm = w
	return w
}

func warmClosed(w *client.Client) bool {
	return errors.Is(w.ReportReach("x", true), client.ErrClosed)
}

func TestWarmClosedOnDisconnectQuitAndStop(t *testing.T) {
	t.Run("disconnect", func(t *testing.T) {
		g := newTestGame(t)
		_, url := startServer(t, "127.0.0.1:0")
		s := g.openSession(url, "me")
		pumpUntil(t, g, connected(s))
		w := warmOn(t, g, s, url)
		g.disconnect(s.key)
		if !warmClosed(w) {
			t.Fatal("warm client left open by disconnect")
		}
	})
	t.Run("quit", func(t *testing.T) {
		g := newTestGame(t)
		_, url := startServer(t, "127.0.0.1:0")
		s := g.openSession(url, "me")
		pumpUntil(t, g, connected(s))
		w := warmOn(t, g, s, url)
		g.closeSessions()
		if !warmClosed(w) || s.warm != nil {
			t.Fatal("warm client left open by quit")
		}
	})
	t.Run("hosted stop", func(t *testing.T) {
		g, path := hostedTestGame(t, "127.0.0.1:0", true)
		g.bootHosted(path)
		s := g.sess[hostedKey]
		pumpUntil(t, g, connected(s))
		w := warmOn(t, g, s, g.hosted.url)
		g.stopHosted()
		if s.warm != nil || !warmClosed(w) {
			t.Fatal("warm client left open by stopHosted")
		}
	})
}

// duplexNop is fake call devices that really carry (silent) opus.
type duplexNop struct {
	nopSource
	nopSink
	fakeCloser
}

// The active host only dropped us and answers the redial while a takeover is
// pending: the call rejoins there with the same mute state, not ended.
func TestRedialWinKeepsCall(t *testing.T) {
	g, _, s := pairGame(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.c.JoinVoice(ctx, "voice", nopSource{}, nopSink{}); err != nil {
		t.Fatalf("JoinVoice: %v", err)
	}
	d := &duplexNop{}
	g.vc = voiceCall{start: time.Now(), src: d, sink: d, muted: true}
	g.callKey = s.key
	s.st.SetInCall("voice")
	old := s.c
	_ = old.Close() // only our connection drops
	pumpVoiceUntil(t, g, func() bool {
		return s.c != nil && s.c != old && !g.vc.busy && s.c.VoiceChannel() == "voice"
	})
	if g.callKey != s.key || s.st.InCall != "voice" || !g.vc.muted {
		t.Fatalf("call lost: callKey %q InCall %q muted %v", g.callKey, s.st.InCall, g.vc.muted)
	}
	if s.st.HostedBy != "a" {
		t.Fatalf("hosted by %q, want a", s.st.HostedBy)
	}
}
