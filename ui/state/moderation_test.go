package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

type modWorld struct {
	cs                          chain.State
	founder, admin, user, user2 string
}

// newModWorld builds a real chain: a founder, an admin (granted), two users.
// mutate may append more entries as the founder.
func newModWorld(t *testing.T, mutate func(add func(chain.Op, any), w *modWorld)) *modWorld {
	t.Helper()
	fid, _ := identity.Generate()
	aid, _ := identity.Generate()
	uid, _ := identity.Generate()
	u2, _ := identity.Generate()
	g, err := chain.NewEntry(fid, nil, chain.OpGenesis, chain.GenesisBody{Name: "s", Founder: fid.PublicKey(), Salt: []byte("12345678")}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c, err := chain.New(g)
	if err != nil {
		t.Fatal(err)
	}
	w := &modWorld{founder: identity.Fingerprint(fid.PublicKey()), admin: identity.Fingerprint(aid.PublicKey()),
		user: identity.Fingerprint(uid.PublicKey()), user2: identity.Fingerprint(u2.PublicKey())}
	add := func(op chain.Op, body any) {
		h := c.Head()
		e, err := chain.NewEntry(fid, &h, op, body, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	add(chain.OpGrant, chain.GrantBody{Fingerprint: w.admin, Roles: []string{"admin"}})
	if mutate != nil {
		mutate(add, w)
	}
	w.cs = c.State()
	return w
}

func labels(its []ModItem) string {
	var s []string
	for _, it := range its {
		s = append(s, it.Label)
	}
	return strings.Join(s, ",")
}

func TestModItemsWhoMayActOnWhom(t *testing.T) {
	w := newModWorld(t, nil)
	cases := []struct {
		name, viewer, target, want string
	}{
		{"founder on user", w.founder, w.user, "Make admin,Ban…"},
		{"founder on admin", w.founder, w.admin, "Remove admin,Ban…"},
		{"admin on user", w.admin, w.user, "Make admin,Ban…"},
		{"admin on founder", w.admin, w.founder, ""},
		{"founder on self", w.founder, w.founder, ""},
		{"admin on self", w.admin, w.admin, ""},
		{"user on user", w.user, w.user2, ""},
		{"user on admin", w.user, w.admin, ""},
		{"user on founder", w.user, w.founder, ""},
		{"nobody", "", w.user, ""},
	}
	for _, tc := range cases {
		if got := labels(ModItems(w.cs, tc.viewer, tc.target)); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestModItemsRoleBodiesKeepOtherRoles(t *testing.T) {
	w := newModWorld(t, func(add func(chain.Op, any), w *modWorld) {
		add(chain.OpGrant, chain.GrantBody{Fingerprint: w.user, Roles: []string{"bot"}})
		add(chain.OpGrant, chain.GrantBody{Fingerprint: w.user2, Roles: []string{"admin", "bot"}})
	})
	mk := ModItems(w.cs, w.founder, w.user)[0].Body.(chain.GrantBody)
	if strings.Join(mk.Roles, ",") != "bot,admin" {
		t.Errorf("make admin roles = %v", mk.Roles)
	}
	rm := ModItems(w.cs, w.founder, w.user2)[0].Body.(chain.GrantBody)
	if strings.Join(rm.Roles, ",") != "bot" {
		t.Errorf("remove admin roles = %v", rm.Roles)
	}
	only := ModItems(w.cs, w.founder, w.admin)[0].Body.(chain.GrantBody)
	if strings.Join(only.Roles, ",") != "user" {
		t.Errorf("remove admin from sole role = %v", only.Roles)
	}
}

func TestModItemsHiddenForBannedAndBannedViewer(t *testing.T) {
	w := newModWorld(t, func(add func(chain.Op, any), w *modWorld) {
		add(chain.OpBan, chain.FPBody{Fingerprint: w.user})
		add(chain.OpBan, chain.FPBody{Fingerprint: w.admin})
	})
	if got := ModItems(w.cs, w.founder, w.user); got != nil {
		t.Errorf("banned target got items %v", labels(got))
	}
	if got := ModItems(w.cs, w.admin, w.user2); got != nil {
		t.Errorf("banned admin got items %v", labels(got))
	}
	if IsManager(w.cs, w.admin) || !IsManager(w.cs, w.founder) {
		t.Error("IsManager wrong")
	}
}

func TestBannedRows(t *testing.T) {
	w := newModWorld(t, func(add func(chain.Op, any), w *modWorld) {
		add(chain.OpBan, chain.FPBody{Fingerprint: w.user})
		add(chain.OpBan, chain.FPBody{Fingerprint: w.user2})
	})
	names := map[string]string{w.user: "zed"}
	rows := BannedRows(w.cs, w.founder, names)
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	var named, bare BannedRow
	for _, r := range rows {
		if r.FP == w.user {
			named = r
		} else {
			bare = r
		}
	}
	if named.Label != "zed ("+w.user+")" || bare.Label != w.user2 {
		t.Errorf("labels %q %q", named.Label, bare.Label)
	}
	if named.Item.Op != chain.OpUnban || named.Item.Body.(chain.FPBody).Fingerprint != w.user {
		t.Errorf("unban item = %+v", named.Item)
	}
	if BannedRows(w.cs, w.user2, names) != nil || BannedRows(w.cs, w.admin, names) == nil {
		t.Error("only managers see the list")
	}
}

func TestKnownNames(t *testing.T) {
	st := New()
	st.Messages["general"] = []Message{{FromFP: "a", FromName: "old"}, {FromFP: "b", FromName: "bee"}}
	st.Roster = []proto.Member{{Fingerprint: "a", Name: "new"}}
	n := KnownNames(st)
	if n["a"] != "new" || n["b"] != "bee" {
		t.Errorf("names = %v", n)
	}
}

func TestModOutcome(t *testing.T) {
	ban := ModItem{Kind: ModBan}
	if got := ModOutcome(ban, "X", nil); got != "banned X" {
		t.Error(got)
	}
	err := &client.ServerError{Code: proto.CodeForbidden, Message: "ban not allowed for k7mv"}
	if got := ModOutcome(ban, "X", err); got != "couldn't ban X: you don't have permission" {
		t.Error(got)
	}
	if got := ModOutcome(ModItem{Kind: ModUnban}, "X", errors.New("socket: reset")); strings.Contains(got, "socket") || !strings.HasPrefix(got, "couldn't unban X") {
		t.Error(got)
	}
}
