package state

import (
	"os"
	"path/filepath"
	"strings"
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
	e = Touch(e, "ws://a/", "a", "", "x", nil)
	e = Touch(e, "ws://b/", "b", "", "y", nil)
	if e[0].URL != "ws://b/" || e[1].URL != "ws://a/" {
		t.Fatalf("order %+v", e)
	}
	e = Touch(e, "ws://a/", "a2", "", "z", nil)
	if len(e) != 2 || e[0].URL != "ws://b/" || e[1].URL != "ws://a/" {
		t.Fatalf("order must stay stable %+v", e)
	}
	if e[1].LastChannel != "z" || e[1].Label != "a2" {
		t.Fatalf("upsert %+v", e)
	}
	if e[1].LastSeen.IsZero() {
		t.Fatal("LastSeen unset")
	}
}

func TestTouchNewGoesFirst(t *testing.T) {
	e := Touch(nil, "ws://a/", "", "", "", nil)
	e = Touch(e, "ws://b/", "", "", "", nil)
	if e[0].URL != "ws://b/" {
		t.Fatalf("%+v", e)
	}
}

func TestTouchCapKeepsLive(t *testing.T) {
	var e []ServerEntry
	for i := 0; i < MaxServers-1; i++ {
		e = append(e, ServerEntry{URL: "ws://h" + string(rune('a'+i)) + "/"})
	}
	e = append(e, ServerEntry{URL: "ws://live/"})
	live := func(u string) bool { return u == "ws://live/" }
	e = Touch(e, "ws://new/", "", "", "", live)
	if len(e) != MaxServers || e[0].URL != "ws://new/" {
		t.Fatalf("len %d first %s", len(e), e[0].URL)
	}
	hasLive, hasLastDropped := false, true
	for _, x := range e {
		if x.URL == "ws://live/" {
			hasLive = true
		}
		if x.URL == "ws://h"+string(rune('a'+MaxServers-2))+"/" {
			hasLastDropped = false
		}
	}
	if !hasLive || !hasLastDropped {
		t.Fatalf("live kept=%v last non-live dropped=%v", hasLive, hasLastDropped)
	}
}

func TestTouchEmptyKeepsFields(t *testing.T) {
	e := Touch(nil, "ws://a/", "a", "", "chan", nil)
	e = Touch(e, "ws://a/", "", "", "", nil)
	if e[0].Label != "a" || e[0].LastChannel != "chan" {
		t.Fatalf("%+v", e)
	}
}

func TestTouchCap(t *testing.T) {
	var e []ServerEntry
	for i := 0; i < 25; i++ {
		e = Touch(e, "ws://h"+string(rune('a'+i))+"/", "", "", "", nil)
	}
	if len(e) != MaxServers || e[0].URL != "ws://hy/" {
		t.Fatalf("len %d first %s", len(e), e[0].URL)
	}
}

func TestTouchDoesNotAliasInput(t *testing.T) {
	a := []ServerEntry{{URL: "ws://a/", LastChannel: "1"}}
	_ = Touch(a, "ws://a/", "", "", "2", nil)
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

func TestDisplayNamePrecedence(t *testing.T) {
	cases := []struct {
		e    ServerEntry
		want string
	}{
		{ServerEntry{Nick: "N", Name: "S", Label: "L"}, "N"},
		{ServerEntry{Name: "S", Label: "L"}, "S"},
		{ServerEntry{Label: "L"}, "L"},
		{ServerEntry{URL: "ws://h:9/"}, "h:9"},
	}
	for _, c := range cases {
		if got := DisplayName(c.e); got != c.want {
			t.Errorf("%+v: got %q want %q", c.e, got, c.want)
		}
	}
}

func TestInitialsUsesDisplayName(t *testing.T) {
	cases := []struct {
		e    ServerEntry
		want string
	}{
		{ServerEntry{Name: "Primos", Label: "lo"}, "Pr"},
		{ServerEntry{Nick: "Lab", Name: "Primos"}, "La"},
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

func TestCleanName(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"a\x1bb\tc\n", 64, "abc"},
		{"  x  ", 64, "x"},
		{strings.Repeat("é", 70), 64, strings.Repeat("é", 64)},
		{"\x01\t \n", 64, ""},
		{strings.Repeat("a", 40), 32, strings.Repeat("a", 32)},
		{"a\u202eb", 64, "ab"},
		{"a\u2066b\u2069c", 64, "abc"},
		{"a\u2028b\u2029c", 64, "abc"},
	}
	for _, c := range cases {
		if got := CleanName(c.in, c.max); got != c.want {
			t.Errorf("CleanName(%q,%d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestSetNickCleans(t *testing.T) {
	in := []ServerEntry{{URL: "u", Nick: "old"}, {URL: "v"}}
	out := SetNick(in, "u", "  "+strings.Repeat("a", 40)+"  ")
	if out[0].Nick != strings.Repeat("a", 32) {
		t.Fatalf("nick %q", out[0].Nick)
	}
	if in[0].Nick != "old" {
		t.Fatal("input mutated")
	}
	if out = SetNick(in, "u", "   "); out[0].Nick != "" {
		t.Fatalf("blank nick %q", out[0].Nick)
	}
	out = SetNick(in, "nope", "x")
	if len(out) != len(in) || out[0] != in[0] || out[1] != in[1] {
		t.Fatalf("unknown url: %+v", out)
	}
	out[0].Nick = "z"
	if in[0].Nick != "old" {
		t.Fatal("copy aliases input")
	}
}

func TestRemove(t *testing.T) {
	in := []ServerEntry{{URL: "a"}, {URL: "b"}, {URL: "c"}}
	out := Remove(in, "b")
	if len(out) != 2 || out[0].URL != "a" || out[1].URL != "c" {
		t.Fatalf("%+v", out)
	}
	if len(in) != 3 || in[1].URL != "b" {
		t.Fatal("input mutated")
	}
	if out = Remove(in, "zzz"); len(out) != 3 || out[0].URL != "a" || out[2].URL != "c" {
		t.Fatalf("absent url: %+v", out)
	}
}

func TestTouchKeepsName(t *testing.T) {
	e := Touch(nil, "ws://a/", "a", "Primos", "c", nil)
	e[0].Nick = "mine"
	e = Touch(e, "ws://a/", "", "", "", nil)
	if e[0].Name != "Primos" {
		t.Fatalf("name lost: %+v", e)
	}
	e = Touch(e, "ws://a/", "", "Other", "", nil)
	if e[0].Name != "Other" {
		t.Fatalf("name not replaced: %+v", e)
	}
	if e[0].Nick != "mine" {
		t.Fatalf("Touch changed nick: %+v", e)
	}
}

func TestServersFileOldFormatLoads(t *testing.T) {
	p := filepath.Join(t.TempDir(), "servers.toml")
	old := "[[server]]\nurl = \"ws://a/\"\nlabel = \"a\"\nlast_channel = \"c\"\nlast_seen = 2023-11-14T22:13:20Z\n"
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadServers(p)
	if err != nil || len(got) != 1 || got[0].Name != "" || got[0].Nick != "" || got[0].URL != "ws://a/" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if err := SaveServers(p, got); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "name") || strings.Contains(string(b), "nick") {
		t.Fatalf("empty name/nick written:\n%s", b)
	}
	in := []ServerEntry{{URL: "ws://b/", Name: "Primos", Nick: "Lab"}}
	if err := SaveServers(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadServers(p)
	if err != nil || len(out) != 1 || out[0].Name != "Primos" || out[0].Nick != "Lab" {
		t.Fatalf("round trip: %+v, %v", out, err)
	}
}
