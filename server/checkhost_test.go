package server

import (
	"strings"
	"testing"
)

func TestCheckHost(t *testing.T) {
	key, other := mustID(t), mustID(t)
	ch, err := newChain(key, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckHost(ch, key); err != nil {
		t.Fatalf("listed host: %v", err)
	}
	err = CheckHost(ch, other)
	if err == nil {
		t.Fatal("unlisted key accepted")
	}
	want := "not an approved host of server " + ch.ID().Short()
	if !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want it to contain %q", err, want)
	}
}
