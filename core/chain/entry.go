// Package chain implements the signed append-only chain that gives a Hermec
// server its identity and holds its configuration and membership history.
package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
)

// domain is the prefix signed before every entry, separating chain
// signatures from any other use of the same keys.
const domain = "hermec-chain-v1\x00"

// Op is the kind of change an entry records.
type Op string

// Entry operations.
const (
	OpGenesis    Op = "genesis"
	OpConfig     Op = "config"
	OpGrant      Op = "grant"
	OpRevoke     Op = "revoke"
	OpBan        Op = "ban"
	OpUnban      Op = "unban"
	OpHostAdd    Op = "host_add"
	OpHostRemove Op = "host_remove"
)

// Entry is one signed link of the chain.
type Entry struct {
	Seq    uint64          `json:"seq"`
	Prev   []byte          `json:"prev,omitempty"`
	Time   int64           `json:"time"` // unix milliseconds
	Author []byte          `json:"author"`
	Op     Op              `json:"op"`
	Body   json.RawMessage `json:"body"`
	Sig    []byte          `json:"sig"`
}

// GenesisBody is the body of the first entry.
type GenesisBody struct {
	Name    string `json:"name"`
	Founder []byte `json:"founder"`
	Salt    []byte `json:"salt"`
}

// ConfigBody replaces the server configuration.
type ConfigBody struct {
	Name          string              `json:"name"`
	Channels      []string            `json:"channels"`
	VoiceChannels []string            `json:"voice_channels"`
	MaxVoice      int                 `json:"max_voice"`
	MaxClients    int                 `json:"max_clients"`
	MaxPerIP      int                 `json:"max_per_ip"`
	AllowedKeys   []string            `json:"allowed_keys"`
	Roles         map[string][]string `json:"roles"`
	DefaultRoles  []string            `json:"default_roles"`
}

// GrantBody grants roles to a fingerprint.
type GrantBody struct {
	Fingerprint string   `json:"fingerprint"`
	Roles       []string `json:"roles"`
}

// FPBody names a fingerprint (revoke, ban, unban, host_remove).
type FPBody struct {
	Fingerprint string `json:"fingerprint"`
}

// HostAddBody adds a host key to the failover list.
type HostAddBody struct {
	PubKey   []byte `json:"pub_key"`
	Priority int    `json:"priority"`
}

// marshalNoEscape encodes v as compact JSON without HTML escaping.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// SignedBytes returns the bytes covered by the signature: the domain prefix
// followed by the compact JSON of every field except Sig. It returns nil if
// the entry cannot be encoded (for example an invalid Body).
func (e Entry) SignedBytes() []byte {
	unsigned := struct {
		Seq    uint64          `json:"seq"`
		Prev   []byte          `json:"prev,omitempty"`
		Time   int64           `json:"time"`
		Author []byte          `json:"author"`
		Op     Op              `json:"op"`
		Body   json.RawMessage `json:"body"`
	}{e.Seq, e.Prev, e.Time, e.Author, e.Op, e.Body}
	data, err := marshalNoEscape(unsigned)
	if err != nil {
		return nil
	}
	return append([]byte(domain), data...)
}

// Hash returns sha256(SignedBytes || Sig).
func (e Entry) Hash() [32]byte {
	h := sha256.New()
	h.Write(e.SignedBytes())
	h.Write(e.Sig)
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// NewEntry builds and signs the entry that follows prev (nil for genesis).
func NewEntry(id *identity.Identity, prev *Entry, op Op, body any, now time.Time) (Entry, error) {
	raw, err := marshalNoEscape(body)
	if err != nil {
		return Entry{}, fmt.Errorf("chain: encode body: %w", err)
	}
	e := Entry{
		Time:   now.UnixMilli(),
		Author: []byte(id.PublicKey()),
		Op:     op,
		Body:   raw,
	}
	if prev != nil {
		e.Seq = prev.Seq + 1
		h := prev.Hash()
		e.Prev = h[:]
	}
	e.Sig = id.Sign(e.SignedBytes())
	return e, nil
}

// VerifySig reports whether the signature is valid under Author and Body is
// compact JSON.
func (e Entry) VerifySig() bool {
	var buf bytes.Buffer
	if err := json.Compact(&buf, e.Body); err != nil || !bytes.Equal(buf.Bytes(), e.Body) {
		return false
	}
	sb := e.SignedBytes()
	if sb == nil {
		return false
	}
	return identity.Verify(e.Author, sb, e.Sig)
}
