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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

func TestHostedConfigRoundTrip(t *testing.T) {
	cfg := hostedConfig("  My\u202eServer ", "pw", 7697, "FP")
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

// tickUntil runs tickBackground every 10ms until cond holds; fails after 5s.
func tickUntil(t *testing.T, g *game, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within 5s")
		}
		g.tickBackground(time.Now())
		time.Sleep(10 * time.Millisecond)
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
	tickUntil(t, g, func() bool { return len(wrote) > 0 })
	if want := fmt.Sprintf("ws://100.1.2.3:%d/", port); len(wrote) != 1 || wrote[0] != want {
		t.Fatalf("wrote %v, want %q", wrote, want)
	}

	wrote = nil
	g.selfAddrs = func(context.Context) (string, string) { return "", "" }
	pickMenu(g, hostedKey, "Copy invite address")
	tickUntil(t, g, func() bool { return strings.Contains(g.sess[hostedKey].st.Status, "no network address found") })
	if len(wrote) != 0 {
		t.Fatalf("copied %v with no network address", wrote)
	}
}

// The copy result lands on the hosted tile's status line even when another
// server is the one on screen.
func TestCopyInviteStatusOnHostedSession(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	var wrote atomic.Int32
	old := clipboardWrite
	clipboardWrite = func(string) error { wrote.Add(1); return nil }
	t.Cleanup(func() { clipboardWrite = old })
	g.sess["other"] = newSession("other", "ws://other/", "me")
	g.view("other")
	g.selfAddrs = func(context.Context) (string, string) { return "", "192.168.1.5" }
	g.copyInvite()
	// Wait on the status line itself. The hosted dial can finish late, and
	// its SetConnected clears Status, so no extra tick may run once it is set.
	tickUntil(t, g, func() bool {
		return strings.HasPrefix(g.sess[hostedKey].st.Status, "copied ws://192.168.1.5:")
	})
	if wrote.Load() != 1 {
		t.Fatalf("clipboard writes = %d, want 1", wrote.Load())
	}
	if got := g.sess[hostedKey].st.Status; !strings.HasPrefix(got, "copied ws://192.168.1.5:") {
		t.Errorf("hosted status = %q", got)
	}
	if got := g.sess["other"].st.Status; got != "" {
		t.Errorf("viewed session status = %q, want untouched", got)
	}
}

func TestCopyInviteIsAsync(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	var wrote atomic.Int32
	old := clipboardWrite
	clipboardWrite = func(string) error { wrote.Add(1); return nil }
	t.Cleanup(func() { clipboardWrite = old })
	release := make(chan struct{})
	var calls atomic.Int32
	g.selfAddrs = func(context.Context) (string, string) {
		calls.Add(1)
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		return "", "192.168.1.5"
	}
	start := time.Now()
	g.copyInvite()
	g.copyInvite() // one is in flight: a no-op
	if time.Since(start) > time.Second || wrote.Load() != 0 {
		t.Fatalf("copyInvite blocked (%v) or copied early", time.Since(start))
	}
	close(release)
	tickUntil(t, g, func() bool { return wrote.Load() == 1 })
	g.tickBackground(time.Now())
	if calls.Load() != 1 || wrote.Load() != 1 {
		t.Fatalf("lookups = %d, copies = %d, want 1 and 1", calls.Load(), wrote.Load())
	}
}

func TestLeaveHostedDeleteFailure(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	old := removeFile
	removeFile = func(string) error { return errors.New("access denied") }
	t.Cleanup(func() { removeFile = old })
	srv := g.hosted.srv
	g.leaveHosted()
	if g.hosted != nil || g.notice != "couldn't delete hosted.toml: access denied" {
		t.Fatalf("hosted=%+v notice=%q", g.hosted, g.notice)
	}
	if srv.Addr() != "" {
		if c, err := net.Dial("tcp", srv.Addr()); err == nil {
			c.Close()
			t.Fatal("server still accepting")
		}
	}
}

func TestStaleDialAfterRestart(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	s := g.sess[hostedKey]
	gen1 := s.dialGen
	g.stopHosted()
	g.retryNow(hostedKey)
	if s.dialGen == gen1 || !s.dialing {
		t.Fatalf("gen %d -> %d dialing=%v, want a new dial", gen1, s.dialGen, s.dialing)
	}
	g.applyDial(dialResult{key: hostedKey, s: s, err: errors.New("old"), gen: gen1})
	if s.failed || !s.dialing {
		t.Fatalf("stale result applied: failed=%v dialing=%v", s.failed, s.dialing)
	}
	pumpUntil(t, g, connected(s)) // dial 2 (gen 2) succeeds and is not closed
	if s.failed {
		t.Fatal("failed after a good dial")
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
	g.hostedTweak = func(c *server.Config) { c.Addr, c.Discoverable = "127.0.0.1:0", false }
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

func TestHostedDialsWithItsPassword(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", false)
	cfg := hostedConfig("Den", "sesame", 0, g.fp)
	cfg.Addr, cfg.Discoverable = "127.0.0.1:0", false
	if err := saveHosted(path, cfg); err != nil {
		t.Fatal(err)
	}
	g.bootHosted(path)
	s := g.sess[hostedKey]
	pumpUntil(t, g, func() bool { return connected(s)() || s.failed })
	if s.failed || s.c == nil {
		t.Fatalf("hosted dial with password failed: %q", s.lastErr)
	}
}

func TestRetryNowHostedKeepsLastErr(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	s := g.sess[hostedKey]
	pumpUntil(t, g, connected(s))
	g.stopHosted()
	g.retryNow(hostedKey)
	if s.lastErr != stoppedMsg {
		t.Fatalf("lastErr = %q, want it kept during the redial", s.lastErr)
	}
}

func TestStoppedHostedMenuHasNoRetry(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	pumpUntil(t, g, connected(g.sess[hostedKey]))
	g.stopHosted()
	g.openTileMenu(hostedKey, 10, 70)
	got := menuLabels(g)
	if slices.Contains(got, "Retry") || !slices.Contains(got, "Start hosting") {
		t.Fatalf("stopped items = %v", got)
	}
}

func TestStartHostedNowWithoutSessionNotices(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	g, path := hostedTestGame(t, ln.Addr().String(), true)
	g.bootHosted(path)
	delete(g.sess, hostedKey) // as if the tile had no session
	g.sess["k"] = newSession("k", "ws://k/", "me")
	g.view("k")
	g.startHostedNow()
	if g.notice == "" || g.sess["k"].st.Status != "" {
		t.Fatalf("notice=%q viewed status=%q", g.notice, g.sess["k"].st.Status)
	}
}

func TestStopHostedWithoutSessionShowsFailedTile(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	delete(g.sess, hostedKey)
	g.stopHosted()
	if !g.tileFacts(state.ServerEntry{URL: hostedKey}).Failed {
		t.Fatal("stopped sessionless hosted tile is not failed")
	}
	withLayout(t, g)
	g.buildHits()
	idx := -1
	for i, e := range g.servers {
		if e.URL == hostedKey {
			idx = i
		}
	}
	x0, y0, x1, y1 := retryBadgeRect(pad + float64(idx)*railTileH)
	if h, ok := g.hitFor((x0+x1)/2, (y0+y1)/2); !ok || h.Kind != state.HitRailRetry || h.ID != idx {
		t.Fatalf("hit at stopped hosted badge = %+v, %v (tile %d)", h, ok, idx)
	}
}

func TestLostTextStopped(t *testing.T) {
	s := newSession("k", "ws://k/", "me")
	s.failed, s.lastErr = true, stoppedMsg
	if got := lostText(s, time.Now()); !strings.Contains(got, stoppedMsg) || strings.Contains(got, "reconnecting") {
		t.Fatalf("lostText = %q", got)
	}
}

func TestHistoryDir(t *testing.T) {
	got := historyDir(filepath.Join("a", "hermec", "hosted.toml"))
	if want := filepath.Join("a", "hermec", "history"); got != want {
		t.Fatalf("historyDir = %q, want %q", got, want)
	}
}

func TestStartHostedCreatesHistoryDir(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	pumpUntil(t, g, connected(g.sess[hostedKey]))
	fi, err := os.Stat(historyDir(path))
	if err != nil || !fi.IsDir() {
		t.Fatalf("history dir missing: %v", err)
	}
}
