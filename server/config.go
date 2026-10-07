package server

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/discover"
)

// DefaultAddr is the listen address used when none is configured.
const DefaultAddr = ":7697"

// fileConfig is the on-disk TOML shape of Config.
type fileConfig struct {
	Addr          string              `toml:"addr"`
	Password      string              `toml:"password"`
	AllowedKeys   []string            `toml:"allowed_keys"`
	Channels      []string            `toml:"channels"`
	VoiceChannels []string            `toml:"voice_channels"`
	TLSCert       string              `toml:"tls_cert"`
	TLSKey        string              `toml:"tls_key"`
	PublicIP      string              `toml:"public_ip"`
	UDPPortMin    uint16              `toml:"udp_port_min"`
	UDPPortMax    uint16              `toml:"udp_port_max"`
	DefaultRoles  []string            `toml:"default_roles"`
	Roles         map[string][]string `toml:"roles"`
	Grants        map[string][]string `toml:"grants"`
	MsgRate       int                 `toml:"msg_rate"`
	MsgBurst      int                 `toml:"msg_burst"`
	Discoverable  *bool               `toml:"discoverable"`
	ServerName    string              `toml:"server_name"`
}

// DefaultConfig returns the configuration used when no file is given.
func DefaultConfig() Config {
	return Config{
		Addr:         DefaultAddr,
		Channels:     []string{"general"},
		MsgRate:      DefaultMsgRate,
		MsgBurst:     DefaultMsgBurst,
		Discoverable: true,
		Roles: roles.Config{
			Roles:        roles.Builtin(),
			DefaultRoles: []string{"user"},
		},
	}
}

// LoadConfig reads a TOML server config from path. Omitted keys take their
// defaults. Roles listed under [roles] are added to (or override) the builtin
// roles. Unknown keys are rejected so typos do not silently weaken a server.
func LoadConfig(path string) (Config, error) {
	var fc fileConfig
	md, err := toml.DecodeFile(path, &fc)
	if err != nil {
		return Config{}, fmt.Errorf("server: load config: %w", err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		keys := make([]string, len(un))
		for i, k := range un {
			keys[i] = k.String()
		}
		return Config{}, fmt.Errorf("server: load config: unknown keys: %s", strings.Join(keys, ", "))
	}

	cfg := DefaultConfig()
	if fc.Addr != "" {
		cfg.Addr = fc.Addr
	}
	cfg.Password = fc.Password
	cfg.AllowedKeys = fc.AllowedKeys
	if md.IsDefined("channels") {
		cfg.Channels = fc.Channels
	}
	cfg.VoiceChannels = fc.VoiceChannels
	seen := make(map[string]struct{}, len(cfg.Channels))
	for _, n := range cfg.Channels {
		seen[n] = struct{}{}
	}
	for _, n := range cfg.VoiceChannels {
		if _, dup := seen[n]; dup {
			return Config{}, fmt.Errorf("server: load config: channel %q is duplicated across channels and voice_channels", n)
		}
		seen[n] = struct{}{}
	}
	cfg.TLSCert, cfg.TLSKey = fc.TLSCert, fc.TLSKey
	cfg.PublicIP = fc.PublicIP
	cfg.UDPPortMin, cfg.UDPPortMax = fc.UDPPortMin, fc.UDPPortMax
	if (cfg.UDPPortMin == 0) != (cfg.UDPPortMax == 0) || cfg.UDPPortMin > cfg.UDPPortMax {
		return Config{}, errors.New("server: load config: udp_port_min and udp_port_max must be set together with min <= max")
	}
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		return Config{}, errors.New("server: load config: tls_cert and tls_key must be set together")
	}
	for name, perms := range fc.Roles {
		ps := make([]roles.Permission, len(perms))
		for i, p := range perms {
			ps[i] = roles.Permission(p)
		}
		cfg.Roles.Roles[name] = ps
	}
	if fc.MsgRate < 0 || fc.MsgBurst < 0 {
		return Config{}, errors.New("server: load config: msg_rate and msg_burst must not be negative")
	}
	if md.IsDefined("msg_rate") {
		cfg.MsgRate = fc.MsgRate
	}
	if md.IsDefined("msg_burst") {
		cfg.MsgBurst = fc.MsgBurst
	}
	if utf8.RuneCountInString(fc.ServerName) > discover.MaxNameRunes {
		return Config{}, fmt.Errorf("server: load config: server_name must be at most %d characters", discover.MaxNameRunes)
	}
	cfg.ServerName = fc.ServerName
	if md.IsDefined("discoverable") {
		cfg.Discoverable = *fc.Discoverable
	}
	cfg.Roles.Grants = fc.Grants
	if md.IsDefined("default_roles") {
		cfg.Roles.DefaultRoles = fc.DefaultRoles
	}
	return cfg, nil
}

// MarshalTOML renders c in the same TOML shape LoadConfig reads.
func (c Config) MarshalTOML() ([]byte, error) {
	fc := fileConfig{
		Addr:          c.Addr,
		Password:      c.Password,
		AllowedKeys:   c.AllowedKeys,
		Channels:      c.Channels,
		VoiceChannels: c.VoiceChannels,
		TLSCert:       c.TLSCert,
		TLSKey:        c.TLSKey,
		PublicIP:      c.PublicIP,
		UDPPortMin:    c.UDPPortMin,
		UDPPortMax:    c.UDPPortMax,
		DefaultRoles:  c.Roles.DefaultRoles,
		Roles:         make(map[string][]string, len(c.Roles.Roles)),
		Grants:        c.Roles.Grants,
		MsgRate:       c.MsgRate,
		MsgBurst:      c.MsgBurst,
		Discoverable:  &c.Discoverable,
		ServerName:    c.ServerName,
	}
	for name, perms := range c.Roles.Roles {
		ps := make([]string, len(perms))
		for i, p := range perms {
			ps[i] = string(p)
		}
		fc.Roles[name] = ps
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(fc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
