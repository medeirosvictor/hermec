package ui

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

// founderSrv is an in-process server A with its founder connected.
type founderSrv struct {
	srv *server.Server
	url string
	id  *identity.Identity
	c   *client.Client
}

func startFounder(t *testing.T, password string) *founderSrv {
	t.Helper()
	id := mustIdentity(t)
	addr := freeLoopback(t)
	f := &founderSrv{id: id, url: "ws://" + addr + "/"}
	f.srv = server.New(server.Config{
		HostKey: id, Addr: addr, HostName: "founder", Advertise: []string{f.url}, Password: password,
		Roles:    roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
		Channels: []string{"general"}, VoiceChannels: []string{"voice"},
	})
	if err := f.srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.shutdown)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, f.url, id, "founder", password)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	go func() {
		for range c.Events() {
		}
	}()
	f.c = c
	return f
}

func (f *founderSrv) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = f.srv.Shutdown(ctx)
}

func (f *founderSrv) grant(t *testing.T, id *identity.Identity) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.c.ChainAppend(ctx, chain.OpHostAdd, chain.HostAddBody{PubKey: []byte(id.PublicKey()), Priority: 2}); err != nil {
		t.Fatal(err)
	}
}

func (f *founderSrv) remove(t *testing.T, id *identity.Identity) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.c.ChainAppend(ctx, chain.OpHostRemove, chain.FPBody{Fingerprint: identity.Fingerprint(id.PublicKey())}); err != nil {
		t.Fatal(err)
	}
}

// friendGame is a windowless game with a temp config dir and a stubbed LAN
// address; its friend hosts are stopped when the test ends.
func friendGame(t *testing.T) *game {
	t.Helper()
	g := newTestGame(t)
	withLayout(t, g)
	g.hostedPath = filepath.Join(t.TempDir(), "hosted.toml")
	g.selfAddrs = func(context.Context) (string, string) { return "", "127.0.0.1" }
	t.Cleanup(g.stopFriends)
	return g
}

func friendRole(g *game, id string) func() bool {
	return func() bool {
		f := g.friends[id]
		return f != nil && f.h != nil && f.h.Role() == server.RoleStandby
	}
}

func hostingPath(g *game, id string) string {
	return filepath.Join(filepath.Dir(g.hostedPath), "hosting", id+".toml")
}

func readHosting(t *testing.T, g *game, id string) hostingFile {
	t.Helper()
	var hf hostingFile
	if _, err := toml.DecodeFile(hostingPath(g, id), &hf); err != nil {
		t.Fatal(err)
	}
	return hf
}

// grantedFriend connects g to a new founder server and grants g hosting; it
// returns once the friend host is standby.
func grantedFriend(t *testing.T, g *game, password string) (*founderSrv, *session, string) {
	t.Helper()
	f := startFounder(t, password)
	if password != "" {
		sid, err := chain.ParseID(f.c.ServerID())
		if err != nil {
			t.Fatal(err)
		}
		key, err := proto.PasswordKey(password, sid[:])
		if err != nil {
			t.Fatal(err)
		}
		g.servers = append(g.servers, state.ServerEntry{URL: f.url, PasswordKey: hex.EncodeToString(key)})
	}
	s := g.openSession(f.url, "me")
	pumpUntil(t, g, connected(s))
	id := s.c.ServerID()
	f.grant(t, g.id)
	pumpUntil(t, g, friendRole(g, id))
	return f, s, id
}

// toggleItem opens the tile menu of s and returns the hosting row.
func toggleItem(t *testing.T, g *game, s *session) menuItem {
	t.Helper()
	g.openTileMenu(s.key, 10, 10)
	for _, it := range g.menu.items {
		if strings.HasPrefix(it.label, "let my PC host this server") {
			return it
		}
	}
	t.Fatalf("no hosting toggle in menu %v", g.menu.items)
	return menuItem{}
}

func TestFriendHostStartsWhenGranted(t *testing.T) {
	g := friendGame(t)
	start := time.Now()
	_, s, id := grantedFriend(t, g, "")
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("friend host took %v", d)
	}
	f := g.friends[id]
	if f.port < 7697 || f.port > 7707 {
		t.Errorf("port = %d, want in 7697..7707", f.port)
	}
	hf := readHosting(t, g, id)
	if !hf.Enabled || hf.Port != f.port {
		t.Errorf("hosting file = %+v", hf)
	}
	if _, err := os.Stat(filepath.Join(chainsDir(g.hostedPath), id+".jsonl")); err != nil {
		t.Errorf("chain not persisted: %v", err)
	}
	if !strings.Contains(s.st.Status, friendReachNotice) {
		t.Errorf("status = %q, want the reach notice on the first start", s.st.Status)
	}
}

func TestFriendHostToggleOff(t *testing.T) {
	g := friendGame(t)
	_, s, id := grantedFriend(t, g, "")
	item := toggleItem(t, g, s)
	if item.label != "let my PC host this server: on" {
		t.Fatalf("label = %q", item.label)
	}
	item.act()
	if g.friends[id] != nil {
		t.Fatal("friend host still registered after toggle off")
	}
	if hf := readHosting(t, g, id); hf.Enabled {
		t.Fatal("enabled still true")
	}
	if got := toggleItem(t, g, s).label; got != "let my PC host this server: off" {
		t.Fatalf("label = %q", got)
	}
	// A restart keeps it off.
	g2 := friendGame(t)
	g2.id, g2.fp = g.id, g.fp
	g2.hostedPath = g.hostedPath
	g2.bootFriends()
	time.Sleep(100 * time.Millisecond)
	g2.pumpSessions()
	if len(g2.friends) != 0 {
		t.Fatal("bootFriends started a disabled host")
	}
}

func TestFriendHostBootsEnabled(t *testing.T) {
	g := friendGame(t)
	_, _, id := grantedFriend(t, g, "")
	g.stopFriends()
	g2 := friendGame(t)
	g2.id, g2.fp = g.id, g.fp
	g2.hostedPath = g.hostedPath
	g2.bootFriends()
	pumpUntil(t, g2, friendRole(g2, id))
}

func TestFriendHostStopsOnRemove(t *testing.T) {
	g := friendGame(t)
	f, _, id := grantedFriend(t, g, "")
	f.remove(t, g.id)
	pumpUntil(t, g, func() bool { return g.friends[id] == nil })
	if _, err := os.Stat(hostingPath(g, id)); err != nil {
		t.Errorf("hosting file should stay: %v", err)
	}
}

func TestFriendHostSkippedWhenFounderHosts(t *testing.T) {
	g := friendGame(t)
	f := startFounder(t, "")
	s := g.openSession(f.url, "me")
	pumpUntil(t, g, connected(s))
	g.hosted = &hostedServer{id: s.c.ServerID()}
	f.grant(t, g.id)
	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		g.pumpSessions()
		time.Sleep(10 * time.Millisecond)
	}
	if len(g.friends) != 0 {
		t.Fatal("friend host started beside the app-hosted server")
	}
}

// A friend host starts with the password key its session logged in with, so a
// host that cold-starts into active never serves a passworded group open.
func TestFriendHostKeepsGroupPassword(t *testing.T) {
	g := friendGame(t)
	f, s, id := grantedFriend(t, g, "hunter2")
	if hf := readHosting(t, g, id); len(hf.PasswordKey) != 64 {
		t.Fatalf("password_key = %q, want 64 hex chars", hf.PasswordKey)
	}
	key := s.c.PasswordKey()
	if len(key) != 32 {
		t.Fatal("session has no password key")
	}
	f.shutdown() // the friend host cold-restarts into active
	fh := g.friends[id]
	pumpUntil(t, g, func() bool { return fh.h.Role() == server.RoleActive })
	// Server.Addr is the wildcard listener ([::]:port), which proxy-aware
	// dialers may send through a proxy. Use the concrete loopback address that
	// this test configured the friend host to advertise.
	url := fmt.Sprintf("ws://127.0.0.1:%d/", fh.port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c, err := client.Dial(ctx, url, mustIdentity(t), "x", ""); err == nil {
		c.Close()
		t.Fatal("passworded group served without a password")
	} else {
		var se *client.ServerError
		if !errors.As(err, &se) || se.Code != proto.CodeAuthFailed {
			t.Fatalf("dial %s (friend port %d): err = %v, want auth_failed", url, fh.port, err)
		}
	}
	c, err := client.DialWith(ctx, url, mustIdentity(t), "x", "", client.DialOpts{PasswordKey: key})
	if err != nil {
		t.Fatalf("dial with key: %v", err)
	}
	c.Close()
}

func TestFriendHostNoticeOnce(t *testing.T) {
	g := friendGame(t)
	_, s, id := grantedFriend(t, g, "")
	if !strings.Contains(s.st.Status, friendReachNotice) {
		t.Fatalf("status = %q", s.st.Status)
	}
	s.st.Status = ""
	toggleItem(t, g, s).act() // off
	toggleItem(t, g, s).act() // on again
	pumpUntil(t, g, friendRole(g, id))
	if strings.Contains(s.st.Status, friendReachNotice) {
		t.Fatal("notice shown twice")
	}
}

func TestHostedUsesOrchestrator(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", false)
	g.selfAddrs = func(context.Context) (string, string) { return "", "127.0.0.1" }
	g.hostedPath = path
	if err := g.createHosted("Den", "", 0); err != nil {
		t.Fatal(err)
	}
	pumpUntil(t, g, func() bool { return g.hosted.srv.Role() == server.RoleActive }) // the host loop settles on its own goroutine
}

func TestHostedKnownHostsPersist(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	g.hosted.saveKnown([]string{"ws://10.0.0.5:7697/"})
	cfg, ok, err := loadHosted(path)
	if err != nil || !ok || len(cfg.KnownHosts) != 1 || cfg.KnownHosts[0] != "ws://10.0.0.5:7697/" {
		t.Fatalf("cfg = %+v ok=%v err=%v", cfg.KnownHosts, ok, err)
	}
	if got := g.hosted.knownHosts(); len(got) != 1 {
		t.Fatalf("knownHosts = %v", got)
	}
}
