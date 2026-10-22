package server

import (
	"testing"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

// The founder and every listed host get in even when allowed_keys omits them.
func TestAllowedKeysNeverLocksOutFounderOrHosts(t *testing.T) {
	founder, host2, friend, stranger := mustID(t), mustID(t), mustID(t), mustID(t)
	cfg := Config{
		HostKey: founder, Channels: []string{"general"},
		Roles: roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}},
	}
	ch, err := newChain(founder, cfg)
	if err != nil {
		t.Fatal(err)
	}
	f := &appendFixture{founder: founder, ch: ch}
	add := func(op chain.Op, body any) {
		if err := ch.Append(f.entry(t, founder, op, body)); err != nil {
			t.Fatal(err)
		}
	}
	add(chain.OpHostAdd, chain.HostAddBody{PubKey: []byte(host2.PublicKey()), Priority: 2})
	body := ch.State().Config
	body.AllowedKeys = []string{identity.Fingerprint(friend.PublicKey())}
	add(chain.OpConfig, body)
	cfg.Chain = ch
	url := startServer(t, cfg)

	for name, id := range map[string]*identity.Identity{"founder": founder, "second host": host2, "listed friend": friend} {
		if _, env := authAs(t, url, id, ""); env.Type != proto.TypeAuthOK {
			t.Errorf("%s: type = %q, want auth_ok", name, env.Type)
		}
	}
	if _, env := authAs(t, url, stranger, ""); env.Type != proto.TypeError {
		t.Errorf("stranger: type = %q, want error", env.Type)
	}
}

// The founder keeps every permission whatever the chain grants them.
func TestFounderHoldsEveryPermission(t *testing.T) {
	f := newAppendFixture(t)
	a := f.connect(t, f.founder)
	fp := identity.Fingerprint(f.founder.PublicKey())
	// The founder (the only one who may) empties their own grant.
	sendAppend(t, a, f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: fp, Roles: nil}))
	readChainEntry(t, a)
	sendEnv(t, a, proto.TypeJoin, proto.Join{Channel: "general"})
	for {
		env := readEnvSkipRoster(t, a)
		if env.Type == proto.TypeError {
			t.Fatalf("founder join refused: %s", env.Data)
		}
		if env.Type == proto.TypePresence {
			break
		}
	}
}
