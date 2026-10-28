package client

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
)

// ParseDedicatedHostPublicKey decodes the VPS public key printed by
// hermec-server. It accepts raw base64 (standard or URL encoding), never a
// private-key file or seed.
func ParseDedicatedHostPublicKey(text string) ([]byte, string, error) {
	text = strings.TrimSpace(text)
	for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding, base64.RawURLEncoding, base64.URLEncoding} {
		pub, err := enc.DecodeString(text)
		if err == nil && len(pub) == 32 {
			return pub, identity.Fingerprint(pub), nil
		}
	}
	return nil, "", errors.New("client: dedicated host public key must be an Ed25519 public key encoded as base64")
}

// AuthorizeDedicatedHost appends the VPS's public key as a host. It is
// deliberately founder-only even though the chain has its own authorization
// checks: this flow must not turn an imported public key into a general member
// administration shortcut.
func (c *Client) AuthorizeDedicatedHost(ctx context.Context, publicKey []byte, priority int) error {
	if c == nil || c.chain == nil {
		return ErrClosed
	}
	if len(publicKey) != 32 {
		return errors.New("client: invalid dedicated host public key")
	}
	if c.chain.State().Founder != c.Fingerprint() {
		return fmt.Errorf("client: only the founder may authorize a dedicated host")
	}
	if priority < 1 {
		return errors.New("client: dedicated host priority must be positive")
	}
	return c.ChainAppend(ctx, chain.OpHostAdd, chain.HostAddBody{PubKey: append([]byte(nil), publicKey...), Priority: priority})
}
