package ui

import (
	"context"
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
	rows, note := g.memberRows()
	if note != "" {
		return
	}
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

// openMemberMenu opens the founder's host controls for the member fp at
// (ax, ay): Allow hosting for a non-host, Stop hosting and Move up for a host
// other than the founder (Move up only when the host above is not the
// founder, whose priority 1 never swaps). Anyone but the founder gets no menu.
func (g *game) openMemberMenu(fp string, ax, ay float64) {
	g.closeMenu()
	s := g.cur()
	if s.c == nil {
		return
	}
	cs := s.c.Chain().State()
	if cs.Founder == "" || cs.Founder != s.c.Fingerprint() || fp == cs.Founder {
		return
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
			return
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
	for i := range items { // picking any item closes the menu first
		act := items[i].act
		items[i].act = func() { g.closeMenu(); act() }
	}
	w, h := g.menuSize(items)
	x, y := state.MenuOrigin(ax, ay, w, h, float64(g.w), float64(g.h))
	g.menu = ctxMenu{open: true, key: fp, x: x, y: y, items: items}
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
