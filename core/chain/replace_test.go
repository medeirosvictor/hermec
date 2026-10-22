package chain

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestReplaceSwapsContentsAndRewritesFile(t *testing.T) {
	f := newID(t)
	b := newBuilder(t, f)
	path := filepath.Join(t.TempDir(), "chain.jsonl")
	if err := b.c.SetPath(path); err != nil {
		t.Fatal(err)
	}
	fork, err := FromEntries(b.c.Entries())
	if err != nil {
		t.Fatal(err)
	}
	b.must(f, OpConfig, ConfigBody{Name: "mine"})
	fb := &builder{t: t, c: fork}
	fb.must(f, OpConfig, ConfigBody{Name: "theirs"})
	fb.must(f, OpConfig, ConfigBody{Name: "theirs2"})

	if err := b.c.Replace(fork.Entries()); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got, want := b.c.Head().Hash(), fork.Head().Hash(); got != want {
		t.Fatal("head differs after Replace")
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Head().Hash() != fork.Head().Hash() {
		t.Fatal("file was not rewritten with the replacement")
	}
	// Later appends still persist.
	b.must(f, OpConfig, ConfigBody{Name: "after"})
	if loaded, _ = Load(path); loaded.Head().Seq != fork.Head().Seq+1 {
		t.Fatal("append after Replace was not persisted")
	}
}

func TestReplaceRefusesOtherChain(t *testing.T) {
	a, b := newBuilder(t, newID(t)), newBuilder(t, newID(t))
	if err := a.c.Replace(b.c.Entries()); !errors.Is(err, ErrBadChain) {
		t.Fatalf("want ErrBadChain, got %v", err)
	}
}
