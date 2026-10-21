package server

import (
	"crypto/rand"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/roles"
)

// groupKeys lists the TOML keys that describe the group and so live in the
// chain once one exists.
var groupKeys = []string{
	"server_name", "channels", "voice_channels", "max_voice", "max_clients",
	"max_per_ip", "allowed_keys", "roles", "default_roles", "grants",
}

// GroupFieldsSet returns the group keys that the TOML file at path sets, in a
// fixed order. Used to warn that an existing chain makes them inert.
func GroupFieldsSet(path string) ([]string, error) {
	var fc fileConfig
	md, err := toml.DecodeFile(path, &fc)
	if err != nil {
		return nil, fmt.Errorf("server: load config: %w", err)
	}
	var out []string
	for _, k := range groupKeys {
		if md.IsDefined(k) {
			out = append(out, k)
		}
	}
	return out, nil
}

// configBody renders cfg's group fields as a chain config body. Roles equal to
// the builtin definition are omitted: the chain layers overrides on builtins.
func configBody(cfg Config) chain.ConfigBody {
	b := chain.ConfigBody{
		Name:          cfg.ServerName,
		Channels:      cfg.Channels,
		VoiceChannels: cfg.VoiceChannels,
		MaxVoice:      cfg.MaxVoice,
		MaxClients:    cfg.MaxClients,
		MaxPerIP:      cfg.MaxPerIP,
		AllowedKeys:   cfg.AllowedKeys,
		DefaultRoles:  cfg.Roles.DefaultRoles,
	}
	builtin := roles.Builtin()
	for name, perms := range cfg.Roles.Roles {
		if bp, ok := builtin[name]; ok && reflect.DeepEqual(permStrings(bp), permStrings(perms)) {
			continue
		}
		if b.Roles == nil {
			b.Roles = map[string][]string{}
		}
		b.Roles[name] = permStrings(perms)
	}
	return normConfig(b)
}

func permStrings(ps []roles.Permission) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p)
	}
	return out
}

// normConfig turns empty slices and maps into nil so equal configs compare
// equal however they were built.
func normConfig(b chain.ConfigBody) chain.ConfigBody {
	n := func(s []string) []string {
		if len(s) == 0 {
			return nil
		}
		return s
	}
	b.Channels, b.VoiceChannels = n(b.Channels), n(b.VoiceChannels)
	b.AllowedKeys, b.DefaultRoles = n(b.AllowedKeys), n(b.DefaultRoles)
	if len(b.Roles) == 0 {
		b.Roles = nil
	}
	return b
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// OpenChain loads the chain at path, or creates it from cfg's group fields
// when the file does not exist: genesis (founder = key), one config, a grant
// per cfg.Roles.Grants entry, and host_add for key at priority 1.
func OpenChain(path string, key *identity.Identity, cfg Config) (c *chain.Chain, created bool, err error) {
	if _, serr := os.Stat(path); serr == nil {
		c, err = chain.Load(path)
		return c, false, err
	} else if !os.IsNotExist(serr) {
		return nil, false, serr
	}
	c, err = newChain(key, cfg)
	if err != nil {
		return nil, false, err
	}
	if err := c.SetPath(path); err != nil {
		return nil, false, err
	}
	return c, true, nil
}

// newChain builds an unpersisted chain from cfg with key as founder and sole host.
func newChain(key *identity.Identity, cfg Config) (*chain.Chain, error) {
	now := time.Now()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	g, err := chain.NewEntry(key, nil, chain.OpGenesis, chain.GenesisBody{
		Name:    cfg.ServerName,
		Founder: []byte(key.PublicKey()),
		Salt:    salt,
	}, now)
	if err != nil {
		return nil, err
	}
	c, err := chain.New(g)
	if err != nil {
		return nil, err
	}
	if _, err := ApplyTOML(c, key, cfg, groupKeys); err != nil {
		return nil, err
	}
	head := c.Head()
	h, err := chain.NewEntry(key, &head, chain.OpHostAdd,
		chain.HostAddBody{PubKey: []byte(key.PublicKey()), Priority: 1}, now)
	if err != nil {
		return nil, err
	}
	if err := c.Append(h); err != nil {
		return nil, err
	}
	return c, nil
}

// ApplyTOML appends to c the entries that bring its state in line with the
// group fields of cfg named in set (the keys the TOML file defines; see
// GroupFieldsSet). Fields not in set keep the chain's current values. A config
// entry is appended when a set field differs (always when the chain has no
// config yet); grant and revoke entries are produced only when "grants" is in
// set. It returns one description per appended entry ("config",
// "grant <fp>", "revoke <fp>") and refuses with a *chain.RuleError when key may
// not change the config.
func ApplyTOML(c *chain.Chain, key *identity.Identity, cfg Config, set []string) (applied []string, err error) {
	st := c.State()
	fp := identity.Fingerprint(key.PublicKey())
	if !st.Can(fp, chain.OpConfig) {
		return nil, &chain.RuleError{Reason: fmt.Sprintf("%s may not change the server config", fp)}
	}
	isSet := make(map[string]bool, len(set))
	for _, k := range set {
		isSet[k] = true
	}
	now := time.Now()
	add := func(desc string, op chain.Op, body any) error {
		head := c.Head()
		e, err := chain.NewEntry(key, &head, op, body, now)
		if err != nil {
			return err
		}
		if err := c.Append(e); err != nil {
			return err
		}
		applied = append(applied, desc)
		return nil
	}

	cfgBody := configBody(cfg)
	want := cfgBody
	if st.HasConfig {
		want = normConfig(st.Config)
		if isSet["server_name"] {
			want.Name = cfgBody.Name
		}
		if isSet["channels"] {
			want.Channels = cfgBody.Channels
		}
		if isSet["voice_channels"] {
			want.VoiceChannels = cfgBody.VoiceChannels
		}
		if isSet["max_voice"] {
			want.MaxVoice = cfgBody.MaxVoice
		}
		if isSet["max_clients"] {
			want.MaxClients = cfgBody.MaxClients
		}
		if isSet["max_per_ip"] {
			want.MaxPerIP = cfgBody.MaxPerIP
		}
		if isSet["allowed_keys"] {
			want.AllowedKeys = cfgBody.AllowedKeys
		}
		if isSet["roles"] {
			want.Roles = cfgBody.Roles
		}
		if isSet["default_roles"] {
			want.DefaultRoles = cfgBody.DefaultRoles
		}
		want = normConfig(want)
	}
	if !st.HasConfig || !reflect.DeepEqual(want, normConfig(st.Config)) {
		if err := add("config", chain.OpConfig, want); err != nil {
			return applied, err
		}
	}
	if !isSet["grants"] {
		return applied, nil
	}

	fps := make([]string, 0, len(cfg.Roles.Grants))
	for f := range cfg.Roles.Grants {
		fps = append(fps, f)
	}
	sort.Strings(fps)
	for _, f := range fps {
		if cur, ok := st.Grants[f]; ok && sameStrings(cur, cfg.Roles.Grants[f]) {
			continue
		}
		if err := add("grant "+f, chain.OpGrant, chain.GrantBody{Fingerprint: f, Roles: cfg.Roles.Grants[f]}); err != nil {
			return applied, err
		}
	}
	var stale []string
	for f := range st.Grants {
		if _, ok := cfg.Roles.Grants[f]; !ok && f != st.Founder {
			stale = append(stale, f)
		}
	}
	sort.Strings(stale)
	for _, f := range stale {
		if err := add("revoke "+f, chain.OpRevoke, chain.FPBody{Fingerprint: f}); err != nil {
			return applied, err
		}
	}
	return applied, nil
}

// groupState is the server's view of its chain, kept current by refresh. All
// readers go through accessors so live chain updates can swap it atomically.
type groupState struct {
	mu    sync.RWMutex
	roles roles.Config
	bans  map[string]bool
}

// roles returns the effective role configuration.
func (s *Server) roles() roles.Config {
	s.group.mu.RLock()
	defer s.group.mu.RUnlock()
	return s.group.roles
}

// banned reports whether fp is banned by the chain.
func (s *Server) banned(fp string) bool {
	s.group.mu.RLock()
	defer s.group.mu.RUnlock()
	return s.group.bans[fp]
}

// refreshGroup re-reads roles and bans from the chain.
func (s *Server) refreshGroup() {
	// State is read under the lock so concurrent refreshes cannot store an
	// older snapshot last.
	s.group.mu.Lock()
	defer s.group.mu.Unlock()
	st := s.chain.State()
	s.group.roles, s.group.bans = st.Roles(), st.Bans
}

// chainCfg returns cfg with its group fields replaced by the chain's. A chain
// with no config entry yet leaves cfg's own group fields in place.
func chainCfg(cfg Config, st chain.State) Config {
	if !st.HasConfig {
		cfg.Roles = st.Roles()
		return cfg
	}
	b := st.Config
	cfg.ServerName = b.Name
	cfg.Channels, cfg.VoiceChannels = b.Channels, b.VoiceChannels
	cfg.MaxVoice, cfg.MaxClients, cfg.MaxPerIP = b.MaxVoice, b.MaxClients, b.MaxPerIP
	cfg.AllowedKeys = b.AllowedKeys
	cfg.Roles = st.Roles()
	return cfg
}
