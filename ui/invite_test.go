package ui

import (
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/ui/state"
)

func TestJoinInviteWSSPrefillsDedicatedForm(t *testing.T) {
	g := addTestGame(t)
	d := dedicatedTestDetails()
	link, _ := state.FormatInvite(state.Invite{Addr: d.WSSURL, ServerID: d.ServerID, Password: "s3cret"})
	if msg := g.joinInviteText(link); msg != "" {
		t.Fatal(msg)
	}
	if g.add != addDedicated || g.df.wss.String() != d.WSSURL || g.df.serverID.String() != d.ServerID || g.df.password.String() != "s3cret" {
		t.Fatalf("form not prefilled: add=%v url=%q id=%q", g.add, g.df.wss.String(), g.df.serverID.String())
	}
	if g.dedicatedStop != nil || len(g.sess) != 0 {
		t.Error("the invite connected without validation")
	}
	if strings.Contains(g.df.status, "s3cret") {
		t.Error("status leaks the password")
	}
	link, _ = state.FormatInvite(state.Invite{Addr: d.WSSURL, ServerID: d.ServerID})
	g.joinInviteText(link)
	if g.df.password.String() != "" || g.df.focus != 2 {
		t.Errorf("no-pw invite: password %q focus %d, want empty and password focused", g.df.password.String(), g.df.focus)
	}
}

func TestJoinInviteWSPinsServerIDBeforeDial(t *testing.T) {
	g := addTestGame(t)
	id := dedicatedTestDetails().ServerID
	link, _ := state.FormatInvite(state.Invite{Addr: "ws://127.0.0.1:1/", ServerID: id})
	if msg := g.joinInviteText(link); msg != "" {
		t.Fatal(msg)
	}
	e, ok := g.entryFor("ws://127.0.0.1:1/")
	if !ok || e.ServerID != id {
		t.Fatalf("entry = %+v, %v; want pinned %s", e, ok, id)
	}
	if g.sess["ws://127.0.0.1:1/"] == nil || g.curKey != "ws://127.0.0.1:1/" {
		t.Error("session not opened and viewed")
	}
}

func TestJoinInviteWSRefusesDifferentSavedPin(t *testing.T) {
	g := addTestGame(t)
	g.servers = []state.ServerEntry{{URL: "ws://127.0.0.1:1/", ServerID: "other"}}
	link, _ := state.FormatInvite(state.Invite{Addr: "ws://127.0.0.1:1/", ServerID: dedicatedTestDetails().ServerID})
	if msg := g.joinInviteText(link); msg == "" || g.sess["ws://127.0.0.1:1/"] != nil {
		t.Fatalf("msg=%q, session opened=%v", msg, g.sess["ws://127.0.0.1:1/"] != nil)
	}
}

func TestJoinInviteInvalidIsFriendlyAndSilent(t *testing.T) {
	g := addTestGame(t)
	msg := g.joinInviteText("hermec://join?v=1&addr=nope&pw=hunter2")
	if msg == "" || strings.Contains(msg, "hunter2") || strings.Contains(msg, "nope") {
		t.Fatalf("message = %q", msg)
	}
}

func TestInviteFieldWipedAfterSubmit(t *testing.T) {
	g := addTestGame(t)
	g.ai.f.setText("hermec://join?v=1&pw=hunter2")
	g.ai.focus = true
	text := g.ai.f.String()
	g.ai.clear()
	if g.ai.f.String() != "" || text == "" {
		t.Fatal("field not cleared")
	}
	if got := inviteShown("hermec://join?v=1&pw=hunter2", 80); strings.Contains(got, "hunter2") {
		t.Errorf("typed password drawn: %q", got)
	}
}

func TestInviteForOnlyDedicatedEntries(t *testing.T) {
	g := addTestGame(t)
	d := dedicatedTestDetails()
	g.servers = []state.ServerEntry{{URL: d.WSSURL, ServerID: d.ServerID, PasswordKey: "ab"}, {URL: "ws://1.2.3.4:7697/", ServerID: d.ServerID}, {URL: "wss://nopin.test/"}}
	inv, ok := g.inviteFor(d.WSSURL)
	if !ok || inv.Password != "" || inv.Addr != d.WSSURL || inv.ServerID != d.ServerID {
		t.Fatalf("inviteFor = %+v, %v", inv, ok)
	}
	for _, k := range []string{"ws://1.2.3.4:7697/", "wss://nopin.test/", "missing"} {
		if _, ok := g.inviteFor(k); ok {
			t.Errorf("inviteFor(%q) offered", k)
		}
	}
}
