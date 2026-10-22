package ui

import (
	"os"
	"strings"
	"testing"
)

// checkMigrated asserts hosted.toml holds password_key and no password, and
// (login) that the hosted session still logs in.
func checkMigrated(t *testing.T, g *game, path string, login bool) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "password_key") || strings.Contains(text, "password =") {
		t.Fatalf("hosted.toml:\n%s", text)
	}
	got, ok, err := loadHosted(path)
	if !ok || err != nil || got.Password != "" || len(got.PasswordKey) != 32 {
		t.Fatalf("loaded ok=%v err=%v pw=%q key=%x", ok, err, got.Password, got.PasswordKey)
	}
	if !login {
		return
	}
	s := g.sess[hostedKey]
	pumpUntil(t, g, func() bool { return connected(s)() || s.failed })
	if s.failed || s.c == nil {
		t.Fatalf("hosted dial after migration failed: %q", s.lastErr)
	}
}

func TestHostedTOMLMigratesPassword(t *testing.T) {
	// A pre-chain hosted.toml (no server_id) with a plain password.
	g, path := hostedTestGame(t, "127.0.0.1:0", false)
	cfg := hostedConfig("Den", "pw", 0, g.fp)
	cfg.Addr, cfg.Discoverable = "127.0.0.1:0", false
	if err := saveHosted(path, cfg); err != nil {
		t.Fatal(err)
	}
	g.bootHosted(path)
	checkMigrated(t, g, path, true)

	// A hosted.toml that already has a server_id but still the plain password.
	g.stopHosted()
	old, _, err := loadHosted(path)
	if err != nil {
		t.Fatal(err)
	}
	old.Password, old.PasswordKey = "pw", nil
	if err := saveHosted(path, old); err != nil {
		t.Fatal(err)
	}
	g2, _ := hostedTestGame(t, "127.0.0.1:0", false)
	g2.bootHosted(path)
	checkMigrated(t, g2, path, false) // g2 has another identity, so no login

	// Once migrated, booting again leaves the file alone.
	before, _ := os.ReadFile(path)
	g2.stopHosted()
	g3, _ := hostedTestGame(t, "127.0.0.1:0", false)
	g3.bootHosted(path)
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("hosted.toml rewritten on a boot with nothing to migrate:\n%s\n---\n%s", before, after)
	}
}
