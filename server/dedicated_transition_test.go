package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

func testDedicatedTransition() DedicatedTransitionRecord {
	return DedicatedTransitionRecord{Version: 1, State: DedicatedTransitionPrepared, ServerID: "server", DestinationFingerprint: "destination", ChainHead: 4, HistoryLastID: "history", HistoryLastUnixMilli: 9, TransitionNonce: bytes.Repeat([]byte{1}, proto.DedicatedTransitionNonceSize), Recovery: DedicatedRecoveryRetryPreparation}
}

func TestDedicatedTransitionAtomicPrivateRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transition.json")
	want := testDedicatedTransition()
	if err := SaveDedicatedTransition(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %04o, want 0600", info.Mode().Perm())
	}
	got, err := LoadDedicatedTransition(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerID != want.ServerID || got.State != want.State || !bytes.Equal(got.TransitionNonce, want.TransitionNonce) {
		t.Fatalf("loaded record = %+v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "private_key", "password_key"} {
		if bytes.Contains(bytes.ToLower(data), []byte(forbidden)) {
			t.Fatalf("record leaks %q: %s", forbidden, data)
		}
	}
}

func TestDedicatedTransitionRejectsInsecureRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transition.json")
	if err := SaveDedicatedTransition(path, testDedicatedTransition()); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission assertion")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDedicatedTransition(path); err == nil {
		t.Fatal("world-readable transition accepted")
	}
}

func TestDedicatedTransitionUsesFixedRecoveryGuidance(t *testing.T) {
	r := testDedicatedTransition()
	if r.Recovery.Guidance() != "Keep the current host active; retry preparation." {
		t.Fatalf("guidance = %q", r.Recovery.Guidance())
	}
	r.Recovery = DedicatedRecoveryCode("password: leaked")
	if err := r.validate(); err == nil {
		t.Fatal("arbitrary recovery text accepted")
	}
}

func TestDedicatedTransitionRejectsSymlinkAndOversizedRecord(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := SaveDedicatedTransition(target, testDedicatedTransition()); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "transition.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := LoadDedicatedTransition(link); err == nil {
		t.Fatal("symlink transition accepted")
	}
	big := filepath.Join(dir, "big.json")
	if err := os.WriteFile(big, bytes.Repeat([]byte{'x'}, maxDedicatedTransitionBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDedicatedTransition(big); err == nil {
		t.Fatal("oversized transition accepted")
	}
}

func TestPreparedHostCannotPromote(t *testing.T) {
	h := NewHost(HostConfig{Server: Config{Mode: ModePrepared}})
	if h.Role() != RolePrepared || h.Server().Mode() != ModePrepared {
		t.Fatalf("prepared host = role %q mode %v", h.Role(), h.Server().Mode())
	}
	if h.Server().Promote() {
		t.Fatal("prepared host promoted")
	}
	if h.Server().Mode() != ModePrepared {
		t.Fatalf("mode after promotion = %v", h.Server().Mode())
	}
	h.Server().SetMode(ModeActive)
	if h.Server().Mode() != ModePrepared {
		t.Fatalf("ordinary SetMode activated prepared server: %v", h.Server().Mode())
	}
	h.Server().SetMode(ModeIdle)
	if h.Server().Promote() || h.Server().Mode() == ModeActive {
		t.Fatal("prepared server promoted after an ordinary mode change")
	}
	h.Server().SetMode(ModePrepared)
	if !h.Server().CommitDedicatedActivation() || h.Server().Mode() != ModeActive {
		t.Fatal("committed activation seam did not activate prepared server")
	}
}

func TestPreparedHostDoesNotSelfElectWhenSourceIsUnavailable(t *testing.T) {
	fastHostTimers(t)
	h := NewHost(HostConfig{Prepared: true, Server: Config{Addr: freeAddr(t)}})
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Shutdown(context.Background())
	time.Sleep(700 * time.Millisecond) // exceeds the shortened cold-start window
	if h.Role() != RolePrepared || h.Server().Mode() != ModePrepared {
		t.Fatalf("unlinked prepared host self-elected: role %q mode %v", h.Role(), h.Server().Mode())
	}
}
