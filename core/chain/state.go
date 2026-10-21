package chain

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"sort"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/roles"
)

// RuleError reports an entry that the chain rules do not allow.
type RuleError struct{ Reason string }

func (e *RuleError) Error() string { return "chain: rule: " + e.Reason }

func ruleErr(format string, a ...any) error {
	return &RuleError{Reason: fmt.Sprintf(format, a...)}
}

// Host is a key allowed to run the server, with its failover priority
// (lower is preferred).
type Host struct {
	PubKey      ed25519.PublicKey
	Fingerprint string
	Priority    int
}

// State is the result of replaying a chain.
type State struct {
	ID        ID
	Founder   string // fingerprint
	Config    ConfigBody
	HasConfig bool
	Grants    map[string][]string
	Bans      map[string]bool
	Hosts     []Host // sorted by Priority, then Fingerprint

	started bool
}

// Roles returns the effective role configuration: the builtin roles with the
// config's overrides, the grants, and the default roles (["user"] until a
// config entry sets them).
func (s *State) Roles() roles.Config {
	rs := roles.Builtin()
	for name, perms := range s.Config.Roles {
		ps := make([]roles.Permission, len(perms))
		for i, p := range perms {
			ps[i] = roles.Permission(p)
		}
		rs[name] = ps
	}
	grants := make(map[string][]string, len(s.Grants))
	for k, v := range s.Grants {
		grants[k] = append([]string(nil), v...)
	}
	def := []string{"user"}
	if s.HasConfig {
		def = append([]string(nil), s.Config.DefaultRoles...)
	}
	return roles.Config{Roles: rs, Grants: grants, DefaultRoles: def, Founder: s.Founder}
}

// Can reports whether fp may append an entry with the given op.
func (s *State) Can(fp string, op Op) bool {
	if op == OpGenesis {
		return !s.started
	}
	if !s.started {
		return false
	}
	if fp == s.Founder && s.Founder != "" {
		return true
	}
	if s.Bans[fp] {
		return false
	}
	switch op {
	case OpConfig, OpGrant, OpRevoke, OpBan, OpUnban:
		r := s.Roles()
		return r.Has(fp, roles.PermManage)
	}
	return false // host_add, host_remove: founder only
}

// IsHost reports whether pub is in the host list.
func (s *State) IsHost(pub ed25519.PublicKey) bool {
	for _, h := range s.Hosts {
		if bytes.Equal(h.PubKey, pub) {
			return true
		}
	}
	return false
}

// Banned reports whether fp is banned.
func (s *State) Banned(fp string) bool { return s.Bans[fp] }

func (s *State) clone() State {
	c := *s
	c.Config.Channels = append([]string(nil), s.Config.Channels...)
	c.Config.VoiceChannels = append([]string(nil), s.Config.VoiceChannels...)
	c.Config.AllowedKeys = append([]string(nil), s.Config.AllowedKeys...)
	c.Config.DefaultRoles = append([]string(nil), s.Config.DefaultRoles...)
	if s.Config.Roles != nil {
		c.Config.Roles = make(map[string][]string, len(s.Config.Roles))
		for k, v := range s.Config.Roles {
			c.Config.Roles[k] = append([]string(nil), v...)
		}
	}
	c.Grants = make(map[string][]string, len(s.Grants))
	for k, v := range s.Grants {
		c.Grants[k] = append([]string(nil), v...)
	}
	c.Bans = make(map[string]bool, len(s.Bans))
	for k, v := range s.Bans {
		c.Bans[k] = v
	}
	c.Hosts = make([]Host, len(s.Hosts))
	for i, h := range s.Hosts {
		c.Hosts[i] = Host{append(ed25519.PublicKey(nil), h.PubKey...), h.Fingerprint, h.Priority}
	}
	return c
}

// Apply checks e's authority against the state before it and applies it. It
// does not check the signature or the chain links; Chain does. On error the
// state is unchanged.
func (s *State) Apply(e Entry) error {
	if len(e.Author) != ed25519.PublicKeySize {
		return ruleErr("bad author key")
	}
	author := identity.Fingerprint(ed25519.PublicKey(e.Author))

	if e.Op == OpGenesis {
		if s.started {
			return ruleErr("genesis after start")
		}
		if e.Seq != 0 {
			return ruleErr("genesis at seq %d", e.Seq)
		}
		var g GenesisBody
		if err := e.DecodeBody(&g); err != nil {
			return ruleErr("bad genesis body")
		}
		if len(g.Founder) != ed25519.PublicKeySize || !bytes.Equal(g.Founder, e.Author) {
			return ruleErr("genesis author is not the founder")
		}
		*s = State{
			ID:      IDOf(e),
			Founder: author,
			Grants:  map[string][]string{},
			Bans:    map[string]bool{},
			started: true,
		}
		return nil
	}
	if !s.started {
		return ruleErr("chain does not start with genesis")
	}
	if !s.Can(author, e.Op) {
		return ruleErr("%s not allowed for %s", e.Op, author)
	}

	// Work on a copy so a failed entry leaves s untouched.
	n := s.clone()
	switch e.Op {
	case OpConfig:
		var b ConfigBody
		if err := e.DecodeBody(&b); err != nil {
			return ruleErr("bad config body")
		}
		n.Config, n.HasConfig = b, true
	case OpGrant:
		var b GrantBody
		if err := e.DecodeBody(&b); err != nil || b.Fingerprint == "" {
			return ruleErr("bad grant body")
		}
		if b.Fingerprint == s.Founder && author != s.Founder {
			return ruleErr("only the founder may change the founder's roles")
		}
		n.Grants[b.Fingerprint] = append([]string(nil), b.Roles...)
	case OpRevoke, OpBan, OpUnban, OpHostRemove:
		var b FPBody
		if err := e.DecodeBody(&b); err != nil || b.Fingerprint == "" {
			return ruleErr("bad %s body", e.Op)
		}
		switch e.Op {
		case OpRevoke:
			if b.Fingerprint == s.Founder {
				return ruleErr("cannot revoke the founder")
			}
			delete(n.Grants, b.Fingerprint)
		case OpBan:
			if b.Fingerprint == s.Founder {
				return ruleErr("cannot ban the founder")
			}
			n.Bans[b.Fingerprint] = true
		case OpUnban:
			delete(n.Bans, b.Fingerprint)
		case OpHostRemove:
			i := -1
			for j, h := range n.Hosts {
				if h.Fingerprint == b.Fingerprint {
					i = j
				}
			}
			if i < 0 {
				return ruleErr("unknown host %s", b.Fingerprint)
			}
			n.Hosts = append(n.Hosts[:i], n.Hosts[i+1:]...)
		}
	case OpHostAdd:
		var b HostAddBody
		if err := e.DecodeBody(&b); err != nil {
			return ruleErr("bad host_add body")
		}
		if len(b.PubKey) != ed25519.PublicKeySize {
			return ruleErr("bad host key")
		}
		if b.Priority < 1 {
			return ruleErr("host priority must be >= 1")
		}
		found := false
		for i := range n.Hosts {
			if bytes.Equal(n.Hosts[i].PubKey, b.PubKey) {
				n.Hosts[i].Priority = b.Priority
				found = true
			}
		}
		if !found {
			n.Hosts = append(n.Hosts, Host{
				PubKey:      append(ed25519.PublicKey(nil), b.PubKey...),
				Fingerprint: identity.Fingerprint(ed25519.PublicKey(b.PubKey)),
				Priority:    b.Priority,
			})
		}
		sort.Slice(n.Hosts, func(i, j int) bool {
			if n.Hosts[i].Priority != n.Hosts[j].Priority {
				return n.Hosts[i].Priority < n.Hosts[j].Priority
			}
			return n.Hosts[i].Fingerprint < n.Hosts[j].Fingerprint
		})
	default:
		return ruleErr("unknown op %q", e.Op)
	}
	*s = n
	return nil
}
