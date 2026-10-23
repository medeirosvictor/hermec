package state

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadServers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content *string // nil: no file
		wantErr bool
		wantN   int
	}{
		{"missing file", nil, false, 0},
		{"corrupt", ptr("not [valid"), true, 0},
		{"old format without name or nick", ptr("[[server]]\nurl = \"ws://a/\"\nlabel = \"a\"\nlast_channel = \"c\"\nlast_seen = 2023-11-14T22:13:20Z\n"), false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "sub", "servers.toml")
			if tc.content != nil {
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(*tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := LoadServers(p)
			if (err != nil) != tc.wantErr || len(got) != tc.wantN {
				t.Fatalf("got %v, %v; want err=%v n=%d", got, err, tc.wantErr, tc.wantN)
			}
			if tc.wantN == 1 && (got[0].URL != "ws://a/" || got[0].Name != "" || got[0].Nick != "") {
				t.Fatalf("old format: %+v", got[0])
			}
		})
	}
}

func ptr(s string) *string { return &s }

// Everything persistable survives a save and load, pending entries are not
// saved, empty name and nick are not written, and no temp file is left.
func TestServersRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "servers.toml")
	in := []ServerEntry{
		{URL: "ws://a:1/", Label: "a:1", LastChannel: "general", LastSeen: time.Unix(1700000000, 0).UTC()},
		{URL: "ws://b:2/", Label: "local", Name: "Primos", Nick: "Lab", LastSeen: time.Unix(1600000000, 0).UTC()},
		{URL: "ws://pending/", Pending: true},
	}
	in = SetHosts(in, "ws://a:1/", []string{"ws://a:1/", "ws://b:2/"})
	in = BumpChainSeq(in, "ws://a:1/", 7)
	in = BumpChainSeq(in, "ws://a:1/", 3) // never lowers
	if err := SaveServers(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadServers(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("loaded %+v, want the two non-pending entries", out)
	}
	if a := out[0]; a.URL != "ws://a:1/" || a.LastChannel != "general" || !a.LastSeen.Equal(in[0].LastSeen) ||
		len(a.Hosts) != 2 || a.Hosts[1] != "ws://b:2/" || a.ChainSeq != 7 {
		t.Fatalf("first entry: %+v", a)
	}
	if b := out[1]; b.Label != "local" || b.Name != "Primos" || b.Nick != "Lab" {
		t.Fatalf("second entry: %+v", b)
	}
	if ents, _ := os.ReadDir(filepath.Dir(p)); len(ents) != 1 {
		t.Fatalf("temp file left behind: %v", ents)
	}
	raw, _ := os.ReadFile(p)
	if strings.Count(string(raw), "nick") != 1 || strings.Count(string(raw), "name") != 1 {
		t.Fatalf("empty name or nick written:\n%s", raw)
	}
}

func TestTouch(t *testing.T) {
	t.Run("upsert keeps order, new goes first", func(t *testing.T) {
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
		if e[1].LastChannel != "z" || e[1].Label != "a2" || e[1].LastSeen.IsZero() {
			t.Fatalf("upsert %+v", e)
		}
	})
	t.Run("empty arguments keep fields, a new name replaces, nick untouched, pending cleared", func(t *testing.T) {
		e := Touch(nil, "ws://a/", "a", "Primos", "chan", nil)
		e[0].Nick, e[0].Pending = "mine", true
		e = Touch(e, "ws://a/", "", "", "", nil)
		if e[0].Label != "a" || e[0].LastChannel != "chan" || e[0].Name != "Primos" || e[0].Nick != "mine" || e[0].Pending {
			t.Fatalf("%+v", e)
		}
		if e = Touch(e, "ws://a/", "", "Other", "", nil); e[0].Name != "Other" {
			t.Fatalf("name not replaced: %+v", e)
		}
	})
	t.Run("cap drops the oldest, never a live one", func(t *testing.T) {
		var e []ServerEntry
		for i := 0; i < MaxServers-1; i++ {
			e = append(e, ServerEntry{URL: fmt.Sprintf("ws://h%d/", i)})
		}
		e = append(e, ServerEntry{URL: "ws://live/"}) // the oldest
		e = Touch(e, "ws://new/", "", "", "", func(u string) bool { return u == "ws://live/" })
		if len(e) != MaxServers || e[0].URL != "ws://new/" {
			t.Fatalf("len %d first %s", len(e), e[0].URL)
		}
		urls := map[string]bool{}
		for _, x := range e {
			urls[x.URL] = true
		}
		if !urls["ws://live/"] || urls[fmt.Sprintf("ws://h%d/", MaxServers-2)] {
			t.Fatalf("live kept=%v; the oldest non-live should be gone", urls["ws://live/"])
		}
	})
	t.Run("cap without live", func(t *testing.T) {
		var e []ServerEntry
		for i := 0; i < MaxServers+9; i++ {
			e = Touch(e, fmt.Sprintf("ws://h%d/", i), "", "", "", nil)
		}
		if len(e) != MaxServers || e[0].URL != fmt.Sprintf("ws://h%d/", MaxServers+8) {
			t.Fatalf("len %d first %s", len(e), e[0].URL)
		}
	})
	t.Run("input not aliased", func(t *testing.T) {
		a := []ServerEntry{{URL: "ws://a/", LastChannel: "1"}}
		_ = Touch(a, "ws://a/", "", "", "2", nil)
		if a[0].LastChannel != "1" {
			t.Fatal("input mutated")
		}
	})
}

func TestNames(t *testing.T) {
	for _, tc := range []struct {
		e        ServerEntry
		display  string
		initials string
	}{
		{ServerEntry{Nick: "N", Name: "S", Label: "L"}, "N", "N"},
		{ServerEntry{Name: "Primos", Label: "lo"}, "Primos", "Pr"},
		{ServerEntry{Nick: "Lab", Name: "Primos"}, "Lab", "La"},
		{ServerEntry{Label: "local", URL: "ws://127.0.0.1:1/"}, "local", "lo"},
		{ServerEntry{Label: "é", URL: "ws://x/"}, "é", "é"},
		{ServerEntry{URL: "ws://chat.example.com:9/"}, "chat.example.com:9", "ch"},
		{ServerEntry{URL: "ws://h:9/"}, "h:9", "h:"},
	} {
		if got := DisplayName(tc.e); got != tc.display {
			t.Errorf("DisplayName(%+v) = %q, want %q", tc.e, got, tc.display)
		}
		if got := Initials(tc.e); got != tc.initials {
			t.Errorf("Initials(%+v) = %q, want %q", tc.e, got, tc.initials)
		}
	}
	if got := HostPort("ws://127.0.0.1:5555/"); got != "127.0.0.1:5555" {
		t.Error(got)
	}
	if got := HostPort("junk"); got != "junk" {
		t.Error(got)
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
		{"a‮b", 64, "ab"},
		{"a⁦b⁩c", 64, "abc"},
		{"a b c", 64, "abc"},
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
	if len(out) != len(in) || !reflect.DeepEqual(out, in) {
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

func TestSetHostsDedupesAndCaps(t *testing.T) {
	var hs []string
	for i := 0; i < 40; i++ {
		hs = append(hs, fmt.Sprintf("ws://h%d/", i%30), "")
	}
	out := SetHosts([]ServerEntry{{URL: "u"}}, "u", hs)
	if n := len(out[0].Hosts); n != MaxHosts {
		t.Fatalf("%d hosts, want %d", n, MaxHosts)
	}
	seen := map[string]bool{}
	for _, h := range out[0].Hosts {
		if h == "" || seen[h] {
			t.Fatalf("empty or duplicate host %q in %v", h, out[0].Hosts)
		}
		seen[h] = true
	}
}
