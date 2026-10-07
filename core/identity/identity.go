// Package identity provides the Ed25519 identity used by Hermec components.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

const pemType = "HERMEC ED25519 SEED"

// Identity is an Ed25519 keypair.
type Identity struct {
	priv ed25519.PrivateKey
}

// Generate creates a new random identity.
func Generate() (*Identity, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return &Identity{priv: priv}, nil
}

// Load reads an identity from a key file written by Save.
func Load(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != pemType {
		return nil, errors.New("identity: invalid key file")
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(block.Bytes)))
	if err != nil {
		return nil, fmt.Errorf("identity: decode seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("identity: bad seed length")
	}
	return &Identity{priv: ed25519.NewKeyFromSeed(seed)}, nil
}

// Save writes the identity to path with mode 0600. It fails if the file exists.
func (id *Identity) Save(path string) error {
	body := base64.StdEncoding.EncodeToString(id.priv.Seed())
	data := pem.EncodeToMemory(&pem.Block{Type: pemType, Bytes: []byte(body)})
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

// PublicKey returns the identity's public key.
func (id *Identity) PublicKey() ed25519.PublicKey {
	return id.priv.Public().(ed25519.PublicKey)
}

// Sign signs msg.
func (id *Identity) Sign(msg []byte) []byte {
	return ed25519.Sign(id.priv, msg)
}

// Verify reports whether sig is a valid signature of msg by pub.
func Verify(pub ed25519.PublicKey, msg, sig []byte) bool {
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, msg, sig)
}

// Fingerprint returns a short human-readable fingerprint of pub, e.g.
// "k7mv-q3xp-9dfw-02hj"-style: lowercase base32 of sha256(pub)[:10] in
// hyphenated groups of four.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	s := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:10]))
	parts := make([]string, 0, len(s)/4)
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, s[i:i+4])
	}
	return strings.Join(parts, "-")
}
