package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinIDOnlyWhenEmpty(t *testing.T) {
	es := []ServerEntry{{URL: "a"}, {URL: "b", ServerID: "srv-old"}}
	es = PinID(es, "a", "srv-1")
	es = PinID(es, "b", "srv-new")
	es = PinID(es, "zzz", "srv-x")
	if es[0].ServerID != "srv-1" || es[1].ServerID != "srv-old" || len(es) != 2 {
		t.Fatalf("got %+v", es)
	}
	if got := PinID(es, "a", ""); got[0].ServerID != "srv-1" {
		t.Fatal("empty id changed the pin")
	}
}

func TestServersTOMLKeepsServerID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "servers.toml")
	in := []ServerEntry{{URL: "ws://a/", ServerID: "srv-abc"}, {URL: "ws://b/"}}
	if err := SaveServers(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadServers(p)
	if err != nil || len(out) != 2 || out[0].ServerID != "srv-abc" || out[1].ServerID != "" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	b, _ := os.ReadFile(p)
	if strings.Count(string(b), "server_id") != 1 {
		t.Errorf("empty pin should be omitted:\n%s", b)
	}
}
