package chain

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A crash mid-append leaves a torn final line; Load recovers instead of
// refusing the whole chain.
func TestLoadDropsTornFinalLine(t *testing.T) {
	f := newID(t)
	b := newBuilder(t, f)
	path := filepath.Join(t.TempDir(), "chain.jsonl")
	if err := b.c.SetPath(path); err != nil {
		t.Fatal(err)
	}
	b.must(f, OpConfig, ConfigBody{Name: "one"})
	good, _ := os.ReadFile(path)
	head := b.c.Head().Hash()
	b.must(f, OpConfig, ConfigBody{Name: "two"})
	full, _ := os.ReadFile(path)
	lastLine := full[len(good):]

	tails := map[string][]byte{
		"partial line":      lastLine[:len(lastLine)/2],
		"valid, no newline": lastLine[:len(lastLine)-1],
		"nul filled":        bytes.Repeat([]byte{0}, 64),
		"garbage line":      []byte("{not json\n"),
	}
	for name, tail := range tails {
		p := filepath.Join(t.TempDir(), "c.jsonl")
		if err := os.WriteFile(p, append(append([]byte(nil), good...), tail...), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := Load(p)
		if err != nil {
			t.Errorf("%s: Load: %v", name, err)
			continue
		}
		if c.Head().Hash() != head {
			t.Errorf("%s: head is not the last good entry", name)
		}
		if c.TornTail() != len(tail) {
			t.Errorf("%s: TornTail = %d, want %d", name, c.TornTail(), len(tail))
		}
		if got, _ := os.ReadFile(p); !bytes.Equal(got, good) {
			t.Errorf("%s: file not truncated to the last good line", name)
		}
		// The chain keeps working: the next append lands on the clean file.
		h := c.Head()
		next, err := NewEntry(f, &h, OpConfig, ConfigBody{Name: "three"}, t0)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Append(next); err != nil {
			t.Errorf("%s: append after recovery: %v", name, err)
		}
		if c2, err := Load(p); err != nil || c2.TornTail() != 0 {
			t.Errorf("%s: reload: %v", name, err)
		}
	}
}

// Only the final line is forgiven: damage before it is still a bad chain.
func TestLoadMiddleGarbageIsBad(t *testing.T) {
	f := newID(t)
	b := newBuilder(t, f)
	path := filepath.Join(t.TempDir(), "chain.jsonl")
	if err := b.c.SetPath(path); err != nil {
		t.Fatal(err)
	}
	b.must(f, OpConfig, ConfigBody{Name: "one"})
	b.must(f, OpConfig, ConfigBody{Name: "two"})
	raw, _ := os.ReadFile(path)
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	data := bytes.Join([][]byte{lines[0], bytes.Repeat([]byte{0}, 20), lines[2]}, []byte("\n"))
	data = append(data, '\n')
	p := filepath.Join(t.TempDir(), "c.jsonl")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); !errors.Is(err, ErrBadChain) {
		t.Fatalf("want ErrBadChain, got %v", err)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, data) {
		t.Error("a bad chain file was modified")
	}
}
