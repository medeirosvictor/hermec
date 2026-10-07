package discover

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"testing"
)

func TestParseTailscaleStatusFixture(t *testing.T) {
	b, err := os.ReadFile("testdata/tailscale-status.json")
	if err != nil {
		t.Fatal(err)
	}
	got := parseTailscaleStatus(b)
	want := []string{"100.101.102.103", "100.90.1.2"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("peers = %v, want %v (online only, IPv4 preferred)", got, want)
	}
}

func TestParseTailscaleStatusGarbage(t *testing.T) {
	if got := parseTailscaleStatus([]byte("not json")); got != nil {
		t.Fatalf("got %v", got)
	}
	if got := parseTailscaleStatus([]byte(`{}`)); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestTailscalePeersCLIAbsent(t *testing.T) {
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	defer func() { lookPath = old }()
	if got := tailscalePeers(context.Background()); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestTailscalePeersPresentUsesStatus(t *testing.T) {
	b, _ := os.ReadFile("testdata/tailscale-status.json")
	oldL, oldR := lookPath, runStatus
	lookPath = func(string) (string, error) { return "tailscale", nil }
	runStatus = func(context.Context, string) ([]byte, error) { return b, nil }
	defer func() { lookPath, runStatus = oldL, oldR }()
	if got := tailscalePeers(context.Background()); len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	runStatus = func(context.Context, string) ([]byte, error) { return nil, errors.New("boom") }
	if got := tailscalePeers(context.Background()); got != nil {
		t.Fatalf("got %v, want nil on exec failure", got)
	}
}
