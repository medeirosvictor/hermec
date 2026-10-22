package client

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/server"
)

func TestAuthWithProof(t *testing.T) {
	url := startServer(t, server.Config{Password: "s3cret"})
	id, _ := identity.Generate()
	c, err := Dial(testCtx(t), url, id, "alice", "s3cret")
	if err != nil {
		t.Fatalf("right password: %v", err)
	}
	key := c.PasswordKey()
	c.Close()
	if len(key) != 32 {
		t.Fatalf("PasswordKey = %x, want 32 bytes", key)
	}
	// The cached key alone logs in; the password is not needed.
	id2, _ := identity.Generate()
	c, err = DialWith(testCtx(t), url, id2, "bob", "", DialOpts{PasswordKey: key})
	if err != nil {
		t.Fatalf("with cached key: %v", err)
	}
	c.Close()
	if _, err := Dial(testCtx(t), url, id, "alice", "wrong"); err == nil || !strings.Contains(err.Error(), "auth_failed") {
		t.Fatalf("wrong password err = %v, want auth_failed", err)
	}
}

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
	if len(a.Proof) == 0 || a.Password != "" || strings.Contains(string(raw), "password") || strings.Contains(string(raw), "s3cret") {
		t.Fatalf("auth = %s", raw)
	}
}
