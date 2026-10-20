package discover

import (
	"context"
	"errors"
	"os"
	"testing"
)

func swapTailscaleSeams(t *testing.T, goosV string, look func(string) (string, error), stat func(string) (os.FileInfo, error)) {
	t.Helper()
	oL, oG, oS := lookPath, goos, statFn
	lookPath, goos, statFn = look, goosV, stat
	noTailscale.Store(false)
	t.Cleanup(func() { lookPath, goos, statFn = oL, oG, oS; noTailscale.Store(false) })
}

type fakeInfo struct {
	os.FileInfo
	mode os.FileMode
}

func (f fakeInfo) Mode() os.FileMode { return f.mode }
func (f fakeInfo) IsDir() bool       { return f.mode.IsDir() }

func TestTailscaleAppFallback(t *testing.T) {
	miss := func(string) (string, error) { return "", errors.New("not found") }
	exe := func(p string) (os.FileInfo, error) {
		if p != macAppTailscale {
			t.Errorf("stat %q", p)
		}
		return fakeInfo{mode: 0o755}, nil
	}
	swapTailscaleSeams(t, "darwin", miss, exe)
	if bin, ok := tailscaleBin(); !ok || bin != macAppTailscale {
		t.Fatalf("got %q %v, want app path", bin, ok)
	}
	if TailscaleMissing() {
		t.Fatal("found via app, must not report missing")
	}
	swapTailscaleSeams(t, "darwin", miss, func(string) (os.FileInfo, error) { return fakeInfo{mode: 0o644}, nil })
	if _, ok := tailscaleBin(); ok {
		t.Fatal("non-executable file accepted")
	}
	swapTailscaleSeams(t, "linux", miss, func(string) (os.FileInfo, error) { return fakeInfo{mode: 0o755}, nil })
	if _, ok := tailscaleBin(); ok {
		t.Fatal("app path used off darwin")
	}
	swapTailscaleSeams(t, "darwin", func(string) (string, error) { return "/usr/bin/tailscale", nil }, exe)
	if bin, _ := tailscaleBin(); bin != "/usr/bin/tailscale" {
		t.Fatalf("got %q", bin)
	}
}

func TestNoTailscaleHint(t *testing.T) {
	swapTailscaleSeams(t, "linux", func(string) (string, error) { return "", errors.New("nope") },
		func(string) (os.FileInfo, error) { return nil, os.ErrNotExist })
	if TailscaleMissing() {
		t.Fatal("missing before any lookup")
	}
	if got := tailscalePeers(context.Background()); got != nil {
		t.Fatalf("got %v", got)
	}
	if !TailscaleMissing() {
		t.Fatal("flag not set after a failed lookup")
	}
}
