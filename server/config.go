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

// MaxVoiceLimit is the largest accepted max_voice (see LoadConfig).
const MaxVoiceLimit = 50

// Chat history defaults and ceilings (see Config.HistoryLimits).
const (
	DefaultHistoryDays = 30
	DefaultHistoryMB   = 8
	MaxHistoryDays     = 3650
	MaxHistoryMB       = 1024
)

// HistoryLimits returns the effective retention: days (0 keeps nothing) and
// megabytes, defaulted when unset and clamped to their ceilings.
func (c Config) HistoryLimits() (days, mb int) {
	days, mb = DefaultHistoryDays, DefaultHistoryMB
	if c.HistoryDays != nil {
		days = *c.HistoryDays
	}
	if c.HistoryMB != nil {
		mb = *c.HistoryMB
	}
	return min(max(days, 0), MaxHistoryDays), min(max(mb, 1), MaxHistoryMB)
}

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
	MediaPort     int                 `toml:"media_port"`
	DefaultRoles  []string            `toml:"default_roles"`
	Roles         map[string][]string `toml:"roles"`
	Grants        map[string][]string `toml:"grants"`
	MsgRate       int                 `toml:"msg_rate"`
	MsgBurst      int                 `toml:"msg_burst"`
	MaxVoice      *int                `toml:"max_voice"` // nil (not written) when unset or default
	MaxClients    *int                `toml:"max_clients"`
	MaxPerIP      *int                `toml:"max_per_ip"`
	Discoverable  *bool               `toml:"discoverable"`
	HistoryDays   *int                `toml:"history_days"`
	HistoryMB     *int                `toml:"history_mb"`
	ServerName    string              `toml:"server_name,omitempty"`
	ServerID      string              `toml:"server_id,omitempty"`
}

// DefaultConfig returns the configuration used when no file is given.
func DefaultConfig() Config {
	return Config{
		Addr:         DefaultAddr,
		Channels:     []string{"general"},
		MsgRate:      DefaultMsgRate,
		MsgBurst:     DefaultMsgBurst,
		MaxVoice:     DefaultMaxVoice,
		MaxClients:   DefaultMaxClients,
		MaxPerIP:     DefaultMaxPerIP,
		Discoverable: true,
		Roles: roles.Config{
			Roles:        roles.Builtin(),
			DefaultRoles: []string{"user"},
		},
	}
}

// intOr returns *p, or 0 when p is nil.
func intOr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
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
	maxVoice, maxClients, maxPerIP := intOr(fc.MaxVoice), intOr(fc.MaxClients), intOr(fc.MaxPerIP)
	if fc.Addr != "" {
		cfg.Addr = fc.Addr
	}
	cfg.Password = fc.Password
	cfg.ServerID = fc.ServerID
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
	if fc.MediaPort < 0 || fc.MediaPort > 65535 {
		return Config{}, errors.New("server: load config: media_port must be between 0 and 65535")
	}
	cfg.MediaPort = fc.MediaPort
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
	if maxVoice < 0 || (md.IsDefined("max_voice") && maxVoice == 0) {
		return Config{}, errors.New("server: load config: max_voice must be at least 1")
	}
	if maxClients < 0 || (md.IsDefined("max_clients") && maxClients == 0) {
		return Config{}, errors.New("server: load config: max_clients must be at least 1")
	}
	if maxPerIP < 0 || (md.IsDefined("max_per_ip") && maxPerIP == 0) {
		return Config{}, errors.New("server: load config: max_per_ip must be at least 1")
	}
	if md.IsDefined("max_per_ip") {
		cfg.MaxPerIP = maxPerIP
	}
	if md.IsDefined("max_clients") {
		cfg.MaxClients = maxClients
	}
	// Ceiling: the server's rtc_offer carries one audio line per other member
	// (~0.8 KB live, ~0.55 KB retired, plus video) and must fit
	// proto.MaxMessageSize; beyond ~60 members the first offer after a media
	// rebuild is itself oversize and the room would reset forever.
	if maxVoice > MaxVoiceLimit {
		return Config{}, fmt.Errorf("server: load config: max_voice must be at most %d", MaxVoiceLimit)
	}
	if md.IsDefined("max_voice") {
		cfg.MaxVoice = maxVoice
	}
	if fc.HistoryDays != nil {
		if *fc.HistoryDays < 0 || *fc.HistoryDays > MaxHistoryDays {
			return Config{}, fmt.Errorf("server: load config: history_days must be between 0 and %d", MaxHistoryDays)
		}
		cfg.HistoryDays = fc.HistoryDays
	}
	if fc.HistoryMB != nil {
		if *fc.HistoryMB < 1 || *fc.HistoryMB > MaxHistoryMB {
			return Config{}, fmt.Errorf("server: load config: history_mb must be between 1 and %d", MaxHistoryMB)
		}
		cfg.HistoryMB = fc.HistoryMB
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

// MarshalTOML renders c in the same TOML shape LoadConfig reads. The limit keys
// (max_voice, max_clients, max_per_ip) are written only when they differ from
// their defaults, so a file that keeps the defaults has no keys an older app
// (which rejects unknown keys) would not know.
func (c Config) MarshalTOML() ([]byte, error) {
	omitLimit := func(v, def int) *int {
		if v <= 0 || v == def {
			return nil
		}
		return &v
	}
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
		MediaPort:     c.MediaPort,
		DefaultRoles:  c.Roles.DefaultRoles,
		Roles:         make(map[string][]string, len(c.Roles.Roles)),
		Grants:        c.Roles.Grants,
		MsgRate:       c.MsgRate,
		MsgBurst:      c.MsgBurst,
		MaxVoice:      omitLimit(c.MaxVoice, DefaultConfig().MaxVoice),
		MaxClients:    omitLimit(c.MaxClients, DefaultMaxClients),
		MaxPerIP:      omitLimit(c.MaxPerIP, DefaultMaxPerIP),
		Discoverable:  &c.Discoverable,
		HistoryDays:   c.HistoryDays,
		HistoryMB:     c.HistoryMB,
		ServerName:    c.ServerName,
		ServerID:      c.ServerID,
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

// MarshalLocalTOML renders only the machine-local keys (addr, password, TLS,
// public_ip, udp range, media_port, discoverable, msg rate, server_id). The
// group fields live in the chain once one exists.
func (c Config) MarshalLocalTOML() ([]byte, error) {
	fc := fileConfig{
		Addr:         c.Addr,
		Password:     c.Password,
		TLSCert:      c.TLSCert,
		TLSKey:       c.TLSKey,
		PublicIP:     c.PublicIP,
		UDPPortMin:   c.UDPPortMin,
		UDPPortMax:   c.UDPPortMax,
		MediaPort:    c.MediaPort,
		MsgRate:      c.MsgRate,
		MsgBurst:     c.MsgBurst,
		Discoverable: &c.Discoverable,
		ServerID:     c.ServerID,
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(fc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
