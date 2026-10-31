package dedicated

import (
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
)

func TestInstallBootstrapRejectsUnrelatedOrUnauthorizedState(t *testing.T) {
	local := mustID(t)
	bootstrap := bootstrapChain(t, local)
	other := mustID(t)
	unrelated := bootstrapChain(t, other)
	if _, err := InstallBootstrap(bootstrap, local, unrelated.Entries(), ""); err == nil {
		t.Fatal("accepted chain which does not authorize VPS key")
	}
	// A bootstrap that has acquired any non-bootstrap state is never replaced.
	head := bootstrap.Head()
	e, err := chain.NewEntry(local, &head, chain.OpGrant, chain.GrantBody{Fingerprint: identity.Fingerprint(other.PublicKey()), Roles: []string{"user"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Append(e); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallBootstrap(bootstrap, local, bootstrap.Entries(), ""); err == nil {
		t.Fatal("replaced non-bootstrap chain")
	}
}
