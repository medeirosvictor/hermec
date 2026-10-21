package client

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// Bounds on what a host may send before it has proven itself. The chain
// itself is capped at 2048 entries and 512 KiB; the margins allow for JSON
// overhead but stop a hostile host from streaming without end.
const (
	maxChainFrames = 64
	maxChainBytes  = 2 << 20
)

func badChain(format string, args ...any) *ServerError {
	return &ServerError{Code: proto.CodeBadChain, Message: fmt.Sprintf(format, args...)}
}

// decodeEntries parses the raw entries of a chain transfer.
func decodeEntries(raws []json.RawMessage) ([]chain.Entry, error) {
	out := make([]chain.Entry, 0, len(raws))
	for i, r := range raws {
		var e chain.Entry
		if err := json.Unmarshal(r, &e); err != nil {
			return nil, badChain("entry %d is malformed", i)
		}
		out = append(out, e)
	}
	return out, nil
}

// verifyHost checks everything the host presented before the client sends
// its password: the chain verifies and matches pin (if any), the proving key
// is on the chain's host list, and the proof signature is valid for these
// nonces. It returns the verified chain.
func verifyHost(entries []chain.Entry, pin string, hp proto.HostProof, clientNonce, serverNonce []byte) (*chain.Chain, error) {
	if len(entries) == 0 {
		return nil, badChain("the host sent an empty chain")
	}
	// The genesis hash is the server ID: compare it to the pin first, so a
	// different server is reported as such rather than as a broken chain.
	if pin != "" && chain.IDOf(entries[0]).String() != pin {
		return nil, &ServerError{Code: proto.CodeServerIDMismatch, Message: "this is not the server you saved"}
	}
	ch, err := chain.FromEntries(entries)
	if err != nil {
		if errors.Is(err, chain.ErrFull) || errors.Is(err, chain.ErrBadChain) {
			return nil, badChain("%v", err)
		}
		return nil, badChain("chain rejected")
	}
	id := ch.ID()
	if len(hp.PubKey) != ed25519.PublicKeySize {
		return nil, &ServerError{Code: proto.CodeHostNotAuthorized, Message: "the host sent no valid key"}
	}
	pub := ed25519.PublicKey(hp.PubKey)
	st := ch.State()
	if !st.IsHost(pub) {
		return nil, &ServerError{Code: proto.CodeHostNotAuthorized, Message: "the host is not an approved host of this server"}
	}
	if !identity.Verify(pub, proto.HostProofBytes(clientNonce, serverNonce, id[:]), hp.Sig) {
		return nil, &ServerError{Code: proto.CodeHostNotAuthorized, Message: "the host could not prove its key"}
	}
	return ch, nil
}
