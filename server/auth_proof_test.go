package server

import (
	"path/filepath"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

func (h *hs) sendProofAuth(t *testing.T, c *websocket.Conn, id *identity.Identity, key []byte) {
	t.Helper()
	sendEnv(t, c, proto.TypeAuth, proto.Auth{
		PubKey: id.PublicKey(), Name: "tester",
		Proof: proto.PasswordProof(key, h.serverNonce, h.clientNonce, h.serverID[:]),
		Sig:   id.Sign(proto.ClientAuthBytes(h.serverNonce, h.clientNonce, h.serverID[:])),
	})
}

func TestAuthProofRightAndWrongPassword(t *testing.T) {
	url := startServer(t, Config{Password: "s3cret"})
	for _, tc := range []struct {
		pw string
		ok bool
	}{{"s3cret", true}, {"nope", false}} {
		c := dial(t, url)
		h := hello(t, c)
		key, err := proto.PasswordKey(tc.pw, h.serverID[:])
		if err != nil {
			t.Fatal(err)
		}
		h.sendProofAuth(t, c, mustID(t), key)
		env := readEnv(t, c)
		if tc.ok && env.Type != proto.TypeAuthOK {
			t.Fatalf("right password: type = %q, want auth_ok", env.Type)
		}
		if !tc.ok {
			requireAuthFailed(t, c, env)
		}
	}
}

func TestAuthNeitherProofNorPassword(t *testing.T) {
	url := startServer(t, Config{Password: "s3cret"})
	c := dial(t, url)
	h := hello(t, c)
	h.sendAuth(t, c, mustID(t), "tester", "")
	requireAuthFailed(t, c, readEnv(t, c))
}

func TestAuthKeyOnlyHost(t *testing.T) {
	hostKey := mustID(t)
	cfg := Config{HostKey: hostKey}
	ch, _, err := OpenChain(filepath.Join(t.TempDir(), "chain.jsonl"), hostKey, cfg)
	if err != nil {
		t.Fatal(err)
	}
	id := ch.ID()
	pk, err := proto.PasswordKey("s3cret", id[:])
	if err != nil {
		t.Fatal(err)
	}
	cfg.Chain, cfg.PasswordKey = ch, pk // no Password: the host holds only the key
	url := startServer(t, cfg)

	c := dial(t, url)
	h := hello(t, c)
	h.sendProofAuth(t, c, mustID(t), pk)
	if env := readEnv(t, c); env.Type != proto.TypeAuthOK {
		t.Fatalf("proof login: type = %q, want auth_ok", env.Type)
	}
	if _, env := authAs(t, url, mustID(t), "s3cret"); env.Type != proto.TypeAuthOK {
		t.Fatalf("legacy login: type = %q, want auth_ok", env.Type)
	}
	c, env := authAs(t, url, mustID(t), "nope")
	requireAuthFailed(t, c, env)
	c = dial(t, url)
	h = hello(t, c)
	bad, _ := proto.PasswordKey("nope", id[:])
	h.sendProofAuth(t, c, mustID(t), bad)
	requireAuthFailed(t, c, readEnv(t, c))
}

func TestAuthLegacyPassword(t *testing.T) {
	url := startServer(t, Config{Password: "s3cret"})
	_, env := authAs(t, url, mustID(t), "s3cret")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("legacy password: type = %q, want auth_ok", env.Type)
	}
	c, env := authAs(t, url, mustID(t), "nope")
	requireAuthFailed(t, c, env)
}
