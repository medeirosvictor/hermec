package client

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/core/identity"
)

func TestAuthFrameCarriesNoPassword(t *testing.T) {
	f := newFakeHost(t, nil)
	id, _ := identity.Generate()
	if c, err := Dial(testCtx(t), f.url, id, "alice", "s3cret"); err == nil {
		c.Close()
	}
	a := f.gotAuth()
	if a == nil {
		t.Fatal("no auth received")
	}
	raw, _ := json.Marshal(a)
	if len(a.Proof) == 0 || strings.Contains(string(raw), "password") || strings.Contains(string(raw), "s3cret") {
		t.Fatalf("auth = %s", raw)
	}
}
