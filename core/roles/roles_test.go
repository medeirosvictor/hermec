package roles

import (
	"testing"
)

func TestHasViaGrant(t *testing.T) {
	// grant fp→["admin"] with Builtin(); Has(fp, PermManage) true
	fp := "test-fingerprint"
	cfg := Config{
		Roles:  Builtin(),
		Grants: map[string][]string{fp: {"admin"}},
	}

	if !cfg.Has(fp, PermManage) {
		t.Errorf("Has(%q, PermManage) = false, want true", fp)
	}
}

func TestDefaultRoles(t *testing.T) {
	// ungranted fp, DefaultRoles ["user"]; Has join/chat true, manage false
	fp := "ungranted-fingerprint"
	cfg := Config{
		Roles:        Builtin(),
		Grants:       map[string][]string{},
		DefaultRoles: []string{"user"},
	}

	if !cfg.Has(fp, PermJoinChannel) {
		t.Errorf("Has(%q, PermJoinChannel) = false, want true", fp)
	}
	if !cfg.Has(fp, PermSendChat) {
		t.Errorf("Has(%q, PermSendChat) = false, want true", fp)
	}
	if cfg.Has(fp, PermManage) {
		t.Errorf("Has(%q, PermManage) = true, want false", fp)
	}
}

func TestNoDefaultMeansNothing(t *testing.T) {
	// empty DefaultRoles; ungranted fp has no permissions
	fp := "ungranted-fingerprint"
	cfg := Config{
		Roles:        Builtin(),
		Grants:       map[string][]string{},
		DefaultRoles: []string{},
	}

	if cfg.Has(fp, PermJoinChannel) {
		t.Errorf("Has(%q, PermJoinChannel) = true, want false", fp)
	}
	if cfg.Has(fp, PermSendChat) {
		t.Errorf("Has(%q, PermSendChat) = true, want false", fp)
	}
	if cfg.Has(fp, PermManage) {
		t.Errorf("Has(%q, PermManage) = true, want false", fp)
	}
}

func TestUnknownRoleIgnored(t *testing.T) {
	// grant fp→["ghost"]; Has anything false, no panic
	fp := "test-fingerprint"
	cfg := Config{
		Roles:  Builtin(),
		Grants: map[string][]string{fp: {"ghost"}},
	}

	if cfg.Has(fp, PermJoinChannel) {
		t.Errorf("Has(%q, PermJoinChannel) = true, want false", fp)
	}
	if cfg.Has(fp, PermSendChat) {
		t.Errorf("Has(%q, PermSendChat) = true, want false", fp)
	}
	if cfg.Has(fp, PermManage) {
		t.Errorf("Has(%q, PermManage) = true, want false", fp)
	}
}
