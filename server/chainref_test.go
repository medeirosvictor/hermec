package server

import (
	"encoding/json"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

func TestGrantRebroadcastsRoster(t *testing.T) {
	f := newAppendFixture(t)
	m := mustID(t)
	mfp := identity.Fingerprint(m.PublicKey())
	a := f.connect(t, f.founder)
	b := f.connect(t, m)
	sendAppend(t, a, f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: mfp, Roles: []string{"admin"}}))
	for _, c := range []*websocket.Conn{a, b} {
		for found := false; !found; {
			env := readEnv(t, c)
			if env.Type != proto.TypeRoster {
				continue
			}
			var r proto.Roster
			_ = json.Unmarshal(env.Data, &r)
			for _, mem := range r.Members {
				if mem.Fingerprint != mfp {
					continue
				}
				for _, role := range mem.Roles {
					found = found || role == "admin"
				}
			}
		}
	}
}

func TestChainAppendErrorRef(t *testing.T) {
	f := newAppendFixture(t)
	a := f.connect(t, f.founder)
	stale := f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: "p", Roles: []string{"bot"}})
	win := f.entry(t, f.founder, chain.OpGrant, chain.GrantBody{Fingerprint: "q", Roles: []string{"bot"}})
	sendAppend(t, a, win)
	readChainEntry(t, a)
	raw, _ := json.Marshal(stale)
	sendEnv(t, a, proto.TypeChainAppend, proto.ChainAppend{Entry: raw, Ref: "r1"})
	if em := readErrCode(t, a); em.Code != proto.CodeChainConflict || em.Ref != "r1" {
		t.Fatalf("error = %+v, want chain_conflict ref r1", em)
	}
}
