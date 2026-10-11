package theme

import (
	"image/color"
	"testing"
)

func hexc(s string) color.RGBA {
	c, err := parseHex(s)
	if err != nil {
		panic(err)
	}
	return c
}

func TestPresetsOrder(t *testing.T) {
	want := []string{"amber", "green", "blue", "parchment"}
	got := PresetNames()
	ps := Presets()
	if len(got) != len(want) || len(ps) != len(want) {
		t.Fatalf("names %v presets %d", got, len(ps))
	}
	for i, n := range want {
		if got[i] != n || ps[i].Name != n {
			t.Errorf("[%d] name %q / theme %q, want %q", i, got[i], ps[i].Name, n)
		}
		if _, ok := PresetSource(n); !ok {
			t.Errorf("no embedded source for %s", n)
		}
	}
	if _, ok := PresetSource("nope"); ok {
		t.Error("unknown preset has a source")
	}
	if Default() != ps[0] {
		t.Errorf("Default %+v != amber %+v", Default(), ps[0])
	}
}

func TestParchmentValues(t *testing.T) {
	p := Presets()[3]
	if p.BG != hexc("#EDE4D3") || p.FG != hexc("#3B2414") || p.Dim != hexc("#8A7560") ||
		p.Bright != hexc("#7A1F1F") || p.Panel != hexc("#E3D7C1") {
		t.Errorf("palette %+v", p)
	}
	if p.Glow != 0 || p.ScanlineStrength != 0.04 {
		t.Errorf("glow=%v scanline=%v", p.Glow, p.ScanlineStrength)
	}
	if p.Curvature != 0.06 || p.Vignette != 0.35 {
		t.Errorf("curvature=%v vignette=%v", p.Curvature, p.Vignette)
	}
}

func TestDerivedPanel(t *testing.T) {
	th, err := Parse("x", []byte("bg = \"#000000\"\nfg = \"#FF8000\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	// bg + (fg-bg)*0.06, rounded: 15.3 -> 15, 7.68 -> 8, 0.
	if want := (color.RGBA{15, 8, 0, 0xFF}); th.Panel != want {
		t.Errorf("panel = %v, want %v", th.Panel, want)
	}
	th, err = Load(writeTheme(t, "bg = \"#EDE4D3\"\nfg = \"#3B2414\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	// 237+(59-237)*.06=226.32, 228+(36-228)*.06=216.48, 211+(20-211)*.06=199.54
	if want := (color.RGBA{226, 216, 200, 0xFF}); th.Panel != want {
		t.Errorf("panel = %v, want %v", th.Panel, want)
	}
}

func TestNewKeyDefaults(t *testing.T) {
	th, err := Load(writeTheme(t, "bg = \"#101010\"\nfg = \"#FFFFFF\"\ndim = \"#808080\"\nbright = \"#FFFFFF\"\nscanlines = true\nfont_size = 20.0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if th.Curvature != 0.06 || th.Vignette != 0.35 || th.ScanlineStrength != 0.12 || !th.ScanBar ||
		th.ScanBarPeriod != 8 || th.ScanBarStrength != 0.04 || th.Glow != 0.12 || th.Radius != 6 {
		t.Errorf("defaults wrong: %+v", th)
	}
	if th.Name != "" {
		t.Errorf("loaded file has Name %q", th.Name)
	}
}

func TestNewKeysParse(t *testing.T) {
	th, err := Load(writeTheme(t, "curvature = 0\nscan_bar = false\nradius = 2.5\nscanline_strength = 0.5\nscan_bar_strength = 0.1\nscan_bar_period = 3.0\nvignette = 0.0\nglow = 0.0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if th.Curvature != 0 || th.ScanBar || th.Radius != 2.5 || th.ScanlineStrength != 0.5 ||
		th.ScanBarStrength != 0.1 || th.ScanBarPeriod != 3 || th.Vignette != 0 || th.Glow != 0 {
		t.Errorf("got %+v", th)
	}
}

func TestUnknownKeyStillErrors(t *testing.T) {
	if _, err := Load(writeTheme(t, "curvature = 0.1\nbogus = 1\n")); err == nil {
		t.Error("Load accepted unknown key")
	}
	if _, err := Parse("x", []byte("bogus = 1\n")); err == nil {
		t.Error("Parse accepted unknown key")
	}
}

func TestNextPresetCyclesByName(t *testing.T) {
	ps := Presets()
	if n := NextPreset(ps[3]); n.Name != "amber" {
		t.Errorf("parchment -> %q", n.Name)
	}
	c := Default()
	c.Name = ""
	if n := NextPreset(c); n.Name != "amber" {
		t.Errorf("unnamed -> %q", n.Name)
	}
	th, err := Load(writeTheme(t, `fg = "#00FF00"`))
	if err != nil {
		t.Fatal(err)
	}
	if n := NextPreset(th); n.Name != "amber" {
		t.Errorf("custom -> %q", n.Name)
	}
}
