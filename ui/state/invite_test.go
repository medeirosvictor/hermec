package state

import (
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/core/chain"
)

var testInviteID = (chain.ID{1}).String()

func TestInviteRoundTrip(t *testing.T) {
	for _, in := range []Invite{
		{Addr: "ws://100.1.2.3:7697/", ServerID: testInviteID},
		{Addr: "ws://100.1.2.3:7697/", ServerID: testInviteID, Name: "Friends & Co ü"},
		{Addr: "wss://example.test/", ServerID: testInviteID, Name: "Ops"},
		{Addr: "wss://example.test:8443/", ServerID: testInviteID, Password: "p&w=x y/é?#"},
	} {
		link, err := FormatInvite(in)
		if err != nil {
			t.Fatalf("FormatInvite(%+v): %v", in, err)
		}
		if !strings.HasPrefix(link, "hermec://join?v=1&addr=") {
			t.Errorf("link = %q", link)
		}
		if (in.Password == "") == strings.Contains(link, "pw=") {
			t.Errorf("password presence wrong in %q", link)
		}
		got, err := ParseInvite("  " + link + "\r\n")
		if err != nil || got != in {
			t.Errorf("round trip %q: got %+v, %v", link, got, err)
		}
	}
}

func TestInviteFormatRejectsInvalid(t *testing.T) {
	bad := []Invite{
		{Addr: "ws://h:1/", ServerID: "nope"},
		{Addr: "ws://h:1/", ServerID: strings.ToUpper(testInviteID)},
		{Addr: "http://h/", ServerID: testInviteID},
		{Addr: "ws://h:1/", ServerID: testInviteID, Password: "x"}, // secret over cleartext
		{Addr: "wss://example.test/", ServerID: testInviteID, Name: strings.Repeat("a", MaxServerNameRunes+1)},
		{Addr: "wss://example.test/", ServerID: testInviteID, Name: "a\nb"},
		{Addr: "wss://example.test/", ServerID: testInviteID, Password: strings.Repeat("p", 257)},
	}
	for _, in := range bad {
		if _, err := FormatInvite(in); err == nil {
			t.Errorf("FormatInvite accepted %+v", in)
		}
	}
}

func TestParseInviteMalformed(t *testing.T) {
	id := testInviteID
	const ok = "hermec://join?v=1&addr=ws%3A%2F%2Fh%3A7697%2F&id="
	if _, err := ParseInvite(ok + id); err != nil {
		t.Fatalf("baseline rejected: %v", err)
	}
	for name, s := range map[string]string{
		"empty":            "",
		"spaces":           "   ",
		"other scheme":     "https://join?v=1&addr=ws%3A%2F%2Fh%2F&id=" + id,
		"upper scheme":     "HTTP://join",
		"wrong host":       "hermec://host?v=1&addr=ws%3A%2F%2Fh%2F&id=" + id,
		"userinfo":         "hermec://u:p@join?v=1&addr=ws%3A%2F%2Fh%2F&id=" + id,
		"deep path":        "hermec://join/x?v=1&addr=ws%3A%2F%2Fh%2F&id=" + id,
		"no query":         "hermec://join",
		"no version":       "hermec://join?addr=ws%3A%2F%2Fh%2F&id=" + id,
		"version 2":        "hermec://join?v=2&addr=ws%3A%2F%2Fh%2F&id=" + id,
		"version empty":    "hermec://join?v=&addr=ws%3A%2F%2Fh%2F&id=" + id,
		"no addr":          "hermec://join?v=1&id=" + id,
		"no id":            "hermec://join?v=1&addr=ws%3A%2F%2Fh%2F",
		"unknown param":    ok + id + "&x=1",
		"dup param":        ok + id + "&id=" + id,
		"dup version":      "hermec://join?v=1&" + ok[len("hermec://join?"):] + id,
		"fragment":         ok + id + "#frag",
		"semicolon":        ok + id + ";name=x",
		"bad escape":       ok + id + "&name=%zz",
		"http addr":        "hermec://join?v=1&addr=http%3A%2F%2Fh%2F&id=" + id,
		"addr userinfo":    "hermec://join?v=1&addr=ws%3A%2F%2Fu%40h%3A1%2F&id=" + id,
		"addr query":       "hermec://join?v=1&addr=ws%3A%2F%2Fh%3A1%2F%3Fa%3D1&id=" + id,
		"addr fragment":    "hermec://join?v=1&addr=ws%3A%2F%2Fh%3A1%2F%23f&id=" + id,
		"addr bad port":    "hermec://join?v=1&addr=ws%3A%2F%2Fh%3A99999%2F&id=" + id,
		"addr no host":     "hermec://join?v=1&addr=ws%3A%2F%2F%2F&id=" + id,
		"addr space":       "hermec://join?v=1&addr=ws%3A%2F%2Fh+x%2F&id=" + id,
		"wss non-root":     "hermec://join?v=1&addr=wss%3A%2F%2Fh.test%2Fp&id=" + id,
		"wss uppercase":    "hermec://join?v=1&addr=wss%3A%2F%2FH.test%2F&id=" + id,
		"ws password":      ok + id + "&pw=secret",
		"id wrong length":  ok + "abc",
		"id uppercase":     ok + strings.ToUpper(id),
		"id garbage":       ok + strings.Repeat("!", 52),
		"name control":     ok + id + "&name=a%00b",
		"name too long":    ok + id + "&name=" + strings.Repeat("a", MaxServerNameRunes+1),
		"name bad utf8":    ok + id + "&name=%ff%fe",
		"addr too long":    "hermec://join?v=1&addr=ws%3A%2F%2F" + strings.Repeat("a", 600) + "%2F&id=" + id,
		"total too long":   ok + id + "&name=" + strings.Repeat("a", MaxInviteBytes),
		"pw too long":      "hermec://join?v=1&addr=wss%3A%2F%2Fh.test%2F&id=" + id + "&pw=" + strings.Repeat("p", 257),
		"inner whitespace": "hermec://join?v=1&addr=ws%3A%2F%2Fh%2F&id=" + id + " &name=x",
	} {
		if got, err := ParseInvite(s); err == nil {
			t.Errorf("%s: accepted, got %+v", name, got)
		}
	}
}

func FuzzParseInvite(f *testing.F) {
	f.Add("hermec://join?v=1&addr=wss%3A%2F%2Fh.test%2F&id=" + testInviteID)
	f.Fuzz(func(t *testing.T, s string) {
		i, err := ParseInvite(s)
		if err != nil {
			return
		}
		link, err := FormatInvite(i)
		if err != nil {
			t.Fatalf("parsed invite does not format: %v", err)
		}
		if j, err := ParseInvite(link); err != nil || j != i {
			t.Fatalf("round trip mismatch: %+v vs %+v (%v)", i, j, err)
		}
	})
}
