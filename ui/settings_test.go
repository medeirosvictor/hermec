package ui

import (
	"path/filepath"
	"testing"

	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
)

func TestStartupThemePrecedence(t *testing.T) {
	on := true
	th, crt, n, err := startupTheme("", state.Settings{Palette: "green", Scanlines: &on})
	if err != nil || n != "" || presetName(th) != "green" || !crt {
		t.Fatalf("saved preset: %v %v %q %q", err, crt, n, presetName(th))
	}
	// Explicit -theme beats the saved palette.
	th, _, _, err = startupTheme("../example.theme.toml", state.Settings{Palette: "green"})
	if err != nil || presetName(th) == "green" {
		t.Fatalf("flag theme should win: %v %q", err, presetName(th))
	}
	// Bad saved values become a notice, not an error.
	th, _, n, err = startupTheme("", state.Settings{Palette: "nope"})
	if err != nil || n == "" || presetName(th) != "amber" {
		t.Fatalf("bad palette: %v %q", err, n)
	}
	_, _, n, err = startupTheme("", state.Settings{ThemePath: "missing.toml"})
	if err != nil || n == "" {
		t.Fatalf("bad theme path: %v %q", err, n)
	}
}

func TestPresetNameRoundTrip(t *testing.T) {
	for _, n := range theme.PresetNames() {
		p, ok := presetByName(n)
		if !ok || presetName(p) != n {
			t.Fatalf("%s: %v %q", n, ok, presetName(p))
		}
	}
}

func TestSettingsPersistFromEvents(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	g := &game{st: state.New(), th: theme.Default(), settingsPath: p, autoName: "anon-1"}
	g.setScanlines(false)
	g.cyclePalette()
	g.rememberName("anon-1") // unedited startup name: not persisted
	s, err := state.LoadSettings(p)
	if err != nil || s.Scanlines == nil || *s.Scanlines || s.Palette != "green" || s.Name != "" {
		t.Fatalf("got %+v %v", s, err)
	}
	g.rememberName("zoe")
	if s, _ = state.LoadSettings(p); s.Name != "zoe" {
		t.Fatalf("got %+v", s)
	}
}
