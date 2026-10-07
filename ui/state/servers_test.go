package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadServersMissing(t *testing.T) {
	got, err := LoadServers(filepath.Join(t.TempDir(), "nope", "servers.toml"))
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestServersRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "servers.toml")
	in := []ServerEntry{
		{URL: "ws://a:1/", Label: "a:1", LastChannel: "general", LastSeen: time.Unix(1700000000, 0).UTC()},
		{URL: "ws://b:2/", Label: "local", LastSeen: time.Unix(1600000000, 0).UTC()},
	}
	if err := SaveServers(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadServers(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].URL != in[0].URL || out[0].LastChannel != "general" || !out[0].LastSeen.Equal(in[0].LastSeen) || out[1].Label != "local" {
		t.Fatalf("round trip: %+v", out)
	}
	if ents, _ := os.ReadDir(filepath.Dir(p)); len(ents) != 1 {
		t.Fatalf("temp file left behind: %v", ents)
	}
}

func TestLoadServersCorrupt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.toml")
	if err := os.WriteFile(p, []byte("not [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadServers(p); err == nil {
		t.Fatal("want error")
	}
}

func TestTouchUpsertOrder(t *testing.T) {
	var e []ServerEntry
	e = Touch(e, "ws://a/", "a", "x")
	e = Touch(e, "ws://b/", "b", "y")
	if e[0].URL != "ws://b/" || e[1].URL != "ws://a/" {
		t.Fatalf("order %+v", e)
	}
	e = Touch(e, "ws://a/", "a2", "z")
	if len(e) != 2 || e[0].URL != "ws://a/" || e[0].LastChannel != "z" || e[0].Label != "a2" {
		t.Fatalf("upsert %+v", e)
	}
	if e[0].LastSeen.IsZero() {
		t.Fatal("LastSeen unset")
	}
}

func TestTouchEmptyKeepsFields(t *testing.T) {
	e := Touch(nil, "ws://a/", "a", "chan")
	e = Touch(e, "ws://a/", "", "")
	if e[0].Label != "a" || e[0].LastChannel != "chan" {
		t.Fatalf("%+v", e)
	}
}

func TestTouchCap(t *testing.T) {
	var e []ServerEntry
	for i := 0; i < 25; i++ {
		e = Touch(e, "ws://h"+string(rune('a'+i))+"/", "", "")
	}
	if len(e) != MaxServers || e[0].URL != "ws://hy/" {
		t.Fatalf("len %d first %s", len(e), e[0].URL)
	}
}

func TestTouchDoesNotAliasInput(t *testing.T) {
	a := []ServerEntry{{URL: "ws://a/", LastChannel: "1"}}
	_ = Touch(a, "ws://a/", "", "2")
	if a[0].LastChannel != "1" {
		t.Fatal("input mutated")
	}
}

func TestInitials(t *testing.T) {
	cases := []struct {
		e    ServerEntry
		want string
	}{
		{ServerEntry{Label: "local", URL: "ws://127.0.0.1:1/"}, "lo"},
		{ServerEntry{Label: "é", URL: "ws://x/"}, "é"},
		{ServerEntry{URL: "ws://chat.example.com:9/"}, "ch"},
	}
	for _, c := range cases {
		if got := Initials(c.e); got != c.want {
			t.Errorf("%+v: got %q want %q", c.e, got, c.want)
		}
	}
}

func TestHostPort(t *testing.T) {
	if got := HostPort("ws://127.0.0.1:5555/"); got != "127.0.0.1:5555" {
		t.Fatal(got)
	}
	if got := HostPort("junk"); got != "junk" {
		t.Fatal(got)
	}
}

func TestRailHit(t *testing.T) {
	const w, top, th, h = 56.0, 8.0, 56.0, 400.0
	cases := []struct {
		x, y float64
		want int
	}{
		{10, 8, 0},
		{10, 63, 0},
		{10, 64, 1},
		{10, 8 + 3*56, RailNone}, // below last tile
		{60, 20, RailNone},       // outside rail
		{10, h - 10, RailSettings},
		{10, h - th - 10, RailAdd},
		{10, 2, RailNone},
	}
	for _, c := range cases {
		if got := RailHit(c.x, c.y, w, top, th, 3, h); got != c.want {
			t.Errorf("(%v,%v): got %d want %d", c.x, c.y, got, c.want)
		}
	}
}
