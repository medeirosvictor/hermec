// Package theme holds the GUI colour palette and font settings. This file is
// deliberately free of Ebiten so it can be tested headlessly; font.go is the
// only file that touches Ebiten text.
package theme

import (
	_ "embed"
	"fmt"
	"image/color"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed default.toml
var defaultTOML string

// Theme is the visual configuration of the client.
type Theme struct {
	BG, FG, Dim, Bright color.RGBA
	Scanlines           bool
	FontSize            float64
}

// file mirrors the TOML layout.
type file struct {
	BG        string  `toml:"bg"`
	FG        string  `toml:"fg"`
	Dim       string  `toml:"dim"`
	Bright    string  `toml:"bright"`
	Scanlines bool    `toml:"scanlines"`
	FontSize  float64 `toml:"font_size"`
}

// Default returns the amber palette.
func Default() Theme {
	t, err := decode(defaultTOML, file{})
	if err != nil {
		panic("theme: bad embedded default.toml: " + err.Error())
	}
	return t
}

var presetNames = []string{"amber", "green", "blue"}

// PresetNames returns the preset names in Presets() order.
func PresetNames() []string { return append([]string(nil), presetNames...) }

// Presets returns the built-in palettes: amber (the default), green (P1
// phosphor) and blue (P4). They inherit Scanlines and FontSize from Default.
func Presets() []Theme {
	amber := Default()
	green, blue := amber, amber
	green.BG, green.FG, green.Dim, green.Bright = rgb(0x0A, 0x0F, 0x0A), rgb(0x33, 0xFF, 0x33), rgb(0x1A, 0x7A, 0x1A), rgb(0x99, 0xFF, 0x99)
	blue.BG, blue.FG, blue.Dim, blue.Bright = rgb(0x0A, 0x0C, 0x10), rgb(0x9F, 0xD3, 0xFF), rgb(0x4A, 0x6B, 0x8A), rgb(0xE0, 0xF0, 0xFF)
	return []Theme{amber, green, blue}
}

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 0xFF} }

// NextPreset returns the preset after the one whose palette matches cur,
// keeping cur's Scanlines and FontSize. A palette matching no preset (e.g. a
// custom -theme file) is followed by the first preset.
func NextPreset(cur Theme) Theme {
	ps := Presets()
	next := 0
	for i, p := range ps {
		if p.BG == cur.BG && p.FG == cur.FG && p.Dim == cur.Dim && p.Bright == cur.Bright {
			next = (i + 1) % len(ps)
			break
		}
	}
	n := ps[next]
	n.Scanlines, n.FontSize = cur.Scanlines, cur.FontSize
	return n
}

// Load reads a theme TOML file. Missing keys keep Default values; unknown
// keys and malformed colours are errors. An empty path returns Default().
func Load(path string) (Theme, error) {
	if path == "" {
		return Default(), nil
	}
	f := toFile(Default())
	md, err := toml.DecodeFile(path, &f)
	if err != nil {
		return Theme{}, fmt.Errorf("theme: %w", err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		keys := make([]string, len(u))
		for i, k := range u {
			keys[i] = k.String()
		}
		return Theme{}, fmt.Errorf("theme: unknown keys in %s: %s", path, strings.Join(keys, ", "))
	}
	return build(f)
}

func decode(src string, f file) (Theme, error) {
	if _, err := toml.Decode(src, &f); err != nil {
		return Theme{}, err
	}
	return build(f)
}

func build(f file) (Theme, error) {
	var t Theme
	var err error
	for _, c := range []struct {
		name string
		in   string
		out  *color.RGBA
	}{{"bg", f.BG, &t.BG}, {"fg", f.FG, &t.FG}, {"dim", f.Dim, &t.Dim}, {"bright", f.Bright, &t.Bright}} {
		if *c.out, err = parseHex(c.in); err != nil {
			return Theme{}, fmt.Errorf("theme: %s: %w", c.name, err)
		}
	}
	t.Scanlines = f.Scanlines
	t.FontSize = f.FontSize
	return t, nil
}

func toFile(t Theme) file {
	return file{hex(t.BG), hex(t.FG), hex(t.Dim), hex(t.Bright), t.Scanlines, t.FontSize}
}

func hex(c color.RGBA) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

func parseHex(s string) (color.RGBA, error) {
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{}, fmt.Errorf("invalid colour %q, want #RRGGBB", s)
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(strings.ToLower(s[1:]), "%02x%02x%02x", &r, &g, &b); err != nil {
		return color.RGBA{}, fmt.Errorf("invalid colour %q, want #RRGGBB", s)
	}
	return color.RGBA{r, g, b, 0xFF}, nil
}
