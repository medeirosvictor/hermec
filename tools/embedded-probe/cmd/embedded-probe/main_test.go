package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareStateDirRejectsForeignNonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("not probe state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareStateDir(dir); err == nil {
		t.Fatal("foreign non-empty directory accepted")
	}
}

func TestPrepareStateDirReopensMarkedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "node")
	got, err := prepareStateDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(got, markerName)); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareStateDir(dir); err != nil {
		t.Fatalf("reopen: %v", err)
	}
}
