package roles

import "testing"

func TestFounderHasEverything(t *testing.T) {
	cfg := Config{Roles: Builtin(), Grants: map[string][]string{"f": nil}, Founder: "f"}
	for _, p := range []Permission{PermJoinChannel, PermSendChat, PermManage} {
		if !cfg.Has("f", p) {
			t.Errorf("founder lacks %s", p)
		}
		if cfg.Has("other", p) {
			t.Errorf("non-founder has %s with no default roles", p)
		}
	}
	if (Config{Roles: Builtin()}).Has("", PermManage) {
		t.Error("empty fingerprint matched an unset founder")
	}
}
