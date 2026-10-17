package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSettingsMissing(t *testing.T) {
	got, err := LoadSettings(filepath.Join(t.TempDir(), "nope", "settings.toml"))
	if err != nil || got.Name != "" || got.Palette != "" || got.Scanlines != nil || got.ThemePath != "" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "settings.toml")
	off := false
	in := Settings{Name: "zoë", Palette: "green", Scanlines: &off, ThemePath: "x.toml"}
	if err := SaveSettings(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "zoë" || out.Palette != "green" || out.ThemePath != "x.toml" || out.Scanlines == nil || *out.Scanlines {
		t.Fatalf("got %+v", out)
	}
}

func TestSettingsUnsetScanlinesStaysUnset(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	if err := SaveSettings(p, Settings{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	out, err := LoadSettings(p)
	if err != nil || out.Scanlines != nil {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestCurvatureSettingDefaultsOnAndRoundTrips(t *testing.T) {
	if !(Settings{}).CurvatureEnabled() {
		t.Fatal("nil must mean on")
	}
	p := filepath.Join(t.TempDir(), "settings.toml")
	off := false
	if err := SaveSettings(p, Settings{Curvature: &off}); err != nil {
		t.Fatal(err)
	}
	out, err := LoadSettings(p)
	if err != nil || out.Curvature == nil || *out.Curvature || out.CurvatureEnabled() {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestLoadSettingsMalformed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(p, []byte("name = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSettings(p); err == nil {
		t.Fatal("want error")
	}
}

func TestSaveSettingsLeavesNoTemp(t *testing.T) {
	d := t.TempDir()
	if err := SaveSettings(filepath.Join(d, "settings.toml"), Settings{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	es, _ := os.ReadDir(d)
	if len(es) != 1 {
		t.Fatalf("want only settings.toml, got %v", es)
	}
}

func TestRailHitSettingsAndAdd(t *testing.T) {
	// window 400 tall, tiles 50: gear is the bottom tile, "+" above it.
	if got := RailHit(10, 380, 56, 8, 50, 2, 400); got != RailSettings {
		t.Fatalf("gear: %d", got)
	}
	if got := RailHit(10, 330, 56, 8, 50, 2, 400); got != RailAdd {
		t.Fatalf("add: %d", got)
	}
	if got := RailHit(10, 60, 56, 8, 50, 2, 400); got != 1 {
		t.Fatalf("tile: %d", got)
	}
	if got := RailHit(100, 380, 56, 8, 50, 2, 400); got != RailNone {
		t.Fatalf("outside: %d", got)
	}
}

func TestMembersShownDefaultsOn(t *testing.T) {
	if !(Settings{}).MembersShown() {
		t.Fatal("zero Settings must show members")
	}
	off := false
	if (Settings{Members: &off}).MembersShown() {
		t.Fatal("Members=false must hide")
	}
}

func TestMembersRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	off := false
	if err := SaveSettings(p, Settings{Members: &off}); err != nil {
		t.Fatal(err)
	}
	out, err := LoadSettings(p)
	if err != nil || out.Members == nil || *out.Members || out.MembersShown() {
		t.Fatalf("false: %+v %v", out, err)
	}
	if err := SaveSettings(p, Settings{Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); strings.Contains(string(b), "members") {
		t.Fatalf("nil wrote members key:\n%s", b)
	}
	if err := os.WriteFile(p, []byte("name = \"old\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err = LoadSettings(p); err != nil || !out.MembersShown() {
		t.Fatalf("old file: %+v %v", out, err)
	}
}

// A first run needs settings.toml, servers.toml and identity.key all absent; a
// user who only has a servers list or a key predates settings and keeps the
// retro look.
func TestFirstRunNeedsEveryFileAbsent(t *testing.T) {
	for _, have := range []string{"", "servers.toml", "identity.key"} {
		dir := t.TempDir()
		if have != "" {
			if err := os.WriteFile(filepath.Join(dir, have), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		s, err := LoadOrInitSettings(filepath.Join(dir, "settings.toml"), filepath.Join(dir, "servers.toml"), filepath.Join(dir, "identity.key"))
		if err != nil {
			t.Fatal(err)
		}
		modern := s.Palette == "slate" && s.FontID() == "jetbrains-mono"
		legacy := s.Palette == "" && s.FontID() == "vt323"
		if have == "" && !modern {
			t.Errorf("nothing exists: %+v, want modern defaults", s)
		}
		if have != "" && !legacy {
			t.Errorf("%s exists: %+v (font %q), want amber/VT323", have, s, s.FontID())
		}
	}
}

func TestFirstRunDefaultsAreModern(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "settings.toml") // does not exist
	s, err := LoadOrInitSettings(p, filepath.Join(d, "servers.toml"), filepath.Join(d, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Palette != "slate" || s.Font != "jetbrains-mono" || s.FontID() != "jetbrains-mono" || s.Scanlines != nil {
		t.Fatalf("first run = %+v", s)
	}
}

func TestLegacySettingsKeepRetroFont(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(p, []byte("name = \"zoe\"\npalette = \"green\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadOrInitSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Palette != "green" || s.Name != "zoe" || s.FontID() != "vt323" {
		t.Fatalf("legacy = %+v (font %q)", s, s.FontID())
	}
	// A legacy file with no palette stays amber (""), not slate.
	if err := os.WriteFile(p, []byte("name = \"zoe\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, _ = LoadOrInitSettings(p); s.Palette != "" || s.FontID() != "vt323" {
		t.Fatalf("legacy no palette = %+v", s)
	}
}

func TestFontRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	if err := SaveSettings(p, Settings{Font: "geist-mono"}); err != nil {
		t.Fatal(err)
	}
	s, err := LoadOrInitSettings(p)
	if err != nil || s.Font != "geist-mono" || s.FontID() != "geist-mono" {
		t.Fatalf("got %+v, %v", s, err)
	}
}

func TestMalformedSettingsNotFirstRun(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(p, []byte("name = = broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadOrInitSettings(p)
	if err == nil {
		t.Fatal("malformed file must error")
	}
	if s.Palette != "" || s.Font != "" || s.FontID() != "vt323" {
		t.Fatalf("malformed file got first-run defaults: %+v", s)
	}
}
