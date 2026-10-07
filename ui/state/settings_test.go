package state

import (
	"os"
	"path/filepath"
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
