package client

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
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
// DedicatedReady queries the active source for the exact Ready boundary. It
// does not activate a host. Only the current chain founder may query or act.
func (c *Client) DedicatedReady(ctx context.Context, fingerprint string) (*proto.DedicatedReadiness, error) {
	r, err := c.dedicatedRequest(ctx, fingerprint, nil)
	if err != nil {
		return nil, err
	}
	if r.Status == "ready" && r.Readiness != nil {
		return r.Readiness, nil
	}
	return nil, fmt.Errorf("client: dedicated destination %s", r.Message)
}

// ActivateDedicated explicitly activates exactly the Ready boundary returned
// by DedicatedReady. It never accepts a fingerprint-only activation.
func (c *Client) ActivateDedicated(ctx context.Context, readiness proto.DedicatedReadiness) (*proto.DedicatedResult, error) {
	if err := readiness.Validate(); err != nil {
		return nil, err
	}
	return c.dedicatedRequest(ctx, readiness.DestinationFingerprint, readiness.TransitionNonce)
}

func (c *Client) dedicatedRequest(ctx context.Context, fingerprint string, nonce []byte) (*proto.DedicatedResult, error) {
	if c == nil || c.chain == nil {
		return nil, ErrClosed
	}
	if c.chain.State().Founder != c.Fingerprint() {
		return nil, errors.New("client: only the founder may activate a dedicated host")
	}
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	ref := hex.EncodeToString(b[:])
	ch := make(chan proto.DedicatedResult, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	if c.activations == nil {
		c.activations = make(map[string]chan proto.DedicatedResult)
	}
	c.activations[ref] = ch
	c.mu.Unlock()
	if err := c.send(proto.TypeDedicatedActivate, proto.DedicatedActivate{DestinationFingerprint: fingerprint, TransitionNonce: append([]byte(nil), nonce...), Ref: ref}); err != nil {
		c.removeDedicatedActivation(ref)
		return nil, err
	}
	select {
	case r := <-ch:
		if r.Status == "error" && r.Message == ErrClosed.Error() {
			return nil, ErrClosed
		}
		return &r, nil
	case <-ctx.Done():
		c.removeDedicatedActivation(ref)
		return nil, ctx.Err()
	}
}

func (c *Client) removeDedicatedActivation(ref string) {
	c.mu.Lock()
	delete(c.activations, ref)
	c.mu.Unlock()
}

func (c *Client) resolveDedicatedActivation(r proto.DedicatedResult) {
	c.mu.Lock()
	ch := c.activations[r.Ref]
	delete(c.activations, r.Ref)
	c.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

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
