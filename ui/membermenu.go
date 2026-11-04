package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/ui/state"
)

// addMemberHits registers the member rows drawMembers draws, so a right press
// on one finds its fingerprint.
func (g *game) addMemberHits(gm geometry) {
	if !gm.showMembers {
		return
	}
	rows := g.memberRows()
	r := gm.membersR
	y := gm.paneY0
	for i := g.cur().ms.rosterScroll; i < len(rows); i++ {
		if y+gm.lh > r.y1-pad {
			break
		}
		if rows[i].Kind == state.RosterMember {
			g.hits.Add(state.Hit{X0: r.x0, Y0: y, X1: r.x1, Y1: y + gm.lh, Kind: state.HitMember, Key: rows[i].FP})
		}
		y += gm.lh
	}
}

// openMemberMenu opens the context menu for the person fp at (ax, ay): the
// founder's host controls (see hostItems) and, for anyone but yourself, the
// moderation rows (see modItems) and the playback volume steps.
func (g *game) openMemberMenu(fp string, ax, ay float64) {
	g.closeMenu()
	s := g.cur()
	items := g.hostItems(s, fp)
	items = append(items, g.modItems(s, fp)...)
	own := g.fp
	if s.c != nil {
		own = s.c.Fingerprint()
	}
	if fp != own {
		vol := g.volumeItems(fp)
		if len(items) > 0 {
			vol[0].sep = true
		}
		items = append(items, vol...)
	}
	if len(items) == 0 {
		return
	}
	for i := range items { // picking any item closes the menu first
		act := items[i].act
		items[i].act = func() { g.closeMenu(); act() }
	}
	w, h := g.menuSize(items)
	x, y := state.MenuOrigin(ax, ay, w, h, float64(g.w), float64(g.h))
	g.menu = ctxMenu{open: true, key: fp, x: x, y: y, items: items}
}

// hostItems are the founder's host controls for the member fp: Allow hosting
// for a non-host, Stop hosting and Move up for a host other than the founder
// (Move up only when the host above is not the founder, whose priority 1
// never swaps). Anyone but the founder gets none.
func (g *game) hostItems(s *session, fp string) []menuItem {
	if s.c == nil {
		return nil
	}
	cs := s.c.Chain().State()
	if cs.Founder == "" || cs.Founder != s.c.Fingerprint() || fp == cs.Founder {
		return nil
	}
	idx := -1
	maxPrio := 1
	for i, h := range cs.Hosts {
		if h.Fingerprint == fp {
			idx = i
		}
		maxPrio = max(maxPrio, h.Priority)
	}
	var items []menuItem
	if idx < 0 {
		var pub []byte
		for _, m := range s.st.Roster {
			if m.Fingerprint == fp {
				pub = m.PubKey
			}
		}
		if len(pub) == 0 {
			return nil
		}
		items = append(items, menuItem{label: "Allow hosting", act: func() {
			g.chainOp(s.key, chainStep{chain.OpHostAdd, chain.HostAddBody{PubKey: pub, Priority: maxPrio + 1}})
		}})
	} else {
		items = append(items, menuItem{label: "Stop hosting", act: func() {
			g.chainOp(s.key, chainStep{chain.OpHostRemove, chain.FPBody{Fingerprint: fp}})
		}})
		// Never swap with the founder: priority 1 stays theirs, gaps or not.
		if h := cs.Hosts[idx]; idx > 0 && cs.Hosts[idx-1].Fingerprint != cs.Founder {
			above := cs.Hosts[idx-1]
			items = append(items, menuItem{label: "Move up", act: func() {
				// Swap with the host above, the lower one first.
				g.chainOp(s.key,
					chainStep{chain.OpHostAdd, chain.HostAddBody{PubKey: h.PubKey, Priority: above.Priority}},
					chainStep{chain.OpHostAdd, chain.HostAddBody{PubKey: above.PubKey, Priority: h.Priority}})
			}})
		}
	}
	return items
}

// chainStep is one chain append of a host change.
type chainStep struct {
	op   chain.Op
	body any
}

// chainOp runs the appends in order on session key's client in the background
// (ChainAppend waits on the network), stopping at the first error, which comes
// back on sendCh and shows as a notice.
func (g *game) chainOp(key string, steps ...chainStep) {
	s := g.sess[key]
	if s == nil || s.c == nil {
		return
	}
	c, ch := s.c, g.sendCh
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, st := range steps {
			if err := c.ChainAppend(ctx, st.op, st.body); err != nil {
				ch <- sendResult{key: key, err: err, what: "host change"}
				return
			}
		}
	}()
}

// modItems are the moderation rows of the person fp's menu: Make/Remove admin
// and Ban…, only for a viewer who may manage and never for yourself or the
// founder (see state.ModItems). The server enforces the same rules.
func (g *game) modItems(s *session, fp string) []menuItem {
	if s.c == nil {
		return nil
	}
	var items []menuItem
	for _, it := range state.ModItems(s.c.Chain().State(), s.c.Fingerprint(), fp) {
		mi := menuItem{label: it.Label, warn: it.Confirm}
		if it.Confirm {
			mi.act = func() { g.openBanDialog(s.key, it) }
		} else {
			mi.act = func() { g.modOp(s.key, it) }
		}
		items = append(items, mi)
	}
	return items
}

// memberName is the name to show for fp on session s: the roster or history
// name, else the fingerprint.
func (g *game) memberName(s *session, fp string) string {
	if n := state.KnownNames(s.st)[fp]; n != "" {
		return n
	}
	return fp
}

// maxBannedRows caps the banned-members menu so it fits the window.
const maxBannedRows = 12

// bannedItem is the server tile's "Banned members…" row, for managers only.
func (g *game) bannedItem(key string, ax, ay float64) (menuItem, bool) {
	s := g.sess[key]
	if s == nil || s.c == nil || !state.IsManager(s.c.Chain().State(), s.c.Fingerprint()) {
		return menuItem{}, false
	}
	return menuItem{label: "Banned members…", act: func() { g.openBannedMenu(key, ax, ay) }}, true
}

// openBannedMenu lists the banned fingerprints as menu rows; picking a row
// unbans that person.
func (g *game) openBannedMenu(key string, ax, ay float64) {
	g.closeMenu()
	s := g.sess[key]
	if s == nil || s.c == nil {
		return
	}
	rows := state.BannedRows(s.c.Chain().State(), s.c.Fingerprint(), state.KnownNames(s.st))
	var items []menuItem
	for i, r := range rows {
		if i == maxBannedRows {
			items = append(items, menuItem{label: fmt.Sprintf("and %d more", len(rows)-i), act: func() {}})
			break
		}
		items = append(items, menuItem{label: "Unban " + state.Ellipsize(r.Label, 48), act: func() { g.modOp(key, r.Item) }})
	}
	if len(items) == 0 {
		items = []menuItem{{label: "No banned members", act: func() {}}}
	}
	for i := range items {
		act := items[i].act
		items[i].act = func() { g.closeMenu(); act() }
	}
	w, h := g.menuSize(items)
	x, y := state.MenuOrigin(ax, ay, w, h, float64(g.w), float64(g.h))
	g.menu = ctxMenu{open: true, key: key, x: x, y: y, items: items}
}

// modOp appends one moderation entry on session key's client in the
// background; the outcome comes back on sendCh as a friendly status line and
// the raw error goes to the log.
func (g *game) modOp(key string, it state.ModItem) {
	s := g.sess[key]
	if s == nil || s.c == nil {
		return
	}
	c, ch, name := s.c, g.sendCh, g.memberName(s, it.Target)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := c.ChainAppend(ctx, it.Op, it.Body)
		msg := state.ModOutcome(it, name, err)
		r := sendResult{key: key, err: err, what: "moderation", info: msg}
		if err != nil {
			r.info, r.failMsg = "", msg
		}
		ch <- r
	}()
}
