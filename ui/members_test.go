package ui

import (
	"path/filepath"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
)

func boolp(b bool) *bool { return &b }

func TestMembersGeomShown(t *testing.T) {
	g := mainGame(t)
	on := g.geom()
	g.set.Members = boolp(false)
	off := g.geom()
	g.set.Members = nil
	if !on.showMembers {
		t.Fatal("not shown")
	}
	if on.membersR.x1-on.membersR.x0 != membersPaneW || on.chatR.x1 != on.membersR.x0-pad || on.inputR.x1 != on.chatR.x1 {
		t.Errorf("columns: %+v", on)
	}
	if on.membersR.y0 != on.chatR.y0 || on.membersR.y1 != on.inputR.y1 {
		t.Errorf("rows: %+v", on)
	}
	if off.rightW-on.rightW != membersPaneW+pad {
		t.Errorf("rightW %v -> %v", off.rightW, on.rightW)
	}
	if want := int(on.rightW / on.adv); on.cols != want || on.cols >= off.cols {
		t.Errorf("cols %d (want %d), hidden %d", on.cols, want, off.cols)
	}
}

func TestMembersGeomSettingOff(t *testing.T) {
	g := mainGame(t)
	g.set.Members = boolp(false)
	gm := g.geom()
	if gm.showMembers || gm.membersR != (rectF{}) {
		t.Errorf("shown: %+v", gm.membersR)
	}
	if gm.chatR.x1 != float64(g.w)-margin || gm.toggleR == (rectF{}) {
		t.Errorf("chat %v toggle %v", gm.chatR.x1, gm.toggleR)
	}
}

func TestMembersAutoHide(t *testing.T) {
	g := mainGame(t)
	var last geometry
	hid := false
	for w := 960; w >= 400; w-- {
		g.w = w
		gm := g.geom()
		if gm.showMembers && gm.cols < minChatCols {
			t.Fatalf("w=%d shown with %d cols", w, gm.cols)
		}
		if !gm.showMembers {
			hid = true
			would := int((float64(w) - margin - membersPaneW - pad - pad - gm.rightX) / gm.adv)
			if would >= minChatCols {
				t.Errorf("w=%d hidden but would have %d cols", w, would)
			}
			if gm.membersR != (rectF{}) || g.set.Members != nil {
				t.Error("rect or setting touched")
			}
			if last.cols < minChatCols {
				t.Errorf("last shown width has %d cols", last.cols)
			}
			if gm.toggleR.x1 != float64(w)-margin {
				t.Error("toggle moved off the right edge")
			}
			break
		}
		last = gm
	}
	if !hid {
		t.Fatal("never hid")
	}
}

func TestMembersToggleHit(t *testing.T) {
	g := mainGame(t)
	for _, w := range []int{960, 500} {
		g.w = w
		g.buildHits()
		gm := g.geom()
		if gm.showMembers != (w == 960) {
			t.Errorf("w=%d showMembers=%v", w, gm.showMembers)
		}
		cx, cy := center(gm.toggleR)
		if h, ok := g.hits.At(cx, cy); !ok || h.Kind != state.HitMembersToggle {
			t.Errorf("w=%d centre: %v %v", w, h, ok)
		}
		if h, ok := g.hits.At(gm.toggleR.x0-2, cy); ok && h.Kind == state.HitMembersToggle {
			t.Errorf("w=%d outside hit", w)
		}
		if got := shapeAt(&g.hits, cx, cy); got != ebiten.CursorShapePointer {
			t.Errorf("w=%d shape %v", w, got)
		}
	}
}

func TestToggleMembersPersists(t *testing.T) {
	g := mainGame(t)
	g.settingsPath = filepath.Join(t.TempDir(), "settings.toml")
	g.toggleMembers()
	s, err := state.LoadSettings(g.settingsPath)
	if err != nil || g.set.MembersShown() || s.MembersShown() {
		t.Fatalf("off: %v %v", err, s)
	}
	g.toggleMembers()
	if s, _ := state.LoadSettings(g.settingsPath); !g.set.MembersShown() || !s.MembersShown() {
		t.Fatal("on")
	}
	g.settingsPath = ""
	g.toggleMembers()
}

func TestMembersPanelRows(t *testing.T) {
	g := mainGame(t)
	if rows, note := g.memberRows(); len(rows) != 0 || note != "member list needs a newer server" {
		t.Errorf("old server: %v %q", rows, note)
	}
	st := g.cur().st
	st.Roster = []proto.Member{{Name: "ann", Fingerprint: "a"}, {Name: "bob", Fingerprint: "b"}}
	st.RosterTotal, st.HasRoster = 5, true
	rows, note := g.memberRows()
	last := rows[len(rows)-1]
	if note != "" || last.Kind != state.RosterMore || last.Text != "and 3 more" {
		t.Errorf("rows %v note %q", rows, note)
	}
}

func rosterGame(t *testing.T, n int) (*game, geometry) {
	g := mainGame(t)
	st := g.cur().st
	for i := 0; i < n; i++ {
		st.Roster = append(st.Roster, proto.Member{Name: "m", Fingerprint: string(rune('a' + i))})
	}
	st.RosterTotal, st.HasRoster = n, true
	return g, g.geom()
}

func TestMembersWheel(t *testing.T) {
	g, gm := rosterGame(t, 80)
	s := g.cur()
	rows, _ := g.memberRows()
	max := state.ClampScroll(len(rows), g.rosterVisible(gm), 1<<20)
	if max == 0 {
		t.Fatal("list fits; need overflow")
	}
	cx, cy := center(gm.membersR)
	if !g.wheelMembers(s, gm, cx, cy, -2) || s.ms.rosterScroll != 6 {
		t.Fatalf("wheel down: scroll %d", s.ms.rosterScroll)
	}
	g.wheelMembers(s, gm, cx, cy, 1)
	if s.ms.rosterScroll != 3 {
		t.Errorf("wheel up: scroll %d, want 3", s.ms.rosterScroll)
	}
	g.wheelMembers(s, gm, cx, cy, -1e6)
	if s.ms.rosterScroll != max {
		t.Errorf("huge down: %d, want %d", s.ms.rosterScroll, max)
	}
	g.wheelMembers(s, gm, cx, cy, 1e6)
	if s.ms.rosterScroll != 0 {
		t.Errorf("huge up: %d, want 0", s.ms.rosterScroll)
	}
}

func TestMembersWheelNotConsumed(t *testing.T) {
	g, gm := rosterGame(t, 80)
	s := g.cur()
	s.ms.scroll = 7
	if g.wheelMembers(s, gm, gm.chatR.x0+pad, gm.chatR.y0+gm.lh, -3) {
		t.Error("consumed over the chat")
	}
	if s.ms.rosterScroll != 0 || s.ms.scroll != 7 {
		t.Errorf("scrolls moved: %d %d", s.ms.rosterScroll, s.ms.scroll)
	}
	cx, cy := center(gm.membersR)
	g.set.Members = boolp(false)
	off := g.geom()
	if g.wheelMembers(s, off, cx, cy, -3) || s.ms.rosterScroll != 0 {
		t.Error("consumed with the setting off")
	}
	g.set.Members = nil
	g.w = 500
	narrow := g.geom()
	if narrow.showMembers || g.wheelMembers(s, narrow, cx, cy, -3) || s.ms.rosterScroll != 0 {
		t.Error("consumed while auto-hidden")
	}
}

func TestMembersScrollPerSession(t *testing.T) {
	g, _ := rosterGame(t, 5)
	g.cur().ms.rosterScroll = 4
	other := newSession("o", "ws://o/", "me")
	g.sess[other.key] = other
	g.view(other.key)
	if g.cur().ms.rosterScroll != 0 {
		t.Error("scroll leaked across sessions")
	}
}

func TestClickMembersToggle(t *testing.T) {
	g := mainGame(t)
	if g.clickMembersToggle(state.Hit{Kind: state.HitChannel}) || !g.set.MembersShown() {
		t.Fatal("other hit changed the setting")
	}
	if !g.clickMembersToggle(state.Hit{Kind: state.HitMembersToggle}) || g.set.MembersShown() {
		t.Fatal("toggle hit did not hide the list")
	}
}

func TestMembersToggleNoOverlap(t *testing.T) {
	g := mainGame(t)
	for _, fs := range []float64{g.th.FontSize, g.th.FontSize * 1.5} {
		g.th.FontSize = fs
		face, err := theme.Face("vt323", fs)
		if err != nil {
			t.Skip(err)
		}
		g.face = face
		for w := 1200; w >= 300; w -= 7 {
			g.w = w
			gm := g.geom()
			tr, sr := gm.toggleR, g.signalRect(gm)
			if tr.y0 < sr.y1 && sr.y0 < tr.y1 && tr.x0 < sr.x1 && sr.x0 < tr.x1 {
				t.Fatalf("fs=%v w=%d toggle overlaps signal bars", fs, w)
			}
			if tr.x0 < gm.chanR.x1 {
				t.Fatalf("fs=%v w=%d toggle over the channel pane/header", fs, w)
			}
			if tr.x1 != float64(w)-margin {
				t.Fatalf("fs=%v w=%d toggle right edge moved", fs, w)
			}
		}
	}
}

func TestOwnRowBright(t *testing.T) {
	g := mainGame(t)
	g.fp = "me"
	th := g.th
	cases := []struct {
		r    state.RosterRow
		want any
	}{
		{state.RosterRow{Kind: state.RosterMember, FP: "me"}, th.Bright},
		{state.RosterRow{Kind: state.RosterMember, FP: "x"}, th.FG},
		{state.RosterRow{Kind: state.RosterHeading, FP: "me"}, th.Dim},
		{state.RosterRow{Kind: state.RosterMore}, th.Dim},
	}
	for i, c := range cases {
		if got := g.memberRowColor(c.r); got != c.want {
			t.Errorf("case %d: %v want %v", i, got, c.want)
		}
	}
}
