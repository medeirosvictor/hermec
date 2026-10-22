package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

// groupKeys lists the TOML keys that describe the group and so live in the
// chain once one exists.
var groupKeys = []string{
	"server_name", "channels", "voice_channels", "max_voice", "max_clients",
	"max_per_ip", "allowed_keys", "roles", "default_roles", "grants",
	"history_days", "history_mb",
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
		HistoryDays:   cfg.HistoryDays,
		HistoryMB:     cfg.HistoryMB,
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

// CheckHost reports whether key is on c's host list. A server run with any
// other key would fail every client's host check, so callers refuse to start.
func CheckHost(c *chain.Chain, key *identity.Identity) error {
	st := c.State()
	if st.IsHost(key.PublicKey()) {
		return nil
	}
	return fmt.Errorf("this key is not an approved host of server %s (key %s)",
		c.ID().Short(), identity.Fingerprint(key.PublicKey()))
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
		if isSet["history_days"] {
			want.HistoryDays = cfgBody.HistoryDays
		}
		if isSet["history_mb"] {
			want.HistoryMB = cfgBody.HistoryMB
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
	mu      sync.RWMutex
	roles   roles.Config
	bans    map[string]bool
	allowed map[string]struct{} // nil: any key may connect
	exempt  map[string]struct{} // the founder and the hosts: allowed_keys never applies
}

// keyAllowed reports whether fp passes the allowed_keys gate. The founder and
// every listed host always pass, so an allowlist cannot lock the group out.
func (s *Server) keyAllowed(fp string) bool {
	s.group.mu.RLock()
	defer s.group.mu.RUnlock()
	if s.group.allowed == nil {
		return true
	}
	if _, ok := s.group.exempt[fp]; ok {
		return true
	}
	_, ok := s.group.allowed[fp]
	return ok
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
	keys := s.cfg.AllowedKeys // chainCfg already folded the chain's list in at New
	if st.HasConfig {
		keys = st.Config.AllowedKeys
	}
	s.group.allowed = nil
	s.group.exempt = map[string]struct{}{}
	if st.Founder != "" {
		s.group.exempt[st.Founder] = struct{}{}
	}
	for _, h := range st.Hosts {
		s.group.exempt[h.Fingerprint] = struct{}{}
	}
	if len(keys) > 0 {
		s.group.allowed = make(map[string]struct{}, len(keys))
		for _, fp := range keys {
			s.group.allowed[fp] = struct{}{}
		}
	}
}

// handleChainAppend processes a chain_append from c: the entry must be c's own
// and extend the head under the chain's rules. On success every authenticated
// connection receives it and its effects (roles, bans, allowed_keys) apply at
// once, as do the history limits; name, channels and the other limits of a
// config entry apply at the next start.
func (c *conn) handleChainAppend(env proto.Envelope) {
	s := c.srv
	var m proto.ChainAppend
	var e chain.Entry
	if json.Unmarshal(env.Data, &m) != nil || json.Unmarshal(m.Entry, &e) != nil {
		c.sendError(proto.CodeBadRequest, "malformed chain_append")
		return
	}
	if len(e.Author) != ed25519.PublicKeySize || identity.Fingerprint(ed25519.PublicKey(e.Author)) != c.fingerprint {
		c.sendError(proto.CodeForbidden, "entries may only be appended by their author")
		return
	}
	frames, err := chainFrames([]chain.Entry{e})
	if err != nil || len(frames) != 1 {
		c.sendError(proto.CodeBadRequest, "malformed chain entry")
		return
	}

	// One append at a time, so connections see entries in chain order.
	s.chainMu.Lock()
	err = s.chain.Append(e)
	var ruleErr *chain.RuleError
	switch {
	case err == nil:
	case errors.Is(err, chain.ErrConflict):
		s.chainMu.Unlock()
		c.sendError(proto.CodeChainConflict, "the chain moved on; retry")
		return
	case errors.Is(err, chain.ErrFull):
		s.chainMu.Unlock()
		c.sendError(proto.CodeChainFull, "the chain is full")
		return
	case errors.As(err, &ruleErr):
		s.chainMu.Unlock()
		c.sendError(proto.CodeForbidden, ruleErr.Reason)
		return
	default:
		s.chainMu.Unlock()
		s.logf("server: chain_append from %s: %v", c.fingerprint, err)
		c.sendError(proto.CodeBadRequest, "entry rejected")
		return
	}
	s.refreshGroup()
	if s.applyHistoryLimits() {
		// New caps take effect at once, not at the next hourly trim. Done
		// before the broadcast, outside chMu (History is a leaf lock).
		s.trimHistory()
	}
	s.chMu.Lock()
	dead := s.broadcastChainLocked(frames[0])
	s.chMu.Unlock()
	dropSlow(dead)
	if e.Op == chain.OpHostRemove {
		s.dropRemovedLinks()
	}
	if e.Op == chain.OpBan {
		var b chain.FPBody
		if e.DecodeBody(&b) == nil {
			s.kick(b.Fingerprint)
		}
	}
	s.chainMu.Unlock()
}

// kick tells every connection of fp that it is banned and ends it. The voice
// seat is released here; the rest of the cleanup runs when the connection's
// read loop sees the socket close.
func (s *Server) kick(fp string) {
	s.chMu.Lock()
	var hit []*conn
	for c := range s.authed {
		if c.fingerprint == fp {
			hit = append(hit, c)
		}
	}
	for c := range s.warm {
		if c.fingerprint == fp {
			hit = append(hit, c)
		}
	}
	s.chMu.Unlock()
	for _, c := range hit {
		c.sendError(proto.CodeBanned, "you are banned from this server")
		s.voiceLeave(c)
		c.closeSend()
	}
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
	cfg.HistoryDays, cfg.HistoryMB = b.HistoryDays, b.HistoryMB
	cfg.Roles = st.Roles()
	return cfg
}
