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

func TestFlagNameDoesNotOverwriteSavedName(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	if err := state.SaveSettings(p, state.Settings{Name: "zoe"}); err != nil {
		t.Fatal(err)
	}
	g := &game{st: state.New(), th: theme.Default(), settingsPath: p, autoName: "bob", set: state.Settings{Name: "zoe"}}
	g.rememberName("bob") // connecting with the unedited -name flag value
	if s, _ := state.LoadSettings(p); s.Name != "zoe" {
		t.Fatalf("flag name stuck: %+v", s)
	}
}

func TestNameFlushOnCommit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	g := &game{st: state.New(), th: theme.Default(), settingsPath: p, set: state.Settings{Name: "zoe"}, settingsOpen: true}
	g.connect = newConnectForm("zo", "ws://", false)
	g.nameDirty = true // typing marks dirty without writing
	if s, _ := state.LoadSettings(p); s.Name != "" {
		t.Fatalf("written before commit: %+v", s)
	}
	g.closeSettings()
	if s, _ := state.LoadSettings(p); s.Name != "zo" || g.settingsOpen {
		t.Fatalf("not flushed: %+v", s)
	}
	// An emptied field persists as empty (default name next start).
	g.connect.name = field{}
	g.nameDirty = true
	g.flushName()
	if s, _ := state.LoadSettings(p); s.Name != "" {
		t.Fatalf("empty not persisted: %+v", s)
	}
}

func TestStartupThemeBadThemeFallsBackToPalette(t *testing.T) {
	th, _, n, err := startupTheme("", state.Settings{ThemePath: "missing.toml", Palette: "blue"})
	if err != nil || n == "" || presetName(th) != "blue" {
		t.Fatalf("got %q %q %v", presetName(th), n, err)
	}
}

func TestUpdateCheckRowTogglesAndPersists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	g := &game{st: state.New(), th: theme.Default(), settingsPath: p}
	g.activateSetting(rowUpdateCheck) // default on -> off
	s, err := state.LoadSettings(p)
	if err != nil || s.UpdateCheck == nil || *s.UpdateCheck {
		t.Fatalf("got %+v %v", s, err)
	}
	g.activateSetting(rowUpdateCheck)
	if s, _ = state.LoadSettings(p); s.UpdateCheck == nil || !*s.UpdateCheck {
		t.Fatalf("got %+v", s)
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

func TestDeviceRowsCycleAndPersist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	g := &game{st: state.New(), th: theme.Default(), settingsPath: p,
		devIn: []string{"Micrófono 日本", "B"}, devOut: []string{"Altavoz"}}
	g.activateSetting(rowInput)
	g.activateSetting(rowOutput)
	s, err := state.LoadSettings(p)
	if err != nil || s.InputDevice != "Micrófono 日本" || s.OutputDevice != "Altavoz" {
		t.Fatalf("got %+v %v", s, err)
	}
	g.activateSetting(rowOutput) // wraps back to default
	if s, _ = state.LoadSettings(p); s.OutputDevice != "" {
		t.Fatalf("got %+v", s)
	}
}

func TestDeviceLabel(t *testing.T) {
	av := []string{"A"}
	if deviceLabel("", av) != "default" || deviceLabel("A", av) != "A" || deviceLabel("Z", av) != "Z (missing)" {
		t.Fatal("labels")
	}
}

func TestSavedDevicesNoticeNothingSaved(t *testing.T) {
	// Must not enumerate (or notice) when nothing is saved.
	if n := savedDevicesNotice(state.Settings{}); n != "" {
		t.Fatalf("got %q", n)
	}
}