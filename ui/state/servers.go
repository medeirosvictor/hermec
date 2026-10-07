package state

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// MaxServers caps the saved server list.
const MaxServers = 20

// ServerEntry is one remembered server.
type ServerEntry struct {
	URL         string    `toml:"url"`
	Label       string    `toml:"label"`
	LastChannel string    `toml:"last_channel"`
	LastSeen    time.Time `toml:"last_seen"`
}

type serversFile struct {
	Servers []ServerEntry `toml:"server"`
}

// LoadServers reads path. A missing file yields an empty list and no error.
func LoadServers(path string) ([]ServerEntry, error) {
	var f serversFile
	if _, err := toml.DecodeFile(path, &f); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return f.Servers, nil
}

// SaveServers writes entries to path via a temp file and rename, creating the
// parent directory.
func SaveServers(path string, entries []ServerEntry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".servers-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	err = toml.NewEncoder(tmp).Encode(serversFile{Servers: entries})
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

// Touch upserts url (newest first, capped at MaxServers) and returns a new
// slice. Empty label/channel keep the existing values on update.
func Touch(entries []ServerEntry, url, label, channel string) []ServerEntry {
	e := ServerEntry{URL: url, Label: label, LastChannel: channel, LastSeen: time.Now().UTC()}
	out := make([]ServerEntry, 0, len(entries)+1)
	out = append(out, e)
	for _, old := range entries {
		if old.URL == url {
			if e.Label == "" {
				out[0].Label = old.Label
			}
			if e.LastChannel == "" {
				out[0].LastChannel = old.LastChannel
			}
			continue
		}
		out = append(out, old)
	}
	if len(out) > MaxServers {
		out = out[:MaxServers]
	}
	return out
}

// HostPort returns the host:port of a server URL, or raw when unparseable.
func HostPort(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// Initials is the first two runes of the label, or of the host.
func Initials(e ServerEntry) string {
	s := e.Label
	if s == "" {
		s = HostPort(e.URL)
	}
	r := []rune(s)
	if len(r) > 2 {
		r = r[:2]
	}
	return string(r)
}

// Rail hit results besides a tile index.
const (
	RailNone = -1
	RailAdd  = -2

	RailSettings = -3
)

// RailHit maps a click to a rail tile: an index in [0,count), RailAdd for the
// "+" tile pinned just above the settings (gear) tile at the bottom of a
// window h tall, RailSettings for the gear tile, or RailNone.
func RailHit(x, y, railW, top, tileH float64, count int, h float64) int {
	if !InRect(x, y, 0, 0, railW, h) {
		return RailNone
	}
	if y >= h-tileH {
		return RailSettings
	}
	if y >= h-2*tileH {
		return RailAdd
	}
	i := RowAt(y, top, tileH, count)
	if i < 0 {
		return RailNone
	}
	return i
}
