package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/ui/state"
)

func renameGame(t *testing.T) *game {
	t.Helper()
	g := menuGame(t, false)
	g.servers = []state.ServerEntry{{URL: "k", Nick: "Old", Name: "Primos"}}
	return g
}

func pickMenu(g *game, key, label string) {
	g.openTileMenu(key, 10, 70)
	for _, it := range g.menu.items {
		if it.label == label {
			it.act()
			return
		}
	}
}

func setNick(g *game, s string) { g.dlg.nick.runes = []rune(s) }

func TestMenuRenameOpensDialogPrefilled(t *testing.T) {
	g := renameGame(t)
	pickMenu(g, "k", "Rename")
	if g.menu.open || g.dlg.mode != dlgRename || g.dlg.nick.String() != "Old" {
		t.Fatalf("menu open %v dlg %+v", g.menu.open, g.dlg)
	}
	if got := g.dlg.title(g.servers[0]); got != "Rename Old" {
		t.Fatalf("title = %q", got)
	}
}

func TestRenameSaveSetsNickAndPersists(t *testing.T) {
	g := renameGame(t)
	g.openRenameDialog("k")
	for i := 0; i < 3; i++ {
		g.dialogKey(false, false, nil, true)
	}
	g.dialogKey(false, false, []rune("Lab"), false)
	g.dialogConfirm()
	if g.servers[0].Nick != "Lab" || g.dialogOpen() {
		t.Fatalf("nick %q open %v", g.servers[0].Nick, g.dialogOpen())
	}
	got, err := state.LoadServers(g.serversPath)
	if err != nil || len(got) != 1 || got[0].Nick != "Lab" {
		t.Fatalf("saved %+v err %v", got, err)
	}
}

func TestRenameWhitespaceClearsNick(t *testing.T) {
	g := renameGame(t)
	g.openRenameDialog("k")
	setNick(g, "   ")
	g.dialogConfirm()
	if g.servers[0].Nick != "" {
		t.Fatalf("nick = %q", g.servers[0].Nick)
	}
	g.openRenameDialog("k")
	setNick(g, "")
	g.dialogKey(false, false, []rune(strings.Repeat("x", 40)), false)
	if n := len(g.dlg.nick.runes); n != state.MaxNickRunes {
		t.Fatalf("field holds %d runes, want %d", n, state.MaxNickRunes)
	}
	g.dialogConfirm()
	if n := len([]rune(g.servers[0].Nick)); n != state.MaxNickRunes {
		t.Fatalf("nick has %d runes", n)
	}
}

func TestRenameEmptyResets(t *testing.T) {
	g := renameGame(t)
	g.openRenameDialog("k")
	setNick(g, "")
	g.dialogConfirm()
	if g.servers[0].Nick != "" || state.DisplayName(g.servers[0]) != "Primos" {
		t.Fatalf("entry %+v", g.servers[0])
	}
}

func TestRenameCancelKeepsNick(t *testing.T) {
	g := renameGame(t)
	g.openRenameDialog("k")
	setNick(g, "Other")
	g.dialogCancel()
	if g.servers[0].Nick != "Old" || g.dialogOpen() {
		t.Fatalf("nick %q open %v", g.servers[0].Nick, g.dialogOpen())
	}
	if _, err := os.Stat(g.serversPath); !os.IsNotExist(err) {
		t.Fatalf("cancel wrote the servers file (err %v)", err)
	}
}

func TestLeaveRemovesWithSession(t *testing.T) {
	g := menuGame(t, true)
	g.servers = append(g.servers, state.ServerEntry{URL: "m", Label: "m"})
	g.sess["m"] = newSession("m", "m", "me")
	g.view("k")
	g.tip = tooltipState{idx: 1, show: true}
	g.openLeaveDialog("k")
	g.dialogConfirm()
	if g.sess["k"] != nil || len(g.servers) != 1 || g.servers[0].URL != "m" || g.curKey != "m" {
		t.Fatalf("sess %v servers %+v cur %q", g.sess, g.servers, g.curKey)
	}
	got, err := state.LoadServers(g.serversPath)
	if err != nil || len(got) != 1 || got[0].URL != "m" {
		t.Fatalf("saved %+v err %v", got, err)
	}
	if g.tip != (tooltipState{}) || g.hover != (state.Hover{}) || g.dialogOpen() {
		t.Fatalf("tip %+v hover %+v not reset", g.tip, g.hover)
	}
}

func TestLeaveWithoutSession(t *testing.T) {
	g := menuGame(t, false)
	g.openLeaveDialog("k")
	g.dialogConfirm()
	if len(g.servers) != 0 || g.dialogOpen() {
		t.Fatalf("servers %+v", g.servers)
	}
}

func TestLeaveEndsCall(t *testing.T) {
	g := newTestGame(t)
	withLayout(t, g)
	_, a, b := twoSessions(t, g)
	g.servers = []state.ServerEntry{{URL: a.key}, {URL: b.key}}
	src, sink := callOn(g, a, "voice")
	g.openLeaveDialog(a.key)
	g.dialogConfirm()
	waitClosed(t, src, sink)
	if g.callKey != "" || len(g.servers) != 1 || g.servers[0].URL != b.key {
		t.Fatalf("callKey %q servers %+v", g.callKey, g.servers)
	}
}

func TestLeaveViewedFallsBackToConnectScene(t *testing.T) {
	g := menuGame(t, true)
	g.view("k")
	g.openLeaveDialog("k")
	g.dialogConfirm()
	if g.curKey != "" {
		t.Fatalf("curKey = %q", g.curKey)
	}
}

func TestLeaveLocalServer(t *testing.T) {
	g := menuGame(t, false)
	g.servers = []state.ServerEntry{{URL: localKey, Label: "local"}}
	g.openLeaveDialog(localKey)
	g.dialogConfirm()
	if len(g.servers) != 0 {
		t.Fatalf("servers %+v", g.servers)
	}
}

func TestRenameSaveFailureStatus(t *testing.T) {
	g := renameGame(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	g.serversPath = filepath.Join(blocker, "servers.toml")
	g.openRenameDialog("k")
	setNick(g, "Lab")
	g.dialogConfirm()
	if g.servers[0].Nick != "Lab" || g.notice == "" {
		t.Fatalf("nick %q notice %q", g.servers[0].Nick, g.notice)
	}
}

func center(r rectF) (float64, float64) { return (r.x0 + r.x1) / 2, (r.y0 + r.y1) / 2 }

func TestDialogPressRules(t *testing.T) {
	cases := []struct {
		name     string
		pt       func(g *game) (float64, float64)
		wantOpen bool
		wantNick string
	}{
		{"primary", func(g *game) (float64, float64) { return center(g.dialogButtonRect(0)) }, false, "New"},
		{"cancel", func(g *game) (float64, float64) { return center(g.dialogButtonRect(1)) }, false, "Old"},
		{"inside", func(g *game) (float64, float64) { r := g.dialogRect(); return r.x0 + 2, r.y0 + 2 }, true, "Old"},
		{"outside", func(g *game) (float64, float64) { return 1, float64(g.h) - 1 }, false, "Old"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := renameGame(t)
			g.openRenameDialog("k")
			setNick(g, "New")
			x, y := tc.pt(g)
			g.hits.Reset() // a voice row under the click, dialog buttons on top as buildHits orders them
			g.hits.Add(state.Hit{X0: x - 1, Y0: y - 1, X1: x + 1, Y1: y + 1, Kind: state.HitVoice, Key: "voice"})
			g.addDialogHits()
			g.dialogPress(x, y)
			if g.dialogOpen() != tc.wantOpen || g.servers[0].Nick != tc.wantNick {
				t.Fatalf("open %v nick %q", g.dialogOpen(), g.servers[0].Nick)
			}
			if h, ok := g.hits.At(x, y); ok {
				t.Fatalf("press not spent: %+v", h)
			}
			if g.vc.busy {
				t.Fatal("voice join started")
			}
		})
	}
}

func TestDialogHitsWin(t *testing.T) {
	g := renameGame(t)
	g.openRenameDialog("k")
	g.buildHits()
	for i := 0; i < 2; i++ {
		x, y := center(g.dialogButtonRect(i))
		h, ok := g.hits.At(x, y)
		if !ok || h.Kind != state.HitDialogBtn || h.ID != i {
			t.Fatalf("button %d: hit %+v ok %v", i, h, ok)
		}
	}
}

func TestDialogEscEnterKeys(t *testing.T) {
	g := renameGame(t)
	g.openRenameDialog("k")
	g.dialogKey(false, false, []rune{'a', 0x01, 0x7f, '\n', 'b'}, false)
	if got := g.dlg.nick.String(); got != "Oldab" {
		t.Fatalf("field = %q", got)
	}
	g.dialogKey(true, false, nil, false)
	if g.dialogOpen() || !g.escSpent || g.servers[0].Nick != "Old" {
		t.Fatalf("esc: open %v spent %v nick %q", g.dialogOpen(), g.escSpent, g.servers[0].Nick)
	}
	g.openRenameDialog("k")
	g.dialogKey(false, true, []rune("x"), false)
	if g.dialogOpen() || g.servers[0].Nick != "Oldx" {
		t.Fatalf("enter: open %v nick %q", g.dialogOpen(), g.servers[0].Nick)
	}
}

func TestDialogRectInsideSmallWindow(t *testing.T) {
	for _, mode := range []dialogMode{dlgRename, dlgLeave} {
		g := renameGame(t)
		g.w, g.h = 200, 150
		g.dlg = dialog{mode: mode, key: "k"}
		r := g.dialogRect()
		if r.x0 < 0 || r.y0 < 0 || r.x1 > 200 || r.y1 > 150 {
			t.Fatalf("mode %d rect %+v outside window", mode, r)
		}
		for i := 0; i < 2; i++ {
			b := g.dialogButtonRect(i)
			if b.x0 < r.x0 || b.y0 < r.y0 || b.x1 > r.x1 || b.y1 > r.y1 {
				t.Fatalf("button %d %+v outside panel %+v", i, b, r)
			}
		}
	}
}

// A dialog only blocks input: the per-tick network work still runs.
func TestDialogKeepsSessionsPumping(t *testing.T) {
	g := renameGame(t)
	g.openRenameDialog("k")
	s := newSession("z", "z", "me")
	s.dialing = true
	g.sess["z"] = s
	g.dialCh <- dialResult{key: "z", s: s, err: os.ErrNotExist}
	g.tickBackground(time.Now())
	if s.dialing {
		t.Fatal("dial result not applied while the dialog is open")
	}
}
