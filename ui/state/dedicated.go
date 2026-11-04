package state

import (
	"errors"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/proto"
)

// DedicatedDetails is the two values a dedicated dial needs. It is not a
// credential. Sharing them goes through Invite (invite.go), the only
// supported connection format.
type DedicatedDetails struct {
	WSSURL   string
	ServerID string
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
