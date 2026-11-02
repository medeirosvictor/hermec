package state

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/medeirosvictor/hermec/internal/fsutil"
)

// Settings are the user's persisted choices. Zero values mean "unset".
type Settings struct {
	Name      string `toml:"name,omitempty"`
	Palette   string `toml:"palette,omitempty"`    // preset name; "" = default
	Scanlines *bool  `toml:"scanlines,omitempty"`  // nil = unset (theme decides)
	ThemePath string `toml:"theme_path,omitempty"` // custom theme file
	Font      string `toml:"font,omitempty"`       // font id; "" = default (see FontID)

	FontScale int `toml:"font_scale,omitempty"` // percent; 0 = 100 (see FontScalePct)

	Curvature *bool `toml:"curvature,omitempty"` // nil = unset (on)

	Members *bool `toml:"members,omitempty"` // nil = unset (shown)

	InputDevice  string `toml:"input_device,omitempty"`  // "" = system default
	OutputDevice string `toml:"output_device,omitempty"` // "" = system default
	CameraDevice string `toml:"camera_device,omitempty"` // "" = system default

	ShareQuality string `toml:"share_quality,omitempty"` // "720p" (default) or "1080p"; see ShareQualityID

	Volumes map[string]int `toml:"volumes,omitempty"` // fingerprint -> playback percent; absent = 100 (see VolumePct)
}

// CurvatureEnabled reports whether the CRT curvature is on; unset means on.
func (s Settings) CurvatureEnabled() bool {
	return s.Curvature == nil || *s.Curvature
}

// MembersShown reports whether the member roster is visible; unset means shown.
func (s Settings) MembersShown() bool {
	return s.Members == nil || *s.Members
}

// Defaults for a first run (no settings file).
const (
	firstRunPalette = "slate"
	defaultFont     = "jetbrains-mono"
)

// FontID is the font to use: the saved font, or the default when none is saved.
func (s Settings) FontID() string {
	if s.Font == "" {
		return defaultFont
	}
	return s.Font
}

// FirstRunSettings are the defaults when no settings file exists: the slate
// palette and JetBrains Mono. Scanlines stay nil so the theme (scanlines off
// for slate) decides.
func FirstRunSettings() Settings {
	return Settings{Palette: firstRunPalette, Font: defaultFont}
}

// LoadOrInitSettings is LoadSettings, except that a missing file yields
// FirstRunSettings.
func LoadOrInitSettings(path string) (Settings, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return FirstRunSettings(), nil
	}
	return LoadSettings(path)
}

// LoadSettings reads path. A missing file yields zero Settings and no error.
func LoadSettings(path string) (Settings, error) {
	var s Settings
	md, err := toml.DecodeFile(path, &s)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{}, nil
		}
		return Settings{}, err
	}
	if err := rejectUnknownKeys(md); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// rejectUnknownKeys fails on keys the file sets but the decoder did not take,
// so a typo is not silently dropped.
func rejectUnknownKeys(md toml.MetaData) error {
	un := md.Undecoded()
	if len(un) == 0 {
		return nil
	}
	keys := make([]string, len(un))
	for i, k := range un {
		keys[i] = k.String()
	}
	return fmt.Errorf("unknown keys: %s", strings.Join(keys, ", "))
}

// SaveSettings writes s to path via a temp file and rename, creating the
// parent directory.
func SaveSettings(path string, s Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(s); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, buf.Bytes(), 0o600)
}

// FontScales are the selectable text sizes in percent, in cycle order.
var FontScales = []int{80, 90, 100, 110, 125, 150}

// FontScalePct is the text size in percent; unset, or any value that is not a
// step in FontScales, means 100.
func (s Settings) FontScalePct() int {
	for _, v := range FontScales {
		if v == s.FontScale {
			return v
		}
	}
	return 100
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
