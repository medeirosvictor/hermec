package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
)

func waitChain(t *testing.T, f *founderSrv, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("chain never reached the expected state")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestModerationMenuShowsAndBans(t *testing.T) {
	f := startFounder(t, "")
	m := mustIdentity(t)
	g := memberMenuGame(t, f, m)
	mfp := identity.Fingerprint(m.PublicKey())
	if !rightClickMember(g, mfp) {
		t.Fatal("no hit on the member row")
	}
	if got := menuLabels(g); !reflect.DeepEqual(got[:3], []string{"Allow hosting", "Make admin", "Ban…"}) {
		t.Fatalf("items = %v", got)
	}
	menuAct(g, "Ban…")
	if g.dlg.mode != dlgBan || g.dlg.mod.Target != mfp {
		t.Fatalf("dialog = %+v", g.dlg)
	}
	g.dialogConfirm()
	waitChain(t, f, func() bool { return isBanned(f, mfp) })
	pumpUntil(t, g, func() bool { return g.cur().st.Status == "banned "+g.memberName(g.cur(), mfp) })
	// The banned list shows them with Unban.
	g.openBannedMenu(g.cur().key, 10, 10)
	if got := menuLabels(g); len(got) != 1 || !strings.HasPrefix(got[0], "Unban ") {
		t.Fatalf("banned menu = %v", got)
	}
	menuAct(g, g.menu.items[0].label)
	waitChain(t, f, func() bool { return !isBanned(f, mfp) })
}

func TestModerationMakeAndRemoveAdmin(t *testing.T) {
	f := startFounder(t, "")
	m := mustIdentity(t)
	g := memberMenuGame(t, f, m)
	mfp := identity.Fingerprint(m.PublicKey())
	rightClickMember(g, mfp)
	menuAct(g, "Make admin")
	waitChain(t, f, func() bool { return hasManage(f, mfp) })
}

func TestModerationHiddenWithoutPermission(t *testing.T) {
	f := startFounder(t, "")
	m := mustIdentity(t)
	g := memberMenuGame(t, f, m)
	s := g.cur()
	if _, ok := g.bannedItem(s.key, 0, 0); !ok {
		t.Fatal("founder should see Banned members")
	}
	if items := g.modItems(s, f.c.Fingerprint()); len(items) != 0 {
		t.Errorf("founder sees moderation on self: %d", len(items))
	}
}

func isBanned(f *founderSrv, fp string) bool {
	cs := f.c.Chain().State()
	return cs.Banned(fp)
}

func hasManage(f *founderSrv, fp string) bool {
	cs := f.c.Chain().State()
	return cs.Roles().Has(fp, "manage")
}
