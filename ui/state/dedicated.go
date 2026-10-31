package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/version"
)

// DedicatedDetails is the deliberately small, portable description of a
// dedicated server. It is not a credential: passwords, password proof keys,
// host lists, chain state, labels, and diagnostics must never be added here.
type DedicatedDetails struct {
	WSSURL   string
	ServerID string
}

// MaxDedicatedDetailsBytes bounds clipboard and file input before parsing.
const MaxDedicatedDetailsBytes = 4096

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
	u, err := proto.CanonicalDedicatedWSS(d.WSSURL)
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
	BuildVersion string `json:"build_version"`
	WSSURL       string `json:"wss_url"`
	ServerID     string `json:"server_id"`
}

// MarshalDedicatedDetails emits the only supported connection-details format.
// Its private wire struct is an allowlist, so future ServerEntry fields cannot
// accidentally become exported credentials.
func MarshalDedicatedDetails(d DedicatedDetails) ([]byte, error) {
	d, err := ValidateDedicatedDetails(d)
	if err != nil {
		return nil, err
	}
	return json.Marshal(dedicatedWire{version.Version, d.WSSURL, d.ServerID})
}

// UnmarshalDedicatedDetails accepts exactly the three documented JSON fields.
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
	seen := make(map[string]bool, 3)
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
	if _, err := dec.Token(); err != nil || len(seen) != 3 {
		return DedicatedDetails{}, errors.New("connection details must contain exactly three fields")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return DedicatedDetails{}, errors.New("invalid connection details")
	}
	if w.BuildVersion != version.Version {
		return DedicatedDetails{}, errors.New("connection details require the same HERMEC build")
	}
	return ValidateDedicatedDetails(DedicatedDetails{WSSURL: w.WSSURL, ServerID: w.ServerID})
}
