package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

func TestHostedConfigRoundTrip(t *testing.T) {
	cfg := hostedConfig("  My‮Server ", "pw", 7697, "FP")
	path := filepath.Join(t.TempDir(), "sub", "hosted.toml")
	if err := saveHosted(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, ok, err := loadHosted(path)
	if err != nil || !ok {
		t.Fatalf("loadHosted ok=%v err=%v", ok, err)
	}
	if got.ServerName != "MyServer" || got.Addr != ":7697" || got.Password != "pw" {
		t.Errorf("got name=%q addr=%q pw=%q", got.ServerName, got.Addr, got.Password)
	}
	if !slices.Equal(got.Channels, []string{"general"}) || !slices.Equal(got.VoiceChannels, []string{"voice"}) {
		t.Errorf("channels=%v voice=%v", got.Channels, got.VoiceChannels)
	}
	if !slices.Contains(got.Roles.RolesFor("FP"), "admin") {
		t.Errorf("FP roles = %v", got.Roles.RolesFor("FP"))
	}
	if r := got.Roles.RolesFor("other"); !slices.Equal(r, []string{"user"}) {
		t.Errorf("other roles = %v", r)
	}
	if hostedPort(got) != 7697 {
		t.Errorf("hostedPort = %d", hostedPort(got))
	}
	if runtime.GOOS == "windows" {
		return // file modes are not meaningful on Windows
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestLoadHostedMissing(t *testing.T) {
	_, ok, err := loadHosted(filepath.Join(t.TempDir(), "nope.toml"))
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v, want false, nil", ok, err)
	}
}

func TestIsPortInUse(t *testing.T) {
	wsa := fmt.Errorf("server: listen: %w", &net.OpError{Err: os.NewSyscallError("bind", syscall.Errno(10048))})
	if !isPortInUse(wsa) {
		t.Error("WSAEADDRINUSE not detected")
	}
	if !isPortInUse(fmt.Errorf("x: %w", server.ErrDiscoveryBind)) {
		t.Error("ErrDiscoveryBind not detected")
	}
	if isPortInUse(errors.New("boom")) {
		t.Error("plain error detected")
	}
}

func TestIsPortInUseReal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	s := server.New(server.Config{Addr: ln.Addr().String(), Discoverable: false})
	err = s.Start()
	if err == nil {
		t.Fatal("Start succeeded on a busy port")
	}
	if !isPortInUse(err) {
		t.Errorf("isPortInUse(%v) = false", err)
	}
}

func TestPortInUseMsg(t *testing.T) {
	if got, want := portInUseMsg(7697), "port 7697 is in use (is hermec-server running?)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInviteAddress(t *testing.T) {
	for _, c := range []struct{ ts, lan, want string }{
		{"100.64.1.2", "192.168.2.10", "ws://100.64.1.2:7697/"},
		{"", "192.168.2.10", "ws://192.168.2.10:7697/"},
		{"", "", "no network address found"},
	} {
		if got := inviteAddress(c.ts, c.lan, 7697); got != c.want {
			t.Errorf("inviteAddress(%q,%q) = %q, want %q", c.ts, c.lan, got, c.want)
		}
	}
}

// hostedTestGame writes a hosted.toml named "Den" listening on addr (discovery
// off) and returns a windowless game plus the file path.
func hostedTestGame(t *testing.T, addr string, write bool) (*game, string) {
	t.Helper()
	g := newTestGame(t)
	withLayout(t, g)
	path := filepath.Join(t.TempDir(), "hosted.toml")
	if write {
		cfg := hostedConfig("Den", "", 0, g.fp)
		cfg.Addr = addr
		cfg.Discoverable = false
		if err := saveHosted(path, cfg); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(g.stopHosted)
	return g, path
}

func TestRealURLHosted(t *testing.T) {
	g := &game{hosted: &hostedServer{url: "ws://127.0.0.1:4242/"}}
	if got := g.realURL(hostedKey); got != "ws://127.0.0.1:4242/" {
		t.Errorf("realURL = %q", got)
	}
	if got := g.keyFor("ws://127.0.0.1:4242/"); got != hostedKey {
		t.Errorf("keyFor = %q", got)
	}
	g.hosted.url = ""
	if got := g.realURL(hostedKey); got != hostedKey {
		t.Errorf("realURL with no url = %q", got)
	}
}

func TestStartupHostedAutoStart(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	if g.hosted == nil || g.hosted.srv == nil {
		t.Fatalf("hosted = %+v, want a running server", g.hosted)
	}
	if !strings.HasPrefix(g.hosted.url, "ws://127.0.0.1:") || strings.HasSuffix(g.hosted.url, ":0/") {
		t.Fatalf("url = %q, want the real port", g.hosted.url)
	}
	s := g.sess[hostedKey]
	if s == nil || !s.dialing {
		t.Fatalf("session = %+v, want one dialing", s)
	}
	if e, ok := g.entryFor(hostedKey); !ok || e.Label != "Den" {
		t.Fatalf("entry = %+v ok=%v, want label Den", e, ok)
	}
	pumpUntil(t, g, connected(s))
	if e, _ := g.entryFor(hostedKey); e.Label != "Den" || e.Pending {
		t.Fatalf("saved entry = %+v", e)
	}
}

func TestStartupWithoutHostedFile(t *testing.T) {
	g, path := hostedTestGame(t, "", false)
	g.bootHosted(path)
	if g.hosted != nil || len(g.sess) != 0 {
		t.Fatalf("hosted = %+v sess = %v", g.hosted, g.sess)
	}
}

func TestStartupHostedPortBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	g, path := hostedTestGame(t, ln.Addr().String(), true)
	g.bootHosted(path)
	port := ln.Addr().(*net.TCPAddr).Port
	s := g.sess[hostedKey]
	if s == nil || !s.failed || s.lastErr != portInUseMsg(port) || s.dialing {
		t.Fatalf("session = %+v, want failed with %q", s, portInUseMsg(port))
	}
	if g.hosted.srv != nil || g.hosted.err != portInUseMsg(port) {
		t.Fatalf("hosted = %+v", g.hosted)
	}
	if _, ok := g.entryFor(hostedKey); !ok {
		t.Fatal("no rail tile for the busy hosted server")
	}
}

func TestStopStartHosted(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	s := g.sess[hostedKey]
	pumpUntil(t, g, connected(s))
	g.stopHosted()
	const stopped = "not hosting — click ↻ to start"
	if g.hosted.srv != nil || g.hosted.err != stopped || !s.failed || s.lastErr != stopped || s.c != nil {
		t.Fatalf("after stop: hosted=%+v failed=%v lastErr=%q c=%v", g.hosted, s.failed, s.lastErr, s.c)
	}
	g.pumpSessions()
	if !s.failed || s.dialing {
		t.Fatalf("session redialed by itself: failed=%v dialing=%v", s.failed, s.dialing)
	}
	g.retryNow(hostedKey)
	if g.hosted.srv == nil || s.failed {
		t.Fatalf("after retry: hosted=%+v failed=%v", g.hosted, s.failed)
	}
	pumpUntil(t, g, connected(s))
}

func TestHostedMenu(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	g.servers = append(g.servers, state.ServerEntry{URL: "k", Label: "k"})
	g.openTileMenu(hostedKey, 10, 70)
	got := menuLabels(g)
	if !slices.Contains(got, "Copy invite address") || !slices.Contains(got, "Stop hosting") || slices.Contains(got, "Start hosting") {
		t.Fatalf("live items = %v", got)
	}
	g.openTileMenu("k", 10, 70)
	for _, l := range menuLabels(g) {
		if l == "Copy invite address" || l == "Stop hosting" || l == "Start hosting" {
			t.Fatalf("plain tile has %q", l)
		}
	}
	pickMenu(g, hostedKey, "Stop hosting")
	if g.hosted.srv != nil {
		t.Fatal("Stop hosting did not stop")
	}
	g.openTileMenu(hostedKey, 10, 70)
	got = menuLabels(g)
	if !slices.Contains(got, "Start hosting") || slices.Contains(got, "Stop hosting") || slices.Contains(got, "Copy invite address") {
		t.Fatalf("stopped items = %v", got)
	}
	pickMenu(g, hostedKey, "Start hosting")
	if g.hosted.srv == nil {
		t.Fatal("Start hosting did not start")
	}
}

func TestHostedMenuCopy(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	var wrote []string
	old := clipboardWrite
	clipboardWrite = func(s string) error { wrote = append(wrote, s); return nil }
	t.Cleanup(func() { clipboardWrite = old })
	port := hostedPort(server.Config{Addr: strings.TrimSuffix(strings.TrimPrefix(g.hosted.url, "ws://"), "/")})

	g.selfAddrs = func(context.Context) (string, string) { return "100.1.2.3", "192.168.1.5" }
	pickMenu(g, hostedKey, "Copy invite address")
	if want := fmt.Sprintf("ws://100.1.2.3:%d/", port); len(wrote) != 1 || wrote[0] != want {
		t.Fatalf("wrote %v, want %q", wrote, want)
	}

	wrote = nil
	g.selfAddrs = func(context.Context) (string, string) { return "", "" }
	pickMenu(g, hostedKey, "Copy invite address")
	if len(wrote) != 0 {
		t.Fatalf("copied %v with no network address", wrote)
	}
	if got := g.cur().st.Status + g.notice; !strings.Contains(got, "no network address found") {
		t.Errorf("no feedback about the missing address: %q", got)
	}
}

func TestLeaveHostedDeletesFile(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	pumpUntil(t, g, connected(g.sess[hostedKey]))
	g.openLeaveDialog(hostedKey)
	if got := g.leaveSuffixFor(hostedKey); got != "This stops hosting and deletes this server's settings." {
		t.Fatalf("suffix = %q", got)
	}
	if got := g.leaveSuffixFor("k"); got != leaveSuffix {
		t.Fatalf("plain suffix = %q", got)
	}
	addr := g.hosted.srv.Addr()
	g.dialogConfirm()
	if g.hosted != nil || g.sess[hostedKey] != nil {
		t.Fatalf("hosted=%+v sess=%v", g.hosted, g.sess)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hosted.toml still there: %v", err)
	}
	if _, ok := g.entryFor(hostedKey); ok {
		t.Fatal("tile still on the rail")
	}
	if c, err := net.Dial("tcp", addr); err == nil {
		c.Close()
		t.Fatal("server still accepting")
	}
}

func TestCreateHostedRunsAndOpens(t *testing.T) {
	g, path := hostedTestGame(t, "", false)
	g.hostedPath = path
	if err := g.createHosted("Den", "pw", 0); err != nil {
		t.Fatal(err)
	}
	if g.hosted == nil || g.hosted.srv == nil || g.sess[hostedKey] == nil || g.curKey != hostedKey {
		t.Fatalf("hosted=%+v sess=%v cur=%q", g.hosted, g.sess, g.curKey)
	}
	if _, ok, err := loadHosted(path); !ok || err != nil {
		t.Fatalf("hosted.toml not saved: ok=%v err=%v", ok, err)
	}
}
