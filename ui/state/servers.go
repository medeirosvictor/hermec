package state

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/medeirosvictor/hermec/core/names"
)

// MaxServers caps the saved server list.
const MaxServers = 20

// MaxServerNameRunes caps a server-announced name; MaxNickRunes caps the
// user's own nickname for a server.
const (
	MaxServerNameRunes = 64
	MaxNickRunes       = 32
)

// ServerEntry is one remembered server.
type ServerEntry struct {
	URL         string    `toml:"url"`
	Label       string    `toml:"label"`
	LastChannel string    `toml:"last_channel"`
	LastSeen    time.Time `toml:"last_seen"`
	Name        string    `toml:"name,omitempty"` // announced by the server
	Nick        string    `toml:"nick,omitempty"` // the user's own name for it

	// ServerID pins the server's identity (chain.ID.String()) once first seen.
	ServerID string `toml:"server_id,omitempty"`

	// PasswordKey caches proto.PasswordKey(password, server ID) as hex after a
	// successful login, so scrypt runs once and the password is not kept.
	PasswordKey string `toml:"password_key,omitempty"`

	// Pending marks an entry not yet connected; it is never saved.
	Pending bool `toml:"-"`
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
	var keep []ServerEntry
	for _, e := range entries {
		if !e.Pending {
			keep = append(keep, e)
		}
	}
	err = toml.NewEncoder(tmp).Encode(serversFile{Servers: keep})
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

// Touch upserts url and returns a new slice. An existing entry is updated in
// place so the rail order stays stable; a new one goes first. Empty
// label/name/channel keep the existing values on update; Nick is never
// touched. Over MaxServers, the last entries whose URL is not live are dropped; live may be nil (nothing live).
func Touch(entries []ServerEntry, url, label, name, channel string, live func(url string) bool) []ServerEntry {
	out := append([]ServerEntry(nil), entries...)
	found := false
	for i := range out {
		if out[i].URL != url {
			continue
		}
		found = true
		if label != "" {
			out[i].Label = label
		}
		if name != "" {
			out[i].Name = name
		}
		if channel != "" {
			out[i].LastChannel = channel
		}
		out[i].LastSeen = time.Now().UTC()
		out[i].Pending = false
		break
	}
	if !found {
		e := ServerEntry{URL: url, Label: label, Name: name, LastChannel: channel, LastSeen: time.Now().UTC()}
		out = append([]ServerEntry{e}, out...)
	}
	for i := len(out) - 1; i >= 0 && len(out) > MaxServers; i-- {
		if live != nil && live(out[i].URL) {
			continue
		}
		out = append(out[:i], out[i+1:]...)
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

// DisplayName is what the UI calls a server: the user's nick, else the name
// the server announced, else the saved label, else the host:port.
func DisplayName(e ServerEntry) string {
	switch {
	case e.Nick != "":
		return e.Nick
	case e.Name != "":
		return e.Name
	case e.Label != "":
		return e.Label
	}
	return HostPort(e.URL)
}

// CleanName strips control, format (bidi overrides and the like) and line or
// paragraph separator runes from s, trims spaces and caps the result at max
// runes.
func CleanName(s string, max int) string { return names.Clean(s, max) }

// SetNick returns a copy of entries with the nick of url set to the cleaned
// nick (empty clears it). An unknown url yields an unchanged copy.
func SetNick(entries []ServerEntry, url, nick string) []ServerEntry {
	out := append([]ServerEntry(nil), entries...)
	nick = CleanName(nick, MaxNickRunes)
	for i := range out {
		if out[i].URL == url {
			out[i].Nick = nick
		}
	}
	return out
}

// Remove returns a copy of entries without url.
func Remove(entries []ServerEntry, url string) []ServerEntry {
	out := make([]ServerEntry, 0, len(entries))
	for _, e := range entries {
		if e.URL != url {
			out = append(out, e)
		}
	}
	return out
}

// Initials is the first two runes of the display name.
func Initials(e ServerEntry) string {
	r := []rune(DisplayName(e))
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

// SetPasswordKey returns a copy of entries with the cached password key of url
// set to key (hex); an empty key leaves the entries as they are.
func SetPasswordKey(entries []ServerEntry, url, key string) []ServerEntry {
	out := append([]ServerEntry(nil), entries...)
	if key == "" {
		return out
	}
	for i := range out {
		if out[i].URL == url {
			out[i].PasswordKey = key
		}
	}
	return out
}

// PinID returns a copy of entries with the server ID of url set to id, only
// when it has none yet: a pin is never replaced silently.
func PinID(entries []ServerEntry, url, id string) []ServerEntry {
	out := append([]ServerEntry(nil), entries...)
	if id == "" {
		return out
	}
	for i := range out {
		if out[i].URL == url && out[i].ServerID == "" {
			out[i].ServerID = id
		}
	}
	return out
}
