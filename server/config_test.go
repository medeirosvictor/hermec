package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/medeirosvictor/hermec/core/roles"
)

func writeTOML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "server.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(writeTOML(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":7697" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if !reflect.DeepEqual(cfg.Channels, []string{"general"}) {
		t.Errorf("Channels = %v", cfg.Channels)
	}
	if !reflect.DeepEqual(cfg.Roles.DefaultRoles, []string{"user"}) {
		t.Errorf("DefaultRoles = %v", cfg.Roles.DefaultRoles)
	}
	if !reflect.DeepEqual(cfg.Roles.Roles, roles.Builtin()) {
		t.Errorf("Roles = %v", cfg.Roles.Roles)
	}
	if cfg.Password != "" || len(cfg.AllowedKeys) != 0 || cfg.TLSCert != "" || cfg.TLSKey != "" {
		t.Errorf("unexpected non-zero fields: %+v", cfg)
	}
}

func TestLoadConfigVoiceChannels(t *testing.T) {
	cfg, err := LoadConfig(writeTOML(t, `voice_channels = ["lounge"]`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.VoiceChannels, []string{"lounge"}) {
		t.Errorf("VoiceChannels = %v", cfg.VoiceChannels)
	}
	def, _ := LoadConfig(writeTOML(t, ""))
	if len(def.VoiceChannels) != 0 {
		t.Errorf("default VoiceChannels = %v", def.VoiceChannels)
	}
	if _, err := LoadConfig(writeTOML(t, "channels = [\"a\"]\nvoice_channels = [\"a\"]")); err == nil {
		t.Error("expected name collision error")
	}
	if _, err := LoadConfig(writeTOML(t, `voice_channels = ["a", "a"]`)); err == nil {
		t.Error("expected duplicate voice channel error")
	}
}

func TestLoadConfigFull(t *testing.T) {
	p := writeTOML(t, `
addr = "127.0.0.1:9000"
password = "hunter2"
allowed_keys = ["aaaa-bbbb", "cccc-dddd"]
channels = ["lobby", "dev"]
tls_cert = "cert.pem"
tls_key = "key.pem"
default_roles = ["guest"]

[roles]
guest = ["join_channel"]
admin = ["join_channel", "send_chat", "manage"]

[grants]
"aaaa-bbbb" = ["admin"]
`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Addr:        "127.0.0.1:9000",
		Password:    "hunter2",
		AllowedKeys: []string{"aaaa-bbbb", "cccc-dddd"},
		Channels:    []string{"lobby", "dev"},
		TLSCert:     "cert.pem",
		TLSKey:      "key.pem",
		MsgRate:     DefaultMsgRate,
		MsgBurst:    DefaultMsgBurst,
		Roles: roles.Config{
			Roles: map[string][]roles.Permission{
				"admin": {roles.PermJoinChannel, roles.PermSendChat, roles.PermManage},
				"user":  {roles.PermJoinChannel, roles.PermSendChat},
				"bot":   {roles.PermJoinChannel, roles.PermSendChat},
				"guest": {roles.PermJoinChannel},
			},
			Grants:       map[string][]string{"aaaa-bbbb": {"admin"}},
			DefaultRoles: []string{"guest"},
		},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got  %+v\nwant %+v", cfg, want)
	}
}

func TestLoadConfigTLSBothOrNeither(t *testing.T) {
	for _, body := range []string{`tls_cert = "c.pem"`, `tls_key = "k.pem"`} {
		if _, err := LoadConfig(writeTOML(t, body)); err == nil {
			t.Errorf("expected error for %q", body)
		}
	}
}

func TestLoadConfigErrors(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Error("missing file: expected error")
	}
	if _, err := LoadConfig(writeTOML(t, `addr = 5`)); err == nil {
		t.Error("bad type: expected error")
	}
	if _, err := LoadConfig(writeTOML(t, `adr = ":1"`)); err == nil {
		t.Error("unknown key: expected error")
	}
}
