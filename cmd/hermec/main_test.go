package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestVersionFlag builds hermec and checks that -version prints the version
// line to stdout and exits 0 without starting the GUI.
func TestVersionFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary build in -short mode")
	}

	exe := filepath.Join(t.TempDir(), "hermec.exe")
	build := exec.Command("go", "build", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, "-version")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("hermec -version: %v (stderr: %q)", err, stderr.String())
	}
	if got, want := stdout.String(), "hermec dev\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
