package client

import (
	"encoding/base64"
	"testing"

	"github.com/medeirosvictor/hermec/core/identity"
)

func TestParseDedicatedHostPublicKey(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	pub, fp, err := ParseDedicatedHostPublicKey(base64.RawStdEncoding.EncodeToString(id.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	if string(pub) != string(id.PublicKey()) || fp != identity.Fingerprint(id.PublicKey()) {
		t.Fatalf("got %x %q", pub, fp)
	}
	if _, _, err := ParseDedicatedHostPublicKey("not-a-key"); err == nil {
		t.Fatal("bad key accepted")
	}
}
