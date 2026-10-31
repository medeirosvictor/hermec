package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui/state"
)

func TestServerIDMenuRowCopies(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	g.servers = append(g.servers, state.ServerEntry{URL: "k", Label: "k"})
	var copied string
	old := clipboardWrite
	clipboardWrite = func(s string) error { copied = s; return nil }
	t.Cleanup(func() { clipboardWrite = old })

	g.openTileMenu("k", 10, 70)
	for _, l := range menuLabels(g) {
		if strings.HasPrefix(l, "id ") {
			t.Fatalf("tile with unknown id has row %q", l)
		}
	}
	g.openTileMenu(hostedKey, 10, 70)
	want := "id " + shortID(g.hosted.id)
	if !slices.Contains(menuLabels(g), want) || !strings.HasPrefix(want, "id srv-") {
		t.Fatalf("labels %v lack %q", menuLabels(g), want)
	}
	pickMenu(g, hostedKey, want)
	if copied != g.hosted.id {
		t.Fatalf("copied %q, want %q", copied, g.hosted.id)
	}
}

func chainFileFor(path, id string) string {
	return filepath.Join(filepath.Dir(path), "chains", id+".jsonl")
}

// A hosted.toml without server_id is refused, not turned into a new chain.
func TestBootHostedRefusesMissingServerID(t *testing.T) {
	g, path := hostedTestGame(t, "", false)
	cfg := hostedConfig("Den", 0, g.fp)
	cfg.Addr, cfg.Discoverable = "127.0.0.1:0", false
	if err := saveHostedLocal(path, cfg); err != nil {
		t.Fatal(err)
	}
	g.bootHosted(path)
	if g.hosted != nil {
		t.Fatalf("hosted = %+v, want none", g.hosted)
	}
	if !strings.Contains(g.notice, "server_id") {
		t.Errorf("notice = %q", g.notice)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "chains")); !os.IsNotExist(err) {
		t.Errorf("chains dir created: %v", err)
	}
}

func TestBootHostedLoadsFoundedChain(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	if g.hosted == nil || g.hosted.srv == nil || g.hosted.chain == nil {
		t.Fatalf("hosted = %+v, want running with a chain", g.hosted)
	}
	st := g.hosted.chain.State()
	if st.Founder != g.fp {
		t.Errorf("founder = %q, want %q", st.Founder, g.fp)
	}
	if st.Config.Name != "Den" || g.hosted.name() != "Den" {
		t.Errorf("chain name = %q, name() = %q", st.Config.Name, g.hosted.name())
	}
}

func TestHostedRestartKeepsServerID(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	id := g.hosted.chain.ID().String()
	cf := chainFileFor(path, id)
	before, err := os.ReadFile(cf)
	if err != nil {
		t.Fatal(err)
	}
	g.stopHosted()

	g2 := newTestGame(t)
	g2.id, g2.fp = g.id, g.fp
	withLayout(t, g2)
	t.Cleanup(g2.stopHosted)
	g2.bootHosted(path)
	if g2.hosted == nil || g2.hosted.chain == nil {
		t.Fatalf("second boot hosted = %+v", g2.hosted)
	}
	if got := g2.hosted.chain.ID().String(); got != id {
		t.Errorf("id changed: %s -> %s", id, got)
	}
	after, _ := os.ReadFile(cf)
	if string(before) != string(after) {
		t.Error("chain file was rewritten on restart")
	}
	ents, _ := os.ReadDir(filepath.Dir(cf))
	if len(ents) != 1 {
		t.Errorf("chains dir has %d entries, want 1", len(ents))
	}
}

// The app writes only local keys, so a hosted.toml with a group key is refused.
func TestBootHostedRejectsGroupKeys(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	if g.notice != "" {
		t.Fatalf("first boot notice = %q", g.notice)
	}
	g.stopHosted()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nhistory_days = 0\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	g2 := newTestGame(t)
	g2.id, g2.fp = g.id, g.fp
	withLayout(t, g2)
	t.Cleanup(g2.stopHosted)
	g2.bootHosted(path)
	if g2.hosted != nil {
		t.Fatalf("second boot hosted = %+v, want none", g2.hosted)
	}
	if !strings.Contains(g2.notice, "history_days") || !strings.Contains(g2.notice, "-apply-config") {
		t.Errorf("notice = %q", g2.notice)
	}
}

func TestCreateHostedWritesChainAndLocalConfig(t *testing.T) {
	g, path := hostedTestGame(t, "", false)
	g.hostedTweak = func(c *server.Config) { c.Addr, c.Discoverable = "127.0.0.1:0", false }
	g.hostedPath = path
	if err := g.createHosted("Den", "pw", 0); err != nil {
		t.Fatal(err)
	}
	id := g.hosted.chain.ID().String()
	if _, err := os.Stat(chainFileFor(path, id)); err != nil {
		t.Fatalf("chain file: %v", err)
	}
	b, _ := os.ReadFile(path)
	text := string(b)
	if !strings.Contains(text, id) || !strings.Contains(text, "password_key = ") || strings.Contains(text, "password =") {
		t.Errorf("hosted.toml:\n%s", text)
	}
	for _, k := range []string{"server_name", "channels", "grants", "allowed_keys"} {
		if strings.Contains(text, k) {
			t.Errorf("hosted.toml has group key %q:\n%s", k, text)
		}
	}
}
