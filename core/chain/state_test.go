package chain

import (
	"errors"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/roles"
)

// builder helps tests append entries to a Chain signed by arbitrary identities.
type builder struct {
	t *testing.T
	c *Chain
	n int
}

func newBuilder(t *testing.T, founder *identity.Identity) *builder {
	t.Helper()
	c, err := New(genesis(t, founder))
	if err != nil {
		t.Fatal(err)
	}
	return &builder{t: t, c: c}
}

func (b *builder) entry(by *identity.Identity, op Op, body any) Entry {
	b.t.Helper()
	b.n++
	h := b.c.Head()
	e, err := NewEntry(by, &h, op, body, t0.Add(time.Duration(b.n)*time.Second))
	if err != nil {
		b.t.Fatal(err)
	}
	return e
}

func (b *builder) add(by *identity.Identity, op Op, body any) error {
	b.t.Helper()
	return b.c.Append(b.entry(by, op, body))
}

func (b *builder) must(by *identity.Identity, op Op, body any) {
	b.t.Helper()
	if err := b.add(by, op, body); err != nil {
		b.t.Fatalf("%s: %v", op, err)
	}
}

func fp(id *identity.Identity) string { return identity.Fingerprint(id.PublicKey()) }

func isRule(err error) bool {
	var re *RuleError
	return errors.As(err, &re)
}

func TestFounderCanDoEverything(t *testing.T) {
	f, a := newID(t), newID(t)
	b := newBuilder(t, f)
	b.must(f, OpConfig, ConfigBody{Name: "x", Channels: []string{"general"}})
	b.must(f, OpGrant, GrantBody{Fingerprint: fp(a), Roles: []string{"admin"}})
	b.must(f, OpBan, FPBody{Fingerprint: "zzzz"})
	b.must(f, OpUnban, FPBody{Fingerprint: "zzzz"})
	b.must(f, OpHostAdd, HostAddBody{PubKey: a.PublicKey(), Priority: 1})
	b.must(f, OpHostRemove, FPBody{Fingerprint: fp(a)})
	b.must(f, OpRevoke, FPBody{Fingerprint: fp(a)})
	st := b.c.State()
	if st.Founder != fp(f) || !st.Can(fp(f), OpHostAdd) {
		t.Fatal("founder state wrong")
	}
	if st.Can(fp(f), OpGenesis) {
		t.Fatal("genesis allowed after start")
	}
}

func TestAdminCanBanNotAddHost(t *testing.T) {
	f, a, c := newID(t), newID(t), newID(t)
	b := newBuilder(t, f)
	b.must(f, OpGrant, GrantBody{Fingerprint: fp(a), Roles: []string{"admin"}})
	b.must(a, OpBan, FPBody{Fingerprint: fp(c)})
	if st := b.c.State(); !st.Banned(fp(c)) {
		t.Fatal("not banned")
	}
	err := b.add(a, OpHostAdd, HostAddBody{PubKey: c.PublicKey(), Priority: 1})
	if !isRule(err) {
		t.Fatalf("want RuleError, got %v", err)
	}
}

func TestUserCannotGrant(t *testing.T) {
	f, u := newID(t), newID(t)
	b := newBuilder(t, f)
	if err := b.add(u, OpGrant, GrantBody{Fingerprint: fp(u), Roles: []string{"admin"}}); !isRule(err) {
		t.Fatalf("want RuleError, got %v", err)
	}
	if b.c.Head().Seq != 0 {
		t.Fatal("rejected entry was appended")
	}
}

func TestFounderCannotBeRevokedOrBanned(t *testing.T) {
	f, a := newID(t), newID(t)
	b := newBuilder(t, f)
	b.must(f, OpGrant, GrantBody{Fingerprint: fp(a), Roles: []string{"admin"}})
	for _, op := range []Op{OpRevoke, OpBan} {
		if err := b.add(a, op, FPBody{Fingerprint: fp(f)}); !isRule(err) {
			t.Errorf("%s admin->founder: %v", op, err)
		}
		if err := b.add(f, op, FPBody{Fingerprint: fp(f)}); !isRule(err) {
			t.Errorf("%s founder->founder: %v", op, err)
		}
	}
}

func TestConfigLatestWins(t *testing.T) {
	f := newID(t)
	b := newBuilder(t, f)
	if st := b.c.State(); st.HasConfig {
		t.Fatal("HasConfig before config")
	} else if r := st.Roles(); len(r.DefaultRoles) != 1 || r.DefaultRoles[0] != "user" {
		t.Fatalf("default roles %v", r.DefaultRoles)
	}
	b.must(f, OpConfig, ConfigBody{Name: "one", DefaultRoles: []string{"user"}})
	b.must(f, OpConfig, ConfigBody{Name: "two", Roles: map[string][]string{"vip": {"send_chat"}}, DefaultRoles: []string{"vip"}})
	st := b.c.State()
	if !st.HasConfig || st.Config.Name != "two" {
		t.Fatalf("config %+v", st.Config)
	}
	r := st.Roles()
	if !r.Has("anyone", roles.PermSendChat) || r.Has("anyone", roles.PermJoinChannel) || r.Has("anyone", roles.PermManage) {
		t.Fatal("override roles not applied")
	}
	if _, ok := r.Roles["admin"]; !ok {
		t.Fatal("builtin roles lost")
	}
}

func TestHostsSortedByPriority(t *testing.T) {
	f, h1, h2, h3 := newID(t), newID(t), newID(t), newID(t)
	b := newBuilder(t, f)
	b.must(f, OpHostAdd, HostAddBody{PubKey: h1.PublicKey(), Priority: 5})
	b.must(f, OpHostAdd, HostAddBody{PubKey: h2.PublicKey(), Priority: 2})
	b.must(f, OpHostAdd, HostAddBody{PubKey: h3.PublicKey(), Priority: 2})
	st := b.c.State()
	if len(st.Hosts) != 3 || st.Hosts[2].Priority != 5 || st.Hosts[0].Priority != 2 {
		t.Fatalf("hosts %+v", st.Hosts)
	}
	if st.Hosts[0].Fingerprint > st.Hosts[1].Fingerprint {
		t.Fatal("ties not sorted by fingerprint")
	}
	// re-adding updates priority
	b.must(f, OpHostAdd, HostAddBody{PubKey: h1.PublicKey(), Priority: 1})
	st = b.c.State()
	if len(st.Hosts) != 3 || st.Hosts[0].Fingerprint != fp(h1) || !st.IsHost(h1.PublicKey()) {
		t.Fatalf("update failed %+v", st.Hosts)
	}
	if err := b.add(f, OpHostAdd, HostAddBody{PubKey: h1.PublicKey(), Priority: 0}); !isRule(err) {
		t.Fatalf("priority 0: %v", err)
	}
	if err := b.add(f, OpHostRemove, FPBody{Fingerprint: "nope"}); !isRule(err) {
		t.Fatalf("unknown remove: %v", err)
	}
	b.must(f, OpHostRemove, FPBody{Fingerprint: fp(h1)})
	if st := b.c.State(); st.IsHost(h1.PublicKey()) {
		t.Fatal("still host")
	}
}

func TestGenesisRules(t *testing.T) {
	f, o := newID(t), newID(t)
	// author != founder
	e, _ := NewEntry(o, nil, OpGenesis, GenesisBody{Name: "x", Founder: f.PublicKey()}, t0)
	if _, err := New(e); !errors.Is(err, ErrBadChain) {
		t.Fatalf("author!=founder: %v", err)
	}
	// second genesis
	b := newBuilder(t, f)
	if err := b.add(f, OpGenesis, GenesisBody{Name: "x", Founder: f.PublicKey()}); !isRule(err) {
		t.Fatalf("second genesis: %v", err)
	}
}
