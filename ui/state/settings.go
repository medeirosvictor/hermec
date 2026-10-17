package state

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Settings are the user's persisted choices. Zero values mean "unset".
type Settings struct {
	Name      string `toml:"name,omitempty"`
	Palette   string `toml:"palette,omitempty"`    // preset name; "" = default
	Scanlines *bool  `toml:"scanlines,omitempty"`  // nil = unset (theme decides)
	ThemePath string `toml:"theme_path,omitempty"` // custom theme file
	Font      string `toml:"font,omitempty"`       // font id; "" = vt323 (see FontID)

	FontScale int `toml:"font_scale,omitempty"` // percent; 0 = 100 (see FontScalePct)

	// UpdateCheck is no longer read: the update check always runs and the
	// settings row shows its status. The field stays so old files still load.
	UpdateCheck *bool `toml:"update_check,omitempty"`

	Curvature *bool `toml:"curvature,omitempty"` // nil = unset (on)

	Members *bool `toml:"members,omitempty"` // nil = unset (shown)

	InputDevice  string `toml:"input_device,omitempty"`  // "" = system default
	OutputDevice string `toml:"output_device,omitempty"` // "" = system default
}

// UpdateCheckEnabled reports whether the startup update check is on; unset
// means on.
func (s Settings) UpdateCheckEnabled() bool {
	return s.UpdateCheck == nil || *s.UpdateCheck
}

// CurvatureEnabled reports whether the CRT curvature is on; unset means on.
func (s Settings) CurvatureEnabled() bool {
	return s.Curvature == nil || *s.Curvature
}

// MembersShown reports whether the member roster is visible; unset means shown.
func (s Settings) MembersShown() bool {
	return s.Members == nil || *s.Members
}

// Defaults for a first run (no settings file): the modern look. An existing
// file that lacks these keys keeps the retro look instead; see FontID.
const (
	firstRunPalette = "slate"
	firstRunFont    = "jetbrains-mono"
	legacyFont      = "vt323"
)

// FontID is the font to use: the saved font, or vt323 when none is saved
// (every settings file written before the font option existed).
func (s Settings) FontID() string {
	if s.Font == "" {
		return legacyFont
	}
	return s.Font
}

// FirstRunSettings are the defaults when no settings file exists: the slate
// palette and JetBrains Mono. Scanlines stay nil so the theme (scanlines off
// for slate) decides.
func FirstRunSettings() Settings {
	return Settings{Palette: firstRunPalette, Font: firstRunFont}
}

// LoadOrInitSettings is LoadSettings, except that a missing file yields
// FirstRunSettings. An existing file is returned as saved, so users from before
// the font option keep their palette and VT323.
func LoadOrInitSettings(path string) (Settings, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return FirstRunSettings(), nil
	}
	return LoadSettings(path)
}

// LoadSettings reads path. A missing file yields zero Settings and no error.
func LoadSettings(path string) (Settings, error) {
	var s Settings
	if _, err := toml.DecodeFile(path, &s); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{}, nil
		}
		return Settings{}, err
	}
	return s, nil
}

// SaveSettings writes s to path via a temp file and rename, creating the
// parent directory.
func SaveSettings(path string, s Settings) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	err = toml.NewEncoder(tmp).Encode(s)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

// FontScales are the selectable text sizes in percent, in cycle order.
var FontScales = []int{80, 90, 100, 110, 125, 150}

// FontScalePct is the text size in percent; unset (or nonsense) means 100.
func (s Settings) FontScalePct() int {
	if s.FontScale <= 0 {
		return 100
	}
	return s.FontScale
}

// NextFontScale is the step after cur, wrapping around. 0 counts as 100, and
// a value that is not a step goes to 100.
func NextFontScale(cur int) int {
	if cur == 0 {
		cur = 100
	}
	for i, v := range FontScales {
		if v == cur {
			return FontScales[(i+1)%len(FontScales)]
		}
	}
	return 100
}
