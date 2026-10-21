package chain

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func line(t *testing.T, e Entry) []byte {
	t.Helper()
	j, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestAppendConflict(t *testing.T) {
	f := newID(t)
	b := newBuilder(t, f)
	e1 := b.entry(f, OpConfig, ConfigBody{Name: "a"})
	e2 := b.entry(f, OpConfig, ConfigBody{Name: "b"})
	if err := b.c.Append(e1); err != nil {
		t.Fatal(err)
	}
	if err := b.c.Append(e2); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestAppendFull(t *testing.T) {
	old := maxEntries
	maxEntries = 3
	defer func() { maxEntries = old }()
	f := newID(t)
	b := newBuilder(t, f) // entry 1 of 3
	b.must(f, OpConfig, ConfigBody{Name: "a"})
	b.must(f, OpConfig, ConfigBody{Name: "b"})
	if err := b.add(f, OpConfig, ConfigBody{Name: "c"}); !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
	if len(b.c.Entries()) != 3 {
		t.Fatal("entry appended past cap")
	}
}

func TestAppendFullBytes(t *testing.T) {
	old := maxBytes
	maxBytes = 1500
	defer func() { maxBytes = old }()
	f := newID(t)
	b := newBuilder(t, f)
	err := b.add(f, OpConfig, ConfigBody{Name: strings.Repeat("x", 2000)})
	if !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
}

func TestLoadRoundTrip(t *testing.T) {
	f := newID(t)
	b := newBuilder(t, f)
	path := filepath.Join(t.TempDir(), "sub", "chain.jsonl")
	if err := b.c.SetPath(path); err != nil {
		t.Fatal(err)
	}
	b.must(f, OpConfig, ConfigBody{Name: "<a> & b"})
	b.must(f, OpGrant, GrantBody{Fingerprint: "x", Roles: []string{"admin"}})
	b.must(f, OpBan, FPBody{Fingerprint: "y"})
	c2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c2.ID() != b.c.ID() || c2.Head().Hash() != b.c.Head().Hash() {
		t.Fatal("round trip mismatch")
	}
	if got := c2.Since(1); len(got) != 2 || got[0].Seq != 2 {
		t.Fatalf("Since: %d", len(got))
	}
	// Appending to a loaded chain persists.
	b2 := &builder{t: t, c: c2, n: 10}
	b2.must(f, OpUnban, FPBody{Fingerprint: "y"})
	c3, err := Load(path)
	if err != nil || c3.Head().Seq != 4 {
		t.Fatalf("reload: %v", err)
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	f, o := newID(t), newID(t)
	b := newBuilder(t, f)
	path := filepath.Join(t.TempDir(), "chain.jsonl")
	if err := b.c.SetPath(path); err != nil {
		t.Fatal(err)
	}
	b.must(f, OpConfig, ConfigBody{Name: "one"})
	b.must(f, OpConfig, ConfigBody{Name: "two"})
	b.must(f, OpConfig, ConfigBody{Name: "three"})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	if len(lines) != 4 {
		t.Fatalf("%d lines", len(lines))
	}
	es := b.c.Entries()
	// forged: last entry re-signed by a non-founder, links intact
	forged, _ := NewEntry(o, &es[2], OpConfig, ConfigBody{Name: "evil"}, t0)
	// edited body, signature left as is
	edited := es[1]
	edited.Body = []byte(`{"name":"hacked"}`)

	join := func(ls ...[]byte) []byte { return append(bytes.Join(ls, []byte("\n")), '\n') }
	cases := map[string][]byte{
		"edited body":   join(lines[0], line(t, edited), lines[2], lines[3]),
		"swapped lines": join(lines[0], lines[2], lines[1], lines[3]),
		"dropped line":  join(lines[0], lines[1], lines[3]),
		"non-founder":   join(lines[0], lines[1], lines[2], line(t, forged)),
		"garbage":       join(lines[0], []byte("{not json")),
		"empty":         {},
	}
	for name, data := range cases {
		p := filepath.Join(t.TempDir(), "c.jsonl")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); !errors.Is(err, ErrBadChain) {
			t.Errorf("%s: want ErrBadChain, got %v", name, err)
		}
	}
}

func TestAppendPersistFailureTruncates(t *testing.T) {
	f := newID(t)
	b := newBuilder(t, f)
	path := filepath.Join(t.TempDir(), "chain.jsonl")
	if err := b.c.SetPath(path); err != nil {
		t.Fatal(err)
	}
	b.must(f, OpConfig, ConfigBody{Name: "ok"})
	before, _ := os.ReadFile(path)
	head := b.c.Head().Hash()

	old := writeFile
	writeFile = func(fl *os.File, p []byte) (int, error) {
		n, _ := fl.Write(p[:len(p)/2]) // torn write
		return n, errors.New("disk full")
	}
	err := b.add(f, OpConfig, ConfigBody{Name: "fails"})
	writeFile = old
	if err == nil {
		t.Fatal("want persist error")
	}
	if b.c.Head().Hash() != head {
		t.Fatal("head changed")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("torn line left on disk")
	}
	c2, err := Load(path)
	if err != nil || c2.Head().Hash() != head {
		t.Fatalf("load after failure: %v", err)
	}
	// A later append succeeds and the file still loads.
	b.must(f, OpConfig, ConfigBody{Name: "again"})
	if c3, err := Load(path); err != nil || c3.Head().Hash() != b.c.Head().Hash() {
		t.Fatalf("load after retry: %v", err)
	}
}

func TestLoadCapsReads(t *testing.T) {
	f := newID(t)
	b := newBuilder(t, f)
	path := filepath.Join(t.TempDir(), "chain.jsonl")
	if err := b.c.SetPath(path); err != nil {
		t.Fatal(err)
	}
	b.must(f, OpConfig, ConfigBody{Name: "a"})
	b.must(f, OpConfig, ConfigBody{Name: "b"})
	old := maxEntries
	maxEntries = 2
	defer func() { maxEntries = old }()
	if _, err := Load(path); !errors.Is(err, ErrFull) {
		t.Fatalf("want ErrFull, got %v", err)
	}
}
