package ui

import (
	"image"
	"path/filepath"
	"testing"
	"time"

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
	g := &game{th: theme.Default(), settingsPath: p, autoName: "bob", set: state.Settings{Name: "zoe"}}
	g.rememberName("bob") // connecting with the unedited -name flag value
	if s, _ := state.LoadSettings(p); s.Name != "zoe" {
		t.Fatalf("flag name stuck: %+v", s)
	}
}

func TestNameFlushOnCommit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	g := &game{th: theme.Default(), settingsPath: p, set: state.Settings{Name: "zoe"}, settingsOpen: true}
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

func TestCurvatureRowTogglesAndPersists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	g := &game{th: theme.Default(), settingsPath: p, crtOn: true, w: 960, h: 600}
	if g.curvature() != g.th.Curvature || g.curvature() <= 0 {
		t.Fatalf("default curvature = %v", g.curvature())
	}
	g.activateSetting(rowCurvature) // on -> off
	s, err := state.LoadSettings(p)
	if err != nil || s.Curvature == nil || *s.Curvature {
		t.Fatalf("got %+v %v", s, err)
	}
	if g.curvature() != 0 || g.curvedCursor() {
		t.Fatal("curvature off must zero the effective curvature and cursor correction")
	}
	g.toggleCurvature() // F3: off -> on
	if g.curvature() == 0 || !g.curvedCursor() {
		t.Fatal("toggle back on failed")
	}
}

func TestSettingsPersistFromEvents(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	g := &game{th: theme.Default(), settingsPath: p, autoName: "anon-1"}
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
	g := &game{th: theme.Default(), settingsPath: p,
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

func TestFontRowCyclesRebuildsAndPersists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	g := testGame(t)
	g.settingsPath = p
	g.set = state.Settings{Font: "vt323"}
	var err error
	if g.face, g.faceS, g.faceT, err = buildFaces("vt323", g.th.FontSize, 100); err != nil {
		t.Skip(err)
	}
	oldFace, oldSmall, oldTitle := g.face, g.faceS, g.faceT
	var lc state.LayoutCache // what main_scene feeds: Lines(..., g.set.FontID())
	lc.Lines("c", nil, 80, g.th.FontSize, g.set.FontID())

	g.activateSetting(rowFont)

	want := theme.NextFont("vt323")
	if g.set.Font != want {
		t.Fatalf("set.Font = %q, want %q", g.set.Font, want)
	}
	if g.face == nil || g.faceS == nil || g.faceT == nil {
		t.Fatal("faces not rebuilt")
	}
	if g.face.Source == oldFace.Source || g.faceS.Source == oldSmall.Source || g.faceT.Source == oldTitle.Source {
		t.Fatal("face sources did not change")
	}
	if g.face.Size != g.th.FontSize || g.faceS.Size != g.th.FontSize*smallScale || g.faceT.Size != g.th.FontSize*titleScale {
		t.Fatalf("sizes %v %v %v", g.face.Size, g.faceS.Size, g.faceT.Size)
	}
	if _, relaid := lc.Lines("c", nil, 80, g.th.FontSize, g.set.FontID()); !relaid {
		t.Fatal("layout cache not invalidated by the font swap")
	}
	if s, err := state.LoadSettings(p); err != nil || s.Font != want {
		t.Fatalf("persisted %+v, %v", s, err)
	}
}

func TestFontRowIsAfterPalette(t *testing.T) {
	if rowFont != rowPalette+1 {
		t.Fatalf("rowFont=%d rowPalette=%d", rowFont, rowPalette)
	}
}

func TestStartupFaces(t *testing.T) {
	th := theme.Default()
	f, fs, ft, err := buildFaces("geist-mono", th.FontSize, 100)
	if err != nil {
		t.Skip(err)
	}
	want, _ := theme.Face("geist-mono", th.FontSize)
	if f.Source != want.Source || fs.Size != th.FontSize*smallScale || ft.Size != th.FontSize*titleScale {
		t.Fatal("buildFaces does not use the saved font and startup scales")
	}
}

// The font id must be part of both the layout and the scrollback render key,
// as wired in the real code paths (relayout, sbKeyFor).
func TestFontIsPartOfCacheKeys(t *testing.T) {
	g := &game{th: theme.Default(), set: state.Settings{Font: "vt323"}}
	var lc state.LayoutCache
	msgs := []state.Message{{FromName: "a", Text: "hi", TS: time.Unix(1, 0)}}
	g.relayout(&lc, "c", msgs, 40)
	if _, relaid := g.relayout(&lc, "c", msgs, 40); relaid {
		t.Fatal("same font relaid")
	}
	g.set.Font = "geist-mono"
	if _, relaid := g.relayout(&lc, "c", msgs, 40); !relaid {
		t.Fatal("font change did not re-layout")
	}

	r := image.Rect(0, 0, 100, 100)
	gm := geometry{adv: 9, lh: 20}
	g.set.Font = "vt323"
	a := g.sbKeyFor(1, 0, r, gm)
	g.set.Font = "geist-mono"
	b := g.sbKeyFor(1, 0, r, gm)
	if a == b {
		t.Fatal("sbKey equal for different fonts with equal adv/lh/colours")
	}
}
