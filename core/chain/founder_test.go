package chain

import "testing"

// A grant naming the founder could strip the founder's roles; only the founder
// may write one.
func TestGrantTargetingFounderIsFounderOnly(t *testing.T) {
	f, adm := newID(t), newID(t)
	b := newBuilder(t, f)
	b.must(f, OpGrant, GrantBody{Fingerprint: fp(adm), Roles: []string{"admin"}})

	if err := b.add(adm, OpGrant, GrantBody{Fingerprint: fp(f), Roles: nil}); !isRule(err) {
		t.Fatalf("admin grant on founder: err = %v, want RuleError", err)
	}
	if err := b.add(adm, OpGrant, GrantBody{Fingerprint: fp(f), Roles: []string{"user"}}); !isRule(err) {
		t.Fatalf("admin grant on founder: err = %v, want RuleError", err)
	}
	if got, ok := b.c.State().Grants[fp(f)]; ok {
		t.Fatalf("founder grants = %v after refused entries", got)
	}
	// The founder may still write their own grant.
	b.must(f, OpGrant, GrantBody{Fingerprint: fp(f), Roles: []string{"admin"}})
}
