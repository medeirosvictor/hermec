package state

import (
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/version"
)

func TestDedicatedDetailsStrictWSSAndServerID(t *testing.T) {
	id := (chain.ID{1}).String()
	for _, raw := range []string{"ws://example.test/", "wss://Example.test/", "wss://example.test/path", "wss://example.test/?q=1"} {
		if _, err := ValidateDedicatedDetails(DedicatedDetails{WSSURL: raw, ServerID: id}); err == nil {
			t.Errorf("ValidateDedicatedDetails accepted %q", raw)
		}
	}
	if _, err := ValidateDedicatedDetails(DedicatedDetails{WSSURL: "wss://example.test/", ServerID: strings.ToUpper(id)}); err == nil {
		t.Error("accepted non-canonical server ID")
	}
}

func TestDedicatedImportRequiresSameBuild(t *testing.T) {
	id := (chain.ID{1}).String()
	data := []byte(`{"build_version":"other-build","wss_url":"wss://example.test/","server_id":"` + id + `"}`)
	if _, err := UnmarshalDedicatedDetails(data); err == nil {
		t.Fatal("accepted details from another build")
	}
	data = []byte(`{"build_version":"` + version.Version + `","wss_url":"wss://example.test/","server_id":"` + id + `"}`)
	if _, err := UnmarshalDedicatedDetails(data); err != nil {
		t.Fatalf("rejected same-build details: %v", err)
	}
}
