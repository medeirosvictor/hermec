package ui

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"github.com/medeirosvictor/hermec/ui/state"
)

const oldServerNote = "member list needs a newer server"

// memberRows is the viewed server's member list as drawable rows, or a note
// when the server predates the roster.
func (g *game) memberRows() (rows []state.RosterRow, note string) {
	st := g.cur().st
	if !st.HasRoster {
		return nil, oldServerNote
	}
	return state.RosterRows(st.Roster, st.RosterTotal), ""
}

// rosterVisible is how many member rows fit in the panel below the header row.
func (g *game) rosterVisible(gm geometry) int {
	n := int((gm.membersR.y1 - pad - gm.paneY0) / gm.lh)
	if n < 1 {
		n = 1
	}
	return n
}

// memberRowColor colours one row: headings and "and N more" dim, the
// viewer's own row bright.
func (g *game) memberRowColor(r state.RosterRow) color.RGBA {
	switch {
	case r.Kind != state.RosterMember:
		return g.th.Dim
	case r.FP == g.fp:
		return g.th.Bright
	}
	return g.th.FG
}

// drawMembers draws the member list inside gm.membersR.
func (g *game) drawMembers(screen *ebiten.Image, gm geometry) {
	r := gm.membersR
	dst := clip(screen, r.x0, r.y0, r.x1, r.y1)
	x := r.x0 + pad
	cols := int((r.x1 - r.x0 - 2*pad) / gm.adv)
	rows, note := g.memberRows()
	if note != "" {
		g.drawText(dst, state.Ellipsize(note, cols), x, gm.paneY0, g.th.Dim)
		return
	}
	y := gm.paneY0
	for i := g.cur().ms.rosterScroll; i < len(rows); i++ {
		if y+gm.lh > r.y1-pad {
			break
		}
		g.drawText(dst, state.Ellipsize(rows[i].Text, cols), x, y, g.memberRowColor(rows[i]))
		y += gm.lh
	}
}

// drawMembersToggle draws the "members" button; bright while the list shows.
func (g *game) drawMembersToggle(screen *ebiten.Image, gm geometry) {
	c := g.th.Dim
	if gm.showMembers {
		c = g.th.Bright
	}
	const label = "members"
	t := gm.toggleR
	g.drawText(clip(screen, t.x0, t.y0, t.x1, t.y1), label, gm.toggleR.x1-text.Advance(label, g.face)-pad/2, gm.toggleR.y0, c)
}

// wheelMembers applies a wheel turn wy at (fx, fy) to the member list. It
// reports whether it consumed the wheel: the list is drawn and the cursor is
// over it. Wheel up scrolls toward the top, like the chat.
func (g *game) wheelMembers(s *session, gm geometry, fx, fy, wy float64) bool {
	r := gm.membersR
	if !gm.showMembers || !state.InRect(fx, fy, r.x0, r.y0, r.x1, r.y1) {
		return false
	}
	var n int
	n, s.ms.wheelCarry = state.WheelLines(s.ms.wheelCarry, wy, wheelLinesPerNotch)
	rows, _ := g.memberRows()
	s.ms.rosterScroll = state.ClampScroll(len(rows), g.rosterVisible(gm), s.ms.rosterScroll-n)
	return true
}

// clickMembersToggle handles a click on h; it reports whether it was the toggle.
func (g *game) clickMembersToggle(h state.Hit) bool {
	if h.Kind != state.HitMembersToggle {
		return false
	}
	g.toggleMembers()
	return true
}
