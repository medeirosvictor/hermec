// Package theme holds the GUI colour palette and font settings. This file is
// deliberately free of Ebiten so it can be tested headlessly; font.go is the
// only file that touches Ebiten text.
package theme

import (
	"embed"
	"fmt"
	"image/color"
	"math"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed default.toml
var defaultTOML string

//go:embed presets/*.toml
var presetFS embed.FS

// Theme is the visual configuration of the client.
type Theme struct {
	// Name is the preset name ("amber", ...); empty for a theme loaded from
	// a custom file.
	Name                string
	BG, FG, Dim, Bright color.RGBA
	Panel               color.RGBA // panel surface
	Warn                color.RGBA // failure / warning marks
	Scanlines          bool       // switch for the whole CRT pass
	FontSize            float64
	Curvature           float64 // barrel strength, 0 = flat
	Vignette            float64 // edge darkening
	ScanlineStrength    float64 // row darkening
	ScanBar             bool    // rolling scan bar
	ScanBarStrength     float64
	ScanBarPeriod       float64 // seconds per sweep
	Glow                float64 // additive phosphor glow
	Radius              float64 // panel corner radius in px
	CornerRadius        float64 // CRT tube corner radius in px
}

// file mirrors the TOML layout. An empty Panel means "derive from BG/FG".
type file struct {
	BG               string  `toml:"bg"`
	FG               string  `toml:"fg"`
	Dim              string  `toml:"dim"`
	Bright           string  `toml:"bright"`
	Panel            string  `toml:"panel"`
	Warn             string  `toml:"warn"`
	Scanlines        bool    `toml:"scanlines"`
	FontSize         float64 `toml:"font_size"`
	Curvature        float64 `toml:"curvature"`
	Vignette         float64 `toml:"vignette"`
	ScanlineStrength float64 `toml:"scanline_strength"`
	ScanBar          bool    `toml:"scan_bar"`
	ScanBarStrength  float64 `toml:"scan_bar_strength"`
	ScanBarPeriod    float64 `toml:"scan_bar_period"`
	Glow             float64 `toml:"glow"`
	Radius           float64 `toml:"radius"`
	CornerRadius     float64 `toml:"corner_radius"`
}

// baseFile is default.toml decoded, with Panel cleared so a file that sets
// bg/fg but not panel gets a panel derived from its own colours.
func baseFile() file {
	var f file
	if _, err := toml.Decode(defaultTOML, &f); err != nil {
		panic("theme: bad embedded default.toml: " + err.Error())
	}
	f.Panel = ""
	return f
}

// Default returns the amber palette, identical to the amber preset.
func Default() Theme {
	var f file
	if _, err := toml.Decode(defaultTOML, &f); err != nil {
		panic("theme: bad embedded default.toml: " + err.Error())
	}
	t, err := build(f)
	if err != nil {
		panic("theme: bad embedded default.toml: " + err.Error())
	}
	t.Name = "amber"
	return t
}

var presetNames = []string{"amber", "green", "blue", "parchment", "slate"}

// PresetNames returns the preset names in Presets() order.
func PresetNames() []string { return append([]string(nil), presetNames...) }

// PresetSource returns the embedded TOML of the named preset.
func PresetSource(name string) ([]byte, bool) {
	b, err := presetFS.ReadFile("presets/" + name + ".toml")
	return b, err == nil
}

// Presets returns the built-in themes: amber (the default), green (P1
// phosphor), blue (P4), parchment (light) and slate (modern dark). Keys a preset file omits keep
// the default.toml values.
func Presets() []Theme {
	out := make([]Theme, len(presetNames))
	for i, n := range presetNames {
		src, ok := PresetSource(n)
		if !ok {
			panic("theme: missing embedded preset " + n)
		}
		t, err := Parse(n, src)
		if err != nil {
			panic("theme: bad embedded preset " + n + ": " + err.Error())
		}
		out[i] = t
	}
	return out
}

// NextPreset returns the preset after the one named cur.Name, keeping cur's
// Scanlines and FontSize. An empty or unknown name (e.g. a custom -theme
// file) is followed by the first preset.
func NextPreset(cur Theme) Theme {
	ps := Presets()
	next := 0
	if cur.Name != "" {
		for i, p := range ps {
			if p.Name == cur.Name {
				next = (i + 1) % len(ps)
				break
			}
		}
	}
	n := ps[next]
	n.Scanlines, n.FontSize = cur.Scanlines, cur.FontSize
	return n
}

// Load reads a theme TOML file. Missing keys keep default.toml values (panel
// is derived); unknown keys and malformed colours are errors. The result has
// an empty Name. An empty path returns Default().
func Load(path string) (Theme, error) {
	if path == "" {
		return Default(), nil
	}
	f := baseFile()
	md, err := toml.DecodeFile(path, &f)
	if err != nil {
		return Theme{}, fmt.Errorf("theme: %w", err)
	}
	if err := checkUndecoded(md, path); err != nil {
		return Theme{}, err
	}
	return build(f)
}

// Parse decodes theme TOML over the default.toml base and tags the result
// with name. Unknown keys are errors.
func Parse(name string, src []byte) (Theme, error) {
	f := baseFile()
	md, err := toml.Decode(string(src), &f)
	if err != nil {
		return Theme{}, fmt.Errorf("theme: %w", err)
	}
	if err := checkUndecoded(md, name); err != nil {
		return Theme{}, err
	}
	t, err := build(f)
	if err != nil {
		return Theme{}, err
	}
	t.Name = name
	return t, nil
}

func checkUndecoded(md toml.MetaData, what string) error {
	u := md.Undecoded()
	if len(u) == 0 {
		return nil
	}
	keys := make([]string, len(u))
	for i, k := range u {
		keys[i] = k.String()
	}
	return fmt.Errorf("theme: unknown keys in %s: %s", what, strings.Join(keys, ", "))
}

func build(f file) (Theme, error) {
	var t Theme
	var err error
	for _, c := range []struct {
		name string
		in   string
		out  *color.RGBA
	}{{"bg", f.BG, &t.BG}, {"fg", f.FG, &t.FG}, {"dim", f.Dim, &t.Dim}, {"bright", f.Bright, &t.Bright}, {"warn", f.Warn, &t.Warn}} {
		if *c.out, err = parseHex(c.in); err != nil {
			return Theme{}, fmt.Errorf("theme: %s: %w", c.name, err)
		}
	}
	if f.Panel == "" {
		t.Panel = mix(t.BG, t.FG, 0.06)
	} else if t.Panel, err = parseHex(f.Panel); err != nil {
		return Theme{}, fmt.Errorf("theme: panel: %w", err)
	}
	t.Scanlines = f.Scanlines
	t.FontSize = f.FontSize
	t.Curvature, t.Vignette = f.Curvature, f.Vignette
	t.ScanlineStrength = f.ScanlineStrength
	t.ScanBar, t.ScanBarStrength, t.ScanBarPeriod = f.ScanBar, f.ScanBarStrength, f.ScanBarPeriod
	t.Glow, t.Radius = f.Glow, f.Radius
	t.CornerRadius = f.CornerRadius
	return t, nil
}

// mix returns a moved fraction f of the way toward b, rounded per channel.
func mix(a, b color.RGBA, f float64) color.RGBA {
	ch := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*f)) }
	return color.RGBA{ch(a.R, b.R), ch(a.G, b.G), ch(a.B, b.B), 0xFF}
}

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
