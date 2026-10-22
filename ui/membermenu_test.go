package ui

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
)

// memberMenuGame views a session on the founder's live client with a roster of
// the founder plus ids as plain users.
func memberMenuGame(t *testing.T, f *founderSrv, ids ...*identity.Identity) *game {
	t.Helper()
	g := newTestGame(t)
	withLayout(t, g)
	s := newSession("k", f.url, "me")
	s.c = f.c
	g.sess[s.key] = s
	g.servers = []state.ServerEntry{{URL: s.key, Label: "k"}}
	g.view(s.key)
	s.st.SetConnected([]proto.ChannelInfo{{Name: "general"}}, f.c.Fingerprint())
	ms := []proto.Member{{Fingerprint: f.c.Fingerprint(), Name: "founder", Roles: []string{"admin"}}}
	for i, id := range ids {
		ms = append(ms, proto.Member{Fingerprint: identity.Fingerprint(id.PublicKey()), Name: string(rune('b' + i)), Roles: []string{"user"}, PubKey: []byte(id.PublicKey())})
	}
	s.st.Apply(client.Event{Roster: &proto.Roster{Members: ms, Total: len(ms)}}, true)
	g.buildHits()
	return g
}

// rightClickMember right-clicks fp's roster row; false when no row hit has fp.
func rightClickMember(g *game, fp string) bool {
	gm := g.geom()
	x := (gm.membersR.x0 + gm.membersR.x1) / 2
	for y := gm.membersR.y0; y < gm.membersR.y1; y += 2 {
		if h, ok := g.hits.At(x, y); ok && h.Kind == state.HitMember && h.Key == fp {
			g.clickRail(h, true, true, x, y)
			return true
		}
	}
	return false
}

func chainHosts(f *founderSrv) map[string]int {
	out := map[string]int{}
	for _, h := range f.c.Chain().State().Hosts {
		out[h.Fingerprint] = h.Priority
	}
	return out
}

func appendHost(t *testing.T, f *founderSrv, id *identity.Identity, prio int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.c.ChainAppend(ctx, chain.OpHostAdd, chain.HostAddBody{PubKey: []byte(id.PublicKey()), Priority: prio}); err != nil {
		t.Fatal(err)
	}
}

func menuAct(g *game, label string) {
	for _, it := range g.menu.items {
		if it.label == label {
			it.act()
			return
		}
	}
}

func waitHosts(t *testing.T, f *founderSrv, cond func(map[string]int) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond(chainHosts(f)) {
		if time.Now().After(deadline) {
			t.Fatalf("hosts = %v", chainHosts(f))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMemberMenuFounderItems(t *testing.T) {
	f := startFounder(t, "")
	m := mustIdentity(t)
	g := memberMenuGame(t, f, m)
	mfp := identity.Fingerprint(m.PublicKey())
	if !rightClickMember(g, mfp) {
		t.Fatal("no hit on the member row")
	}
	if want := []string{"Allow hosting"}; !reflect.DeepEqual(menuLabels(g), want) {
		t.Fatalf("items = %v, want %v", menuLabels(g), want)
	}
	menuAct(g, "Allow hosting")
	waitHosts(t, f, func(h map[string]int) bool { return h[mfp] == 2 })
	if op := f.c.Chain().Head().Op; op != chain.OpHostAdd {
		t.Errorf("head op = %v", op)
	}
}

func TestMemberMenuMoveUp(t *testing.T) {
	f := startFounder(t, "")
	b, c := mustIdentity(t), mustIdentity(t)
	appendHost(t, f, b, 2)
	appendHost(t, f, c, 3)
	g := memberMenuGame(t, f, b, c)
	bfp, cfp := identity.Fingerprint(b.PublicKey()), identity.Fingerprint(c.PublicKey())
	if !rightClickMember(g, cfp) {
		t.Fatal("no hit on the member row")
	}
	if want := []string{"Stop hosting", "Move up"}; !reflect.DeepEqual(menuLabels(g), want) {
		t.Fatalf("items = %v, want %v", menuLabels(g), want)
	}
	menuAct(g, "Move up")
	waitHosts(t, f, func(h map[string]int) bool { return h[cfp] == 2 && h[bfp] == 3 })
	if !rightClickMember(g, bfp) {
		t.Fatal("no hit on B")
	}
	if want := []string{"Stop hosting", "Move up"}; !reflect.DeepEqual(menuLabels(g), want) {
		t.Errorf("B items = %v, want %v", menuLabels(g), want)
	}
	if !rightClickMember(g, cfp) { // C is now directly under the founder
		t.Fatal("no hit on C")
	}
	if want := []string{"Stop hosting"}; !reflect.DeepEqual(menuLabels(g), want) {
		t.Errorf("C items = %v, want %v", menuLabels(g), want)
	}
}

func TestMemberMenuNonFounderNone(t *testing.T) {
	f := startFounder(t, "")
	m := mustIdentity(t)
	g := memberMenuGame(t, f, m)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	other, err := client.Dial(ctx, f.url, mustIdentity(t), "other", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	go func() {
		for range other.Events() {
		}
	}()
	g.cur().c = other
	if !rightClickMember(g, identity.Fingerprint(m.PublicKey())) {
		t.Fatal("no hit on the member row")
	}
	if g.menu.open {
		t.Errorf("menu opened for a non-founder: %v", menuLabels(g))
	}
}
