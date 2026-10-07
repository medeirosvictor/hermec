package state

import (
	"reflect"
	"testing"
)

func TestMergeDiscoveredSortsAndLimits(t *testing.T) {
	in := []DiscoveredIn{
		{Name: "s", Addr: "9.9.9.9:7697", Source: "saved"},
		{Name: "t", Addr: "100.1.1.1:7697", Source: "tailnet"},
		{Name: "l2", Addr: "192.168.1.9:7697", Source: "lan"},
		{Name: "l1", Addr: "192.168.1.2:7697", Source: "lan"},
	}
	rows := MergeDiscovered(in, nil)
	var got []string
	for _, r := range rows {
		got = append(got, r.Name)
	}
	if want := []string{"l1", "l2", "t", "s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if rows[0].URL != "ws://192.168.1.2:7697/" {
		t.Fatalf("url = %q", rows[0].URL)
	}
	var many []DiscoveredIn
	for i := 0; i < 10; i++ {
		many = append(many, DiscoveredIn{Name: "n", Addr: "10.0.0." + string(rune('0'+i)) + ":1", Source: "lan"})
	}
	if n := len(MergeDiscovered(many, nil)); n != MaxDiscovered {
		t.Fatalf("len = %d, want %d", n, MaxDiscovered)
	}
	if MergeDiscovered(nil, nil) != nil {
		t.Fatal("empty input must give nil")
	}
}

func TestMergeDiscoveredUsesSavedLabel(t *testing.T) {
	in := []DiscoveredIn{{Name: "announced", Addr: "10.0.0.5:7697", Source: "lan"}}
	saved := []ServerEntry{
		{URL: "ws://10.0.0.5:7697/", Label: "Home"},
		{URL: "ws://other:1/", Label: "x"},
	}
	r := MergeDiscovered(in, saved)[0]
	if r.Name != "Home" || !r.Saved {
		t.Fatalf("row = %+v", r)
	}
	saved[0].Label = ""
	if r := MergeDiscovered(in, saved)[0]; r.Name != "announced" || !r.Saved {
		t.Fatalf("empty label must fall back: %+v", r)
	}
}

func TestMoveSel(t *testing.T) {
	cases := []struct{ sel, d, n, want int }{
		{-1, 1, 3, 0}, {0, 1, 3, 1}, {2, 1, 3, 2}, {1, -1, 3, 0},
		{0, -1, 3, -1}, {-1, -1, 3, -1}, {-1, 1, 0, -1}, {5, 1, 2, 1},
	}
	for _, c := range cases {
		if got := MoveSel(c.sel, c.d, c.n); got != c.want {
			t.Errorf("MoveSel(%d,%d,%d)=%d want %d", c.sel, c.d, c.n, got, c.want)
		}
	}
}

func TestReselect(t *testing.T) {
	rows := []DiscoveredRow{{Addr: "a:1"}, {Addr: "b:1"}}
	if got := Reselect(rows, "b:1"); got != 1 {
		t.Fatalf("got %d", got)
	}
	if got := Reselect(rows, "zz"); got != -1 {
		t.Fatalf("got %d", got)
	}
	if got := Reselect(rows, ""); got != -1 {
		t.Fatalf("got %d", got)
	}
}

func TestSavedHosts(t *testing.T) {
	saved := []ServerEntry{
		{URL: "ws://a.example:7697/"}, {URL: "hermec://local"},
		{URL: "ws://a.example:7697/"}, {URL: "wss://b.example/"},
	}
	got := SavedHosts(saved, "127.0.0.1:5")
	want := []string{"a.example:7697", "b.example", "127.0.0.1:5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
