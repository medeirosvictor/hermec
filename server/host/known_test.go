package host

import (
	"slices"
	"testing"
)

func TestMergeURLs(t *testing.T) {
	have := []string{"ws://a/", "ws://b/"}
	got := MergeURLs(have, []string{"", "ws://b/", "ws://c/", "ws://c/"}, 0)
	if want := []string{"ws://a/", "ws://b/", "ws://c/"}; !slices.Equal(got, want) {
		t.Fatalf("merged %v, want %v", got, want)
	}
	if len(have) != 2 {
		t.Fatalf("have was changed: %v", have)
	}
	if got := MergeURLs(have, []string{"ws://c/", "ws://d/"}, 3); len(got) != 3 {
		t.Fatalf("cap 3 kept %v", got)
	}
}

func TestKnownAddPersistsGrowthOnly(t *testing.T) {
	var saved [][]string
	k := NewKnown([]string{"ws://a/"}, func(urls []string) { saved = append(saved, urls) })
	k.Add([]string{"ws://a/", ""})
	if len(saved) != 0 {
		t.Fatalf("persisted without growth: %v", saved)
	}
	k.Add([]string{"ws://b/"})
	if len(saved) != 1 || !slices.Equal(saved[0], []string{"ws://a/", "ws://b/"}) {
		t.Fatalf("persisted %v", saved)
	}
	if got := k.List(); !slices.Equal(got, saved[0]) {
		t.Fatalf("List = %v", got)
	}
	got := k.List()
	got[0] = "x"
	if k.List()[0] != "ws://a/" {
		t.Fatal("List returned the internal slice")
	}
}

func TestKnownCapsAtMaxKnown(t *testing.T) {
	k := NewKnown(nil, func([]string) {})
	for i := 0; i < MaxKnown+4; i++ {
		k.Add([]string{string(rune('a'+i)) + "://x/"})
	}
	if n := len(k.List()); n != MaxKnown {
		t.Fatalf("%d known, want %d", n, MaxKnown)
	}
}
