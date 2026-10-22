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

// saveHosted writes cfg in full, group fields included: the pre-chain
// hosted.toml format that the migration tests start from.
func saveHosted(path string, cfg server.Config) error {
	b, err := cfg.MarshalTOML()
	if err != nil {
		return err
	}
	return writeHosted(path, b)
}

func chainFileFor(path, id string) string {
	return filepath.Join(filepath.Dir(path), "chains", id+".jsonl")
}

func TestHostedMigrationCreatesChain(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path)
	if g.hosted == nil || g.hosted.srv == nil || g.hosted.chain == nil {
		t.Fatalf("hosted = %+v, want running with a chain", g.hosted)
	}
	id := g.hosted.chain.ID().String()
	if _, err := os.Stat(chainFileFor(path, id)); err != nil {
		t.Fatalf("chain file: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "server_id") || !strings.Contains(text, id) {
		t.Errorf("hosted.toml lacks server_id %s:\n%s", id, text)
	}
	for _, k := range []string{"server_name", "channels", "grants", "voice_channels"} {
		if strings.Contains(text, k) {
			t.Errorf("hosted.toml still has %q:\n%s", k, text)
		}
	}
	st := g.hosted.chain.State()
	if st.Founder != g.fp {
		t.Errorf("founder = %q, want %q", st.Founder, g.fp)
	}
	if st.Config.Name != "Den" || g.hosted.name() != "Den" {
		t.Errorf("chain name = %q, name() = %q", st.Config.Name, g.hosted.name())
	}
	if g.hosted.id != id {
		t.Errorf("hosted.id = %q", g.hosted.id)
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

// Group keys in hosted.toml are inert once the chain exists; boot says so.
func TestBootHostedWarnsOnGroupKeys(t *testing.T) {
	g, path := hostedTestGame(t, "127.0.0.1:0", true)
	g.bootHosted(path) // migrates: hosted.toml now holds local keys only
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
	if g2.hosted == nil || g2.hosted.srv == nil {
		t.Fatalf("second boot hosted = %+v", g2.hosted)
	}
	if !strings.Contains(g2.notice, "history_days") || !strings.Contains(g2.notice, "-apply-config") {
		t.Errorf("notice = %q", g2.notice)
	}
	if d := g2.hosted.chain.State().Config.HistoryDays; d != nil {
		t.Errorf("chain HistoryDays = %v, want unset", *d)
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
	if !strings.Contains(text, id) || !strings.Contains(text, `password = "pw"`) {
		t.Errorf("hosted.toml:\n%s", text)
	}
	for _, k := range []string{"server_name", "channels", "grants", "allowed_keys"} {
		if strings.Contains(text, k) {
			t.Errorf("hosted.toml has group key %q:\n%s", k, text)
		}
	}
}
