package chain

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/medeirosvictor/hermec/core/roles"
)

// Configuration bounds are deliberately owned by chain: config entries are
// signed input, not merely values produced by the local TOML parser.
const (
	MaxServerNameRunes  = 64
	MaxChannelNameRunes = 64
	MaxChannels         = 64
	MaxVoice            = 50
	MaxClients          = 4096
	MaxPerIP            = 1024
	MaxAllowedKeys      = 1024
	MaxRoles            = 128
	MaxRoleNameRunes    = 64
	MaxRolesPerMember   = 64
	MaxHistoryDays      = 3650
	MaxHistoryMB        = 64
)

// Validate reports whether b is a current, safely representable signed server
// configuration. It intentionally has no zero-value exceptions.
func (b ConfigBody) Validate() error {
	if !utf8.ValidString(b.Name) || utf8.RuneCountInString(b.Name) > MaxServerNameRunes {
		return fmt.Errorf("server name must be valid UTF-8 and at most %d characters", MaxServerNameRunes)
	}
	if len(b.Channels) == 0 || len(b.Channels) > MaxChannels || len(b.VoiceChannels) > MaxChannels || len(b.Channels)+len(b.VoiceChannels) > MaxChannels {
		return fmt.Errorf("must have 1 to %d total channels", MaxChannels)
	}
	seen := make(map[string]struct{}, len(b.Channels)+len(b.VoiceChannels))
	for _, channels := range [][]string{b.Channels, b.VoiceChannels} {
		for _, name := range channels {
			if !validConfigName(name, MaxChannelNameRunes) {
				return fmt.Errorf("invalid channel name %q", name)
			}
			if _, ok := seen[name]; ok {
				return fmt.Errorf("duplicate channel %q", name)
			}
			seen[name] = struct{}{}
		}
	}
	if b.MaxVoice < 1 || b.MaxVoice > MaxVoice || b.MaxClients < 1 || b.MaxClients > MaxClients || b.MaxPerIP < 1 || b.MaxPerIP > MaxPerIP {
		return fmt.Errorf("invalid client or voice limits")
	}
	if len(b.AllowedKeys) > MaxAllowedKeys {
		return fmt.Errorf("too many allowed keys")
	}
	allowed := make(map[string]struct{}, len(b.AllowedKeys))
	for _, fp := range b.AllowedKeys {
		if !validFingerprint(fp) {
			return fmt.Errorf("invalid allowed key fingerprint %q", fp)
		}
		if _, ok := allowed[fp]; ok {
			return fmt.Errorf("duplicate allowed key fingerprint %q", fp)
		}
		allowed[fp] = struct{}{}
	}
	if len(b.Roles) > MaxRoles || len(b.DefaultRoles) > MaxRolesPerMember {
		return fmt.Errorf("too many roles")
	}
	for name, perms := range b.Roles {
		if !validConfigName(name, MaxRoleNameRunes) || len(perms) == 0 || len(perms) > len(allPermissions) {
			return fmt.Errorf("invalid role %q", name)
		}
		seenPerm := map[string]struct{}{}
		for _, p := range perms {
			if _, ok := allPermissions[p]; !ok {
				return fmt.Errorf("invalid permission %q", p)
			}
			if _, ok := seenPerm[p]; ok {
				return fmt.Errorf("duplicate permission %q", p)
			}
			seenPerm[p] = struct{}{}
		}
	}
	for _, name := range b.DefaultRoles {
		if !validConfigName(name, MaxRoleNameRunes) {
			return fmt.Errorf("invalid default role %q", name)
		}
		if _, custom := b.Roles[name]; !custom {
			if _, builtin := roles.Builtin()[name]; !builtin {
				return fmt.Errorf("unknown default role %q", name)
			}
		}
	}
	if b.HistoryDays != nil && (*b.HistoryDays < 0 || *b.HistoryDays > MaxHistoryDays) {
		return fmt.Errorf("history days out of range")
	}
	if b.HistoryMB != nil && (*b.HistoryMB < 1 || *b.HistoryMB > MaxHistoryMB) {
		return fmt.Errorf("history MB out of range")
	}
	return nil
}

var allPermissions = map[string]struct{}{
	string(roles.PermJoinChannel): {}, string(roles.PermSendChat): {}, string(roles.PermManage): {},
}

func validConfigName(s string, max int) bool {
	return utf8.ValidString(s) && s != "" && strings.TrimSpace(s) == s && utf8.RuneCountInString(s) <= max
}

func validFingerprint(s string) bool {
	if len(s) != 19 {
		return false
	}
	for i, r := range s {
		if i == 4 || i == 9 || i == 14 {
			if r != '-' {
				return false
			}
			continue
		}
		if !(r >= 'a' && r <= 'z') && !(r >= '2' && r <= '7') {
			return false
		}
	}
	return true
}
