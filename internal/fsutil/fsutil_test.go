package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileAtomicCreatesAndReplaces(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	for _, want := range []string{"one", "two, longer"} {
		if err := WriteFileAtomic(p, []byte(want), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(p)
		if err != nil || string(got) != want {
			t.Fatalf("got %q, %v; want %q", got, err, want)
		}
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, %v; want 0600", st.Mode(), err)
		}
	}
}

func TestWriteFileAtomicLeavesNoTempOnFailure(t *testing.T) {
	dir := t.TempDir()
	// A directory at the target makes the rename fail after the temp is written.
	target := filepath.Join(dir, "d")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(target, []byte("x"), 0o600); err == nil {
		t.Fatal("want error replacing a non-empty directory")
	}
	if err := WriteFileAtomic(filepath.Join(dir, "missing", "f"), []byte("x"), 0o600); err == nil {
		t.Fatal("want error for a missing parent directory")
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].Name() != "d" {
		t.Fatalf("leftover entries: %v", es)
	}
}
