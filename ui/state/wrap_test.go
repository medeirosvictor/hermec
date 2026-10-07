package state

import (
	"reflect"
	"testing"
)

func TestWrapAscii(t *testing.T) {
	got := Wrap("aaaa bb cc", 5)
	if want := []string{"aaaa", "bb cc"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestWrapLongWordHardBreaks(t *testing.T) {
	got := Wrap("abcdefghijkl", 5)
	if want := []string{"abcde", "fghij", "kl"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestWrapUnicodeRunes(t *testing.T) {
	got := Wrap("ação ção", 4)
	if want := []string{"ação", "ção"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if got := Wrap("abc", 0); !reflect.DeepEqual(got, []string{"abc"}) {
		t.Fatalf("cols 0: %q", got)
	}
}
