package chain

import (
	"encoding/base32"
	"fmt"
	"strings"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// ID identifies a server: the hash of its genesis entry.
type ID [32]byte

// IDOf returns the server ID for a genesis entry.
func IDOf(genesis Entry) ID { return ID(genesis.Hash()) }

// String returns the ID as 52 lowercase base32 characters, used for pins and
// filenames.
func (id ID) String() string {
	return strings.ToLower(b32.EncodeToString(id[:]))
}

// ParseID parses the output of ID.String.
func ParseID(s string) (ID, error) {
	var id ID
	if len(s) != 52 {
		return id, fmt.Errorf("chain: bad server id length %d", len(s))
	}
	raw, err := b32.DecodeString(strings.ToUpper(s))
	if err != nil || len(raw) != len(id) {
		return id, fmt.Errorf("chain: bad server id")
	}
	copy(id[:], raw)
	return id, nil
}

// Short returns a human-readable form, "srv-" plus the first 10 bytes in
// hyphenated groups of four (the identity.Fingerprint style).
func (id ID) Short() string {
	s := strings.ToLower(b32.EncodeToString(id[:10]))
	parts := make([]string, 0, len(s)/4)
	for i := 0; i < len(s); i += 4 {
		parts = append(parts, s[i:i+4])
	}
	return "srv-" + strings.Join(parts, "-")
}
