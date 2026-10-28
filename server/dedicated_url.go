package server

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// CanonicalDedicatedWSS accepts only the one safe spelling of a pinned
// dedicated endpoint. In particular credentials and URL components which can
// carry secrets are never retained in a transition record.
func CanonicalDedicatedWSS(raw string) (string, error) {
	if raw == "" || len(raw) > 2048 || strings.TrimSpace(raw) != raw || strings.IndexFunc(raw, unicode.IsSpace) >= 0 {
		return "", errors.New("dedicated server URL must be a canonical root wss:// URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "wss" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" || u.Path != "/" || u.RawPath != "" || u.Hostname() == "" {
		return "", errors.New("dedicated server URL must be a canonical root wss:// URL")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid dedicated server URL port")
		}
	}
	if u.String() != raw || strings.ToLower(u.Hostname()) != u.Hostname() || (strings.Contains(u.Hostname(), ":") && net.ParseIP(u.Hostname()) == nil) {
		return "", errors.New("dedicated server URL is not canonical")
	}
	return raw, nil
}
