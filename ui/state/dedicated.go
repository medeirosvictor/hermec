package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/version"
)

// DedicatedDetails is the deliberately small, portable description of a
// dedicated server. It is not a credential: passwords, password proof keys,
// host lists, chain state, labels, and diagnostics must never be added here.
type DedicatedDetails struct {
	WSSURL   string
	ServerID string
}

const (
	// DedicatedDetailsFormatVersion is bumped only for an incompatible wire
	// format change. Imports intentionally require the exact application build.
	DedicatedDetailsFormatVersion = 1
	// MaxDedicatedDetailsBytes bounds clipboard and file input before parsing.
	MaxDedicatedDetailsBytes = 4096
)

// CanonicalDedicatedWSS validates a dedicated endpoint and returns its
// canonical spelling. Dedicated endpoints are deliberately WSS-only: LAN and
// friend joining continue to use their separate, more permissive path.
func CanonicalDedicatedWSS(raw string) (string, error) {
	if raw == "" || len(raw) > 2048 {
		return "", errors.New("dedicated server URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("invalid dedicated server URL")
	}
	if u.Scheme != "wss" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" {
		return "", errors.New("dedicated server URL must be a root wss:// URL")
	}
	if u.Path != "/" || u.RawPath != "" || u.Hostname() == "" {
		return "", errors.New("dedicated server URL must have no path")
	}
	// A URL with an invalid port is otherwise accepted by url.Parse.
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid dedicated server URL port")
		}
		if strconv.Itoa(n) != p {
			return "", errors.New("dedicated server URL is not canonical")
		}
	}
	// URL.String is intentionally part of the contract. It prevents imports
	// from silently changing an endpoint (including escaped or mixed-case host
	// spellings) before its pin is checked.
	host := u.Hostname()
	if u.String() != raw || strings.ToLower(host) != host || strings.HasSuffix(host, ".") || (strings.Contains(host, ":") && (net.ParseIP(host) == nil || net.ParseIP(host).String() != host)) {
		return "", errors.New("dedicated server URL is not canonical")
	}
	// Hosts containing whitespace/control characters are not useful names and
	// can make status/log output misleading. net.ParseIP permits bracketed v6
	// only after URL removes brackets, so ordinary DNS is checked separately.
	if strings.IndexFunc(u.Hostname(), func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0 {
		return "", errors.New("invalid dedicated server URL")
	}
	if strings.Contains(u.Hostname(), ":") && net.ParseIP(u.Hostname()) == nil {
		return "", errors.New("invalid dedicated server URL")
	}
	return raw, nil
}

// CanonicalDedicatedServerID verifies the chain ID's canonical lowercase
// representation. Keeping this separate makes it hard for UI code to forget
// that a supplied pin is mandatory.
func CanonicalDedicatedServerID(raw string) (string, error) {
	id, err := chain.ParseID(raw)
	if err != nil || id.String() != raw {
		return "", errors.New("server ID must be a canonical server ID")
	}
	return raw, nil
}

// ValidateDedicatedDetails validates the two values used by a dedicated dial.
func ValidateDedicatedDetails(d DedicatedDetails) (DedicatedDetails, error) {
	u, err := CanonicalDedicatedWSS(d.WSSURL)
	if err != nil {
		return DedicatedDetails{}, err
	}
	id, err := CanonicalDedicatedServerID(d.ServerID)
	if err != nil {
		return DedicatedDetails{}, err
	}
	return DedicatedDetails{WSSURL: u, ServerID: id}, nil
}

type dedicatedWire struct {
	FormatVersion int    `json:"format_version"`
	BuildVersion  string `json:"build_version"`
	WSSURL        string `json:"wss_url"`
	ServerID      string `json:"server_id"`
}

// MarshalDedicatedDetails emits the only supported connection-details format.
// Its private wire struct is an allowlist, so future ServerEntry fields cannot
// accidentally become exported credentials.
func MarshalDedicatedDetails(d DedicatedDetails) ([]byte, error) {
	d, err := ValidateDedicatedDetails(d)
	if err != nil {
		return nil, err
	}
	return json.Marshal(dedicatedWire{DedicatedDetailsFormatVersion, version.Version, d.WSSURL, d.ServerID})
}

// UnmarshalDedicatedDetails accepts exactly the four documented JSON fields.
// Decoder token handling rejects duplicate fields, unknown fields, non-string
// values, trailing JSON, oversized input, and credentials smuggled as extras.
func UnmarshalDedicatedDetails(data []byte) (DedicatedDetails, error) {
	if len(data) == 0 || len(data) > MaxDedicatedDetailsBytes {
		return DedicatedDetails{}, errors.New("connection details are too large or empty")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return DedicatedDetails{}, errors.New("invalid connection details")
	}
	var w dedicatedWire
	seen := make(map[string]bool, 4)
	for dec.More() {
		t, err := dec.Token()
		name, ok := t.(string)
		if err != nil || !ok || seen[name] {
			return DedicatedDetails{}, errors.New("invalid connection details")
		}
		seen[name] = true
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return DedicatedDetails{}, errors.New("invalid connection details")
		}
		switch name {
		case "format_version":
			if err := json.Unmarshal(raw, &w.FormatVersion); err != nil {
				return DedicatedDetails{}, errors.New("invalid connection details")
			}
		case "build_version":
			if err := json.Unmarshal(raw, &w.BuildVersion); err != nil {
				return DedicatedDetails{}, errors.New("invalid connection details")
			}
		case "wss_url":
			if err := json.Unmarshal(raw, &w.WSSURL); err != nil {
				return DedicatedDetails{}, errors.New("invalid connection details")
			}
		case "server_id":
			if err := json.Unmarshal(raw, &w.ServerID); err != nil {
				return DedicatedDetails{}, errors.New("invalid connection details")
			}
		default:
			return DedicatedDetails{}, fmt.Errorf("connection details contain an unsupported field")
		}
	}
	if _, err := dec.Token(); err != nil || len(seen) != 4 {
		return DedicatedDetails{}, errors.New("connection details must contain exactly four fields")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return DedicatedDetails{}, errors.New("invalid connection details")
	}
	if w.FormatVersion != DedicatedDetailsFormatVersion {
		return DedicatedDetails{}, errors.New("unsupported connection details format")
	}
	if w.BuildVersion != version.Version {
		return DedicatedDetails{}, errors.New("connection details require the same HERMEC build")
	}
	return ValidateDedicatedDetails(DedicatedDetails{WSSURL: w.WSSURL, ServerID: w.ServerID})
}
