package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

func addTestGame(t *testing.T) *game {
	t.Helper()
	g := testGame(t)
	n := newTestGame(t)
	g.id, g.fp, g.opts, g.serversPath = n.id, n.fp, n.opts, n.serversPath
	g.sess, g.none = n.sess, n.none
	g.dialCh, g.joinCh, g.sendCh, g.voiceCh = n.dialCh, n.joinCh, n.sendCh, n.voiceCh
	g.connect = newConnectForm("tester", "ws://", false)
	return g
}

func TestAddPageEntry(t *testing.T) {
	g := addTestGame(t)
	if g.add != addChoose {
		t.Fatalf("startup add = %v, want addChoose", g.add)
	}
	if !g.addPageShown() {
		t.Error("choice page not shown at startup")
	}
	g.sess["k1"] = newSession("k1", "ws://k1/", "me")
	g.view("k1")
	g.add = addJoin
	g.addServer()
	if g.add != addChoose || g.addPrev != "k1" || g.curKey != "" {
		t.Fatalf("add=%v prev=%q cur=%q", g.add, g.addPrev, g.curKey)
	}
	if !g.addPageShown() {
		t.Error("choice page not shown after +")
	}
}

func TestAddChoiceHits(t *testing.T) {
	g := addTestGame(t)
	g.chooseAdd(0)
	if g.add != addCreate {
		t.Fatalf("choice 0 -> %v", g.add)
	}
	g.add = addChoose
	g.chooseAdd(1)
	if g.add != addJoin || g.connect.focus != 1 {
		t.Fatalf("choice 1 -> %v focus %d", g.add, g.connect.focus)
	}
	g.add = addChoose
	g.hits.Reset()
	g.addAddHits()
	r := g.addChoiceRect(1)
	h, ok := g.hits.At((r.x0+r.x1)/2, (r.y0+r.y1)/2)
	if !ok || h.Kind != state.HitAddChoice || h.ID != 1 {
		t.Errorf("hit = %+v %v", h, ok)
	}
}

func TestAddEscNavigation(t *testing.T) {
	g := addTestGame(t)
	for _, m := range []addMode{addCreate, addJoin} {
		g.add = m
		g.escSpent = false
		g.addEsc(true)
		if g.add != addChoose || !g.escSpent {
			t.Errorf("esc from %v -> %v spent %v", m, g.add, g.escSpent)
		}
	}
	g.add, g.escSpent = addCreate, true
	g.addEsc(true) // already consumed: nothing happens
	if g.add != addCreate {
		t.Error("spent esc acted")
	}
	g.escSpent = false
	g.add = addChoose
	g.addEsc(true) // no servers: stays
	if g.add != addChoose || g.curKey != "" {
		t.Error("esc with nowhere to go moved")
	}
	g.sess["k1"] = newSession("k1", "ws://k1/", "me")
	g.view("k1")
	g.addServer()
	g.escSpent = false
	g.addEsc(true)
	if g.curKey != "k1" {
		t.Errorf("esc did not return to prev: cur=%q", g.curKey)
	}
}

func TestCreateValidates(t *testing.T) {
	g := addTestGame(t)
	g.createFn = func(string, string, int) error { t.Fatal("create called on invalid form"); return nil }
	g.cf = newCreateForm()
	g.submitCreate()
	if g.cf.err != "server name is required" {
		t.Errorf("err = %q", g.cf.err)
	}
	g.cf.name.runes = []rune("Den")
	g.cf.port.runes = []rune("70000")
	g.submitCreate()
	if g.cf.err != "port must be 1–65535" {
		t.Errorf("err = %q", g.cf.err)
	}
	g.cf.port.runes = nil
	g.submitCreate()
	if g.cf.err != "port must be 1–65535" {
		t.Errorf("empty port err = %q", g.cf.err)
	}
	g.cf = newCreateForm()
	g.cf.focus = 2
	g.cf.typed([]rune("a8b0c"))
	if got := g.cf.port.String(); got != "76978" { // letters ignored, five digits at most
		t.Errorf("port = %q", got)
	}
}

func TestCreateSubmits(t *testing.T) {
	g := addTestGame(t)
	var gotN, gotP string
	var gotPort int
	g.createFn = func(n, p string, port int) error {
		gotN, gotP, gotPort = n, p, port
		g.sess[hostedKey] = newSession(hostedKey, "ws://127.0.0.1:7697/", "me")
		return nil
	}
	g.cf = newCreateForm()
	g.cf.name.runes = []rune("Den")
	g.cf.pass.runes = []rune("pw")
	g.submitCreate()
	if gotN != "Den" || gotP != "pw" || gotPort != 7697 {
		t.Fatalf("create(%q,%q,%d)", gotN, gotP, gotPort)
	}
	if g.curKey != hostedKey {
		t.Errorf("view = %q, want hostedKey", g.curKey)
	}

	g = addTestGame(t)
	msg := portInUseMsg(7697)
	g.createFn = func(string, string, int) error { return errors.New(msg) }
	g.cf = newCreateForm()
	g.cf.name.runes = []rune("Den")
	g.add = addCreate
	g.submitCreate()
	if g.cf.err != msg || g.add != addCreate || g.curKey != "" {
		t.Errorf("err=%q add=%v cur=%q", g.cf.err, g.add, g.curKey)
	}
}

func TestCreateWhileHosting(t *testing.T) {
	g := addTestGame(t)
	g.hostedPath = filepath.Join(t.TempDir(), "hosted.toml")
	if err := os.WriteFile(g.hostedPath, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	g.hosted = &hostedServer{cfg: server.Config{ServerName: "Den"}, path: g.hostedPath}
	if got := g.createChoiceHint(); got != "already hosting Den" {
		t.Errorf("hint = %q", got)
	}
	g.chooseAdd(0)
	if g.add != addChoose {
		t.Errorf("mode changed to %v", g.add)
	}
	calls := 0
	g.createFn = func(string, string, int) error { calls++; return nil }
	g.cf = newCreateForm()
	g.cf.name.runes = []rune("Other")
	g.submitCreate()
	if calls != 0 || g.cf.err != "already hosting Den" {
		t.Errorf("calls=%d err=%q", calls, g.cf.err)
	}
	if b, _ := os.ReadFile(g.hostedPath); string(b) != "keep" {
		t.Errorf("hosted.toml touched: %q", b)
	}
}

func TestCreateInviteLookup(t *testing.T) {
	g := addTestGame(t)
	g.selfAddrs = func(context.Context) (string, string) { return "100.64.0.9", "" }
	g.chooseAdd(0)
	deadline := time.Now().Add(3 * time.Second)
	for !g.addInviteDone && time.Now().Before(deadline) {
		g.pumpAddInvite()
		time.Sleep(5 * time.Millisecond)
	}
	if got := g.createInviteText(); got != "invite: ws://100.64.0.9:7697/" {
		t.Errorf("invite = %q", got)
	}
}

func TestJoinArrowsFillAddress(t *testing.T) {
	f := newConnectForm("me", "ws://", false)
	f.disc = []state.DiscoveredRow{{Name: "a", URL: "ws://a:1/"}, {Name: "b", URL: "ws://b:1/"}}
	steps := []struct {
		d    int
		want string
	}{{1, "ws://a:1/"}, {1, "ws://b:1/"}, {-1, "ws://a:1/"}}
	for _, s := range steps {
		f.moveSel(s.d)
		if got := f.server.String(); got != s.want {
			t.Errorf("after %+d: %q, want %q", s.d, got, s.want)
		}
	}
}

func TestHelpHasFirewallLine(t *testing.T) {
	const want = "Windows will ask to allow HERMEC on your network — choose Allow so friends can connect."
	if firewallLine != want {
		t.Errorf("firewallLine = %q", firewallLine)
	}
	if !strings.Contains(helpSrc, want) {
		t.Error("help.txt lacks the firewall line")
	}
}

func TestLocalStartupOpensConnectForm(t *testing.T) {
	g := addTestGame(t)
	g.connect = newConnectForm("me", "ws://127.0.0.1:1/", true)
	g.initAdd()
	if g.add != addJoin || g.addPageShown() {
		t.Errorf("local: add=%v shown=%v", g.add, g.addPageShown())
	}
	g.escSpent = false
	g.addEsc(true)
	if g.add != addJoin {
		t.Error("esc left the local connect form")
	}
	g = addTestGame(t)
	g.initAdd()
	if g.add != addChoose || !g.addPageShown() {
		t.Errorf("non-local: add=%v", g.add)
	}
}

func TestFirewallLineWraps(t *testing.T) {
	g := addTestGame(t)
	g.w = 300
	cols := g.addCols()
	lines := g.firewallLines()
	if len(lines) < 2 {
		t.Fatalf("not wrapped: %v", lines)
	}
	for _, l := range lines {
		if n := len([]rune(l)); n > cols {
			t.Errorf("line %q is %d runes, budget %d", l, n, cols)
		}
	}
	if strings.Join(lines, " ") != firewallLine {
		t.Errorf("text changed: %q", lines)
	}
}
