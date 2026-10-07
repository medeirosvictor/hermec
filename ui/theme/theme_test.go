package theme

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPalette(t *testing.T) {
	d := Default()
	want := map[string][2]color.RGBA{
		"bg":     {d.BG, {0x0A, 0x0A, 0x08, 0xFF}},
		"fg":     {d.FG, {0xFF, 0xB0, 0x00, 0xFF}},
		"dim":    {d.Dim, {0x7A, 0x55, 0x00, 0xFF}},
		"bright": {d.Bright, {0xFF, 0xCF, 0x40, 0xFF}},
	}
	for k, v := range want {
		if v[0] != v[1] {
			t.Errorf("%s = %v, want %v", k, v[0], v[1])
		}
	}
	if !d.Scanlines || d.FontSize != 18 {
		t.Errorf("scanlines=%v fontsize=%v", d.Scanlines, d.FontSize)
	}
}

func writeTheme(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "theme.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadPartialOverride(t *testing.T) {
	th, err := Load(writeTheme(t, `fg = "#00FF00"`))
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.FG = color.RGBA{0, 0xFF, 0, 0xFF}
	if th != want {
		t.Errorf("got %+v want %+v", th, want)
	}
}

func TestLoadRejects(t *testing.T) {
	if _, err := Load(writeTheme(t, `bogus = 1`)); err == nil {
		t.Error("unknown key accepted")
	}
	if _, err := Load(writeTheme(t, `fg = "#GGGGGG"`)); err == nil {
		t.Error("bad hex accepted")
	}
	th, err := Load("")
	if err != nil || th != Default() {
		t.Errorf("empty path: %+v %v", th, err)
	}
}

func TestLoadExampleFile(t *testing.T) {
	// Load example.theme.toml from repo root (../../example.theme.toml from ui/theme/)
	examplePath := filepath.Join("..", "..", "example.theme.toml")
	th, err := Load(examplePath)
	if err != nil {
		t.Fatalf("failed to load example.theme.toml: %v", err)
	}
	// Verify that defaults are loaded properly
	want := Default()
	if th != want {
		t.Errorf("example.theme.toml loaded incorrectly: got %+v, want %+v", th, want)
	}
}

func TestPresets(t *testing.T) {
	rgb := func(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 0xFF} }
	d := Default()
	want := []struct {
		name                string
		bg, fg, dim, bright color.RGBA
	}{
		{"amber", d.BG, d.FG, d.Dim, d.Bright},
		{"green", rgb(0x0A, 0x0F, 0x0A), rgb(0x33, 0xFF, 0x33), rgb(0x1A, 0x7A, 0x1A), rgb(0x99, 0xFF, 0x99)},
		{"blue", rgb(0x0A, 0x0C, 0x10), rgb(0x9F, 0xD3, 0xFF), rgb(0x4A, 0x6B, 0x8A), rgb(0xE0, 0xF0, 0xFF)},
	}
	got := Presets()
	if len(got) != len(want) {
		t.Fatalf("got %d presets, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if PresetNames()[i] != w.name {
			t.Errorf("name[%d] = %q, want %q", i, PresetNames()[i], w.name)
		}
		if g.BG != w.bg || g.FG != w.fg || g.Dim != w.dim || g.Bright != w.bright {
			t.Errorf("%s palette = %+v", w.name, g)
		}
		if g.Scanlines != d.Scanlines || g.FontSize != 18 {
			t.Errorf("%s scanlines=%v size=%v", w.name, g.Scanlines, g.FontSize)
		}
	}
}

func TestNextPreset(t *testing.T) {
	ps := Presets()
	if NextPreset(ps[0]) != ps[1] || NextPreset(ps[2]) != ps[0] {
		t.Error("cycle order wrong")
	}
	// A custom theme starts the cycle at the first preset, keeping its
	// scanlines and font size.
	c := Default()
	c.FG = color.RGBA{1, 2, 3, 0xFF}
	c.FontSize = 20
	n := NextPreset(c)
	if n.FG != ps[0].FG || n.FontSize != 20 {
		t.Errorf("custom -> %+v", n)
	}
}
