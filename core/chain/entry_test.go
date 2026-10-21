package chain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
)

var t0 = time.UnixMilli(1760000000000)

func newID(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func genesis(t *testing.T, id *identity.Identity) Entry {
	t.Helper()
	e, err := NewEntry(id, nil, OpGenesis, GenesisBody{Name: "<srv> & co", Founder: id.PublicKey(), Salt: []byte{1, 2, 3}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEntrySignVerify(t *testing.T) {
	e := genesis(t, newID(t))
	if !e.VerifySig() {
		t.Fatal("VerifySig false")
	}
	if e.Seq != 0 || e.Prev != nil {
		t.Fatalf("seq=%d prev=%v", e.Seq, e.Prev)
	}
}

func TestEntryTamperFails(t *testing.T) {
	id := newID(t)
	tampers := map[string]func(*Entry){
		"body":   func(e *Entry) { e.Body = []byte(`{"name":"x","founder":"","salt":""}`) },
		"time":   func(e *Entry) { e.Time++ },
		"seq":    func(e *Entry) { e.Seq++ },
		"op":     func(e *Entry) { e.Op = OpConfig },
		"author": func(e *Entry) { e.Author = append([]byte(nil), e.Author...); e.Author[0] ^= 1 },
		"prev":   func(e *Entry) { e.Prev = bytes.Repeat([]byte{7}, 32) },
		"sig":    func(e *Entry) { e.Sig = append([]byte(nil), e.Sig...); e.Sig[0] ^= 1 },
	}
	for name, f := range tampers {
		e := genesis(t, id)
		f(&e)
		if e.VerifySig() {
			t.Errorf("%s tamper still verifies", name)
		}
	}
}

func TestEntryNonCompactBodyFails(t *testing.T) {
	id := newID(t)
	e := genesis(t, id)
	var buf bytes.Buffer
	if err := json.Indent(&buf, e.Body, "", "  "); err != nil {
		t.Fatal(err)
	}
	e.Body = buf.Bytes()
	// Re-sign over the indented body so only compactness is at fault.
	e.Sig = id.Sign(e.SignedBytes())
	if e.VerifySig() {
		t.Fatal("non-compact body verified")
	}
}

func TestEntryStdJSONRoundTrip(t *testing.T) {
	id := newID(t)
	e, err := NewEntry(id, nil, OpGenesis, GenesisBody{Name: "<R&D>", Founder: id.PublicKey()}, t0)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var got Entry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !got.VerifySig() || got.Hash() != e.Hash() {
		t.Fatal("std json round trip changed entry")
	}
	var gb GenesisBody
	if err := got.DecodeBody(&gb); err != nil || gb.Name != "<R&D>" {
		t.Fatalf("DecodeBody: %v %q", err, gb.Name)
	}
}

func TestEntryJSONRoundTrip(t *testing.T) {
	e := genesis(t, newID(t))
	// Wire encoding is no-escape, like proto; plain json.Marshal would
	// HTML-escape the body and change its bytes.
	data, err := marshalNoEscape(e)
	if err != nil {
		t.Fatal(err)
	}
	var got Entry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Hash() != e.Hash() || !got.VerifySig() {
		t.Fatal("round trip changed entry")
	}
}

func TestSignedBytesPrefixAndNoEscape(t *testing.T) {
	sb := genesis(t, newID(t)).SignedBytes()
	if !bytes.HasPrefix(sb, []byte("hermec-chain-v1\x00")) {
		t.Fatal("missing prefix")
	}
	if bytes.Contains(sb, []byte("\\u003c")) || bytes.HasSuffix(sb, []byte("\n")) {
		t.Fatal("html escaped or trailing newline")
	}
}

func TestIDStringRoundTrip(t *testing.T) {
	id := IDOf(genesis(t, newID(t)))
	got, err := ParseID(id.String())
	if err != nil || got != id {
		t.Fatalf("parse: %v", err)
	}
	if len(id.String()) != 52 {
		t.Fatalf("len %d", len(id.String()))
	}
	if !strings.HasPrefix(id.Short(), "srv-") {
		t.Fatal(id.Short())
	}
	if _, err := ParseID("abc"); err == nil {
		t.Fatal("bad id parsed")
	}
	s := id.String()
	if _, err := ParseID(strings.ToUpper(s)); err == nil {
		t.Fatal("uppercase parsed")
	}
	// The last char carries 1 data bit and 4 padding bits; change only padding.
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	last := strings.IndexByte(alphabet, s[51])
	alt := s[:51] + string(alphabet[last^1])
	if _, err := ParseID(alt); err == nil {
		t.Fatal("non-canonical alias parsed")
	}
}

func TestNextEntryLinks(t *testing.T) {
	id := newID(t)
	first := genesis(t, id)
	second, err := NewEntry(id, &first, OpRevoke, FPBody{Fingerprint: "abcd"}, t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	h := first.Hash()
	if second.Seq != 1 || !bytes.Equal(second.Prev, h[:]) {
		t.Fatal("bad link")
	}
}
