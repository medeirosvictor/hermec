package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

func chainTestCfg(grants map[string][]string) Config {
	cfg := DefaultConfig()
	cfg.ServerName = "primos"
	cfg.Channels = []string{"general", "dev"}
	cfg.VoiceChannels = []string{"lounge"}
	cfg.Roles.Grants = grants
	return cfg
}

func TestOpenChainCreatesFromConfig(t *testing.T) {
	key := mustID(t)
	other := identity.Fingerprint(mustID(t).PublicKey())
	cfg := chainTestCfg(map[string][]string{other: {"admin"}})
	path := filepath.Join(t.TempDir(), "chain.jsonl")

	c, created, err := OpenChain(path, key, cfg)
	if err != nil || !created {
		t.Fatalf("OpenChain = created %v, err %v", created, err)
	}
	st := c.State()
	if st.Config.Name != "primos" || !reflect.DeepEqual(st.Config.Channels, cfg.Channels) ||
		!reflect.DeepEqual(st.Config.VoiceChannels, cfg.VoiceChannels) {
		t.Errorf("config = %+v", st.Config)
	}
	if !reflect.DeepEqual(st.Grants, map[string][]string{other: {"admin"}}) {
		t.Errorf("grants = %v", st.Grants)
	}
	if len(st.Hosts) != 1 || st.Hosts[0].Priority != 1 || !st.IsHost(key.PublicKey()) {
		t.Errorf("hosts = %+v", st.Hosts)
	}
	if st.Founder != identity.Fingerprint(key.PublicKey()) {
		t.Errorf("founder = %q", st.Founder)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("chain file not written: %v", err)
	}
}

func TestOpenChainReopenKeepsAppendedGrant(t *testing.T) {
	key := mustID(t)
	cfg := chainTestCfg(nil)
	path := filepath.Join(t.TempDir(), "chain.jsonl")
	c, _, err := OpenChain(path, key, cfg)
	if err != nil {
		t.Fatal(err)
	}
	x := identity.Fingerprint(mustID(t).PublicKey())
	head := c.Head()
	e, err := chain.NewEntry(key, &head, chain.OpGrant, chain.GrantBody{Fingerprint: x, Roles: []string{"bot"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Append(e); err != nil {
		t.Fatal(err)
	}
	id := c.ID()

	c2, created, err := OpenChain(path, key, cfg)
	if err != nil || created {
		t.Fatalf("reopen = created %v, err %v", created, err)
	}
	if got := c2.State().Grants[x]; !reflect.DeepEqual(got, []string{"bot"}) {
		t.Errorf("grant for X = %v", got)
	}
	if c2.ID() != id {
		t.Error("server ID changed on reopen")
	}
}

func TestApplyTOMLAppendsDiffOnly(t *testing.T) {
	key := mustID(t)
	cfg := chainTestCfg(nil)
	c, _, err := OpenChain(filepath.Join(t.TempDir(), "chain.jsonl"), key, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := ApplyTOML(c, key, cfg); err != nil || n != 0 {
		t.Fatalf("unchanged cfg: n = %d, err = %v", n, err)
	}
	before := len(c.Entries())
	cfg.Channels = []string{"general", "dev", "ops"}
	if n, err := ApplyTOML(c, key, cfg); err != nil || n != 1 {
		t.Fatalf("changed channels: n = %d, err = %v", n, err)
	}
	if es := c.Entries(); len(es) != before+1 || es[len(es)-1].Op != chain.OpConfig {
		t.Errorf("entries = %d, last op %q", len(es), es[len(es)-1].Op)
	}
	if n, err := ApplyTOML(c, key, cfg); err != nil || n != 0 {
		t.Fatalf("second apply: n = %d, err = %v", n, err)
	}
	// Grant differences: add one, then drop it.
	x := identity.Fingerprint(mustID(t).PublicKey())
	cfg.Roles.Grants = map[string][]string{x: {"admin"}}
	if n, err := ApplyTOML(c, key, cfg); err != nil || n != 1 {
		t.Fatalf("add grant: n = %d, err = %v", n, err)
	}
	cfg.Roles.Grants = nil
	if n, err := ApplyTOML(c, key, cfg); err != nil || n != 1 {
		t.Fatalf("revoke grant: n = %d, err = %v", n, err)
	}
	if st := c.State(); len(st.Grants) != 0 {
		t.Errorf("grants = %v", st.Grants)
	}
}

func TestApplyTOMLNoAuthority(t *testing.T) {
	key := mustID(t)
	cfg := chainTestCfg(nil)
	c, _, err := OpenChain(filepath.Join(t.TempDir(), "chain.jsonl"), key, cfg)
	if err != nil {
		t.Fatal(err)
	}
	before := len(c.Entries())
	cfg.Channels = []string{"other"}
	_, err = ApplyTOML(c, mustID(t), cfg)
	var re *chain.RuleError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want RuleError", err)
	}
	if len(c.Entries()) != before {
		t.Error("chain changed")
	}
}

func TestNewDerivesGroupConfigFromChain(t *testing.T) {
	key := mustID(t)
	chainCfg := chainTestCfg(nil)
	chainCfg.ServerName = "b"
	c, _, err := OpenChain(filepath.Join(t.TempDir(), "chain.jsonl"), key, chainCfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg := chainTestCfg(nil)
	cfg.ServerName = "a"
	cfg.Channels = []string{"ignored"}
	cfg.HostKey, cfg.Chain = key, c
	url := startServer(t, cfg)
	_, env := authAs(t, url, mustID(t), "")
	var ok proto.AuthOK
	if err := json.Unmarshal(env.Data, &ok); err != nil {
		t.Fatal(err)
	}
	if ok.ServerName != "b" {
		t.Errorf("server_name = %q, want b", ok.ServerName)
	}
	var names []string
	for _, ch := range ok.Channels {
		names = append(names, ch.Name)
	}
	if !reflect.DeepEqual(names, []string{"general", "dev", "lounge"}) {
		t.Errorf("channels = %v", names)
	}
}

func TestNewWithoutChainStillWorks(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Discoverable = false
	url := startServer(t, cfg)
	_, env := authAs(t, url, mustID(t), "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("type = %q, want auth_ok", env.Type)
	}
}

func TestInMemoryChainKeepsCustomRoles(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Roles = roles.Config{
		Roles:        map[string][]roles.Permission{"listener": {roles.PermJoinChannel}},
		DefaultRoles: []string{"listener"},
	}
	s := New(cfg)
	if got := s.roles().RolesFor("anyone"); !reflect.DeepEqual(got, []string{"listener"}) {
		t.Errorf("roles = %v", got)
	}
	if s.roles().Has("anyone", roles.PermSendChat) {
		t.Error("listener may send chat")
	}
}

func TestGroupFieldsSet(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.toml")
	if err := os.WriteFile(p, []byte("addr = \":1\"\nserver_name = \"x\"\nchannels = [\"a\"]\nmax_voice = 3\n[grants]\n\"fp\" = [\"admin\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := GroupFieldsSet(p)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"server_name", "channels", "max_voice", "grants"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
