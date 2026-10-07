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

	UpdateCheck *bool `toml:"update_check,omitempty"` // nil = unset (enabled)

	InputDevice  string `toml:"input_device,omitempty"`  // "" = system default
	OutputDevice string `toml:"output_device,omitempty"` // "" = system default
}

// UpdateCheckEnabled reports whether the startup update check is on; unset
// means on.
func (s Settings) UpdateCheckEnabled() bool {
	return s.UpdateCheck == nil || *s.UpdateCheck
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
