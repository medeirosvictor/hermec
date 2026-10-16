package ui

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/ui/state"
)

// A saved local entry in a non-local run must report an error without
// leaving a session behind or the game stuck in a dialing state.
func TestStartDialLocalNotRunningDoesNotWedge(t *testing.T) {
	g := newTestGame(t)
	if s := g.openSession(localKey, "me"); s != nil {
		t.Fatalf("openSession = %+v, want nil", s)
	}
	if len(g.sess) != 0 {
		t.Fatalf("sessions = %d, want 0", len(g.sess))
	}
	if g.none.st.ConnectErr == "" {
		t.Fatal("want ConnectErr")
	}
	g.addServer()
	if g.curKey != "" || g.connect.focus != 1 {
		t.Fatal("addServer did not run")
	}
}

func TestTileFacts(t *testing.T) {
	g := newTestGame(t)
	mk := func(key string) *session {
		s := newSession(key, key, "me")
		g.sess[key] = s
		return s
	}
	// The zero clients are not closable; drop them before newTestGame's cleanup.
	t.Cleanup(func() {
		for _, s := range g.sess {
			s.c = nil
		}
	})
	up := func(s *session) {
		s.c = &client.Client{}
		s.everUp = true
	}
	view := mk("view")
	up(view)
	bg := mk("bg")
	bg.everUp = true // c == nil: reconnecting
	call := mk("call")
	up(call)
	unread := mk("unread")
	up(unread)
	unread.st.Unread = map[string]bool{"general": true}
	first := mk("first") // first dial, never connected
	first.dialing = true
	g.curKey, g.callKey = "view", "call"

	cases := map[string]state.TileFacts{
		"view":   {Viewed: true, HasSession: true},
		"bg":     {HasSession: true, Reconnecting: true},
		"call":   {HasSession: true, InCall: true},
		"unread": {HasSession: true, Unread: true},
		"first":  {HasSession: true},
		"nosess": {},
	}
	for k, want := range cases {
		if got := g.tileFacts(state.ServerEntry{URL: k}); got != want {
			t.Errorf("%s: got %+v want %+v", k, got, want)
		}
	}
}

func connectTo(t *testing.T, g *game, url string) *session {
	t.Helper()
	s := g.openSession(url, "me")
	g.view(s.key)
	pumpUntil(t, g, connected(s))
	return s
}

func TestSaveServerStoresCleanedServerName(t *testing.T) {
	g := newTestGame(t)
	_, url := startServerNamed(t, "127.0.0.1:0", "Primos")
	connectTo(t, g, url)
	if len(g.servers) != 1 || g.servers[0].Name != "Primos" {
		t.Fatalf("servers = %+v", g.servers)
	}
	disk, err := state.LoadServers(g.serversPath)
	if err != nil || len(disk) != 1 || disk[0].Name != "Primos" {
		t.Fatalf("saved = %+v, %v", disk, err)
	}

	// A server that announces no name keeps the stored one.
	g2 := newTestGame(t)
	_, url2 := startServerNamed(t, "127.0.0.1:0", "")
	g2.servers = []state.ServerEntry{{URL: url2, Name: "Primos"}}
	connectTo(t, g2, url2)
	if len(g2.servers) != 1 || g2.servers[0].Name != "Primos" {
		t.Fatalf("name not kept: %+v", g2.servers)
	}
}

func TestSaveServerCleansHostileName(t *testing.T) {
	g := newTestGame(t)
	_, url := startServerNamed(t, "127.0.0.1:0", "\x1b[2J"+strings.Repeat("x", 100))
	s := connectTo(t, g, url)
	got := serverNameOf(s.c)
	if n := len([]rune(got)); n != state.MaxServerNameRunes {
		t.Errorf("len = %d, want %d", n, state.MaxServerNameRunes)
	}
	for _, r := range got {
		if unicode.IsControl(r) {
			t.Fatalf("control rune %q in %q", r, got)
		}
	}
	if g.servers[0].Name != got {
		t.Errorf("stored %q, want %q", g.servers[0].Name, got)
	}

	g2 := newTestGame(t)
	_, url2 := startServerNamed(t, "127.0.0.1:0", "  \x1b ")
	s2 := connectTo(t, g2, url2)
	if got := serverNameOf(s2.c); got != "" {
		t.Errorf("blank name = %q, want empty", got)
	}
	if n, want := state.DisplayName(g2.servers[0]), state.HostPort(url2); n != want {
		t.Errorf("DisplayName = %q, want host %q", n, want)
	}
}

func TestStepTooltip(t *testing.T) {
	g := newTestGame(t)
	g.servers = []state.ServerEntry{{URL: "ws://a/"}, {URL: "ws://b/"}}
	t0 := time.Unix(100, 0)
	tile := state.Hit{Kind: state.HitRailTile, ID: 0}
	g.stepTooltip(tile, true, t0)
	if g.tip.show {
		t.Fatal("shown at once")
	}
	g.stepTooltip(tile, true, t0.Add(state.TooltipDelay))
	if !g.tip.show || g.tip.idx != 0 {
		t.Fatalf("tip = %+v, want shown on 0", g.tip)
	}
	g.menu.open = true
	g.stepTooltip(tile, true, t0.Add(time.Second))
	if g.tip.show {
		t.Error("shown under an open menu")
	}
	g.menu.open = false
	g.stepTooltip(tile, true, t0.Add(2*time.Second))
	if !g.tip.show {
		t.Fatal("not shown again after the menu closed")
	}
	g.dlg.mode = dlgRename
	g.stepTooltip(tile, true, t0.Add(3*time.Second))
	if g.tip.show {
		t.Error("shown under an open dialog")
	}
	g.dlg.mode = dlgNone
	add := state.Hit{Kind: state.HitRailAdd}
	g.stepTooltip(add, true, t0.Add(4*time.Second))
	g.stepTooltip(add, true, t0.Add(5*time.Second))
	if g.tip.show {
		t.Error("shown on a non-tile hit")
	}
	g.stepTooltip(tile, true, t0.Add(6*time.Second))
	g.stepTooltip(state.Hit{}, false, t0.Add(7*time.Second))
	if g.tip.show {
		t.Error("shown with no hit")
	}
}

func TestTooltipSurvivesRemoval(t *testing.T) {
	g := newTestGame(t)
	g.servers = []state.ServerEntry{{URL: "ws://a/", Name: "A"}, {URL: "ws://b/", Name: "B"}}
	g.tip = tooltipState{idx: 1, show: true}
	if s, ok := g.tooltipText(); !ok || s != "B" {
		t.Fatalf("tooltipText = %q, %v", s, ok)
	}
	g.servers = state.Remove(g.servers, "ws://b/")
	if s, ok := g.tooltipText(); ok || s != "" {
		t.Fatalf("after removal tooltipText = %q, %v", s, ok)
	}
}
