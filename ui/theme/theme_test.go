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
	if !d.Scanlines || d.FontSize != 14 {
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
