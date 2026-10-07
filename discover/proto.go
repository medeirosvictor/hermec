// Package discover defines the LAN discovery packet format shared by the
// server responder and the client scanner. It imports nothing from the
// server or UI packages.
package discover

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Magic is the exact payload of a discovery probe datagram.
const Magic = "HERMEC_DISCOVER_1"

// MaxPacket is the largest discovery datagram either side sends or accepts.
const MaxPacket = 512

// MaxNameRunes is the longest permitted server name.
const MaxNameRunes = 64

// Announce is a server's reply to a probe.
type Announce struct {
	Hermec int    `json:"hermec"`
	Name   string `json:"name"`
	Port   int    `json:"port"`
	Ver    string `json:"ver"`
}

// EncodeAnnounce marshals a as the JSON reply payload. It fails if the
// result would exceed MaxPacket bytes.
func EncodeAnnounce(a Announce) ([]byte, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxPacket {
		return nil, fmt.Errorf("discover: announce is %d bytes, max %d", len(b), MaxPacket)
	}
	return b, nil
}

// ParseAnnounce strictly decodes a reply: size <= MaxPacket, hermec == 1,
// port in 1..65535 and name at most MaxNameRunes runes.
func ParseAnnounce(b []byte) (Announce, error) {
	if len(b) > MaxPacket {
		return Announce{}, errors.New("discover: announce too large")
	}
	var a Announce
	if err := json.Unmarshal(b, &a); err != nil {
		return Announce{}, fmt.Errorf("discover: bad announce: %w", err)
	}
	if a.Hermec != 1 {
		return Announce{}, fmt.Errorf("discover: unsupported hermec version %d", a.Hermec)
	}
	if a.Port < 1 || a.Port > 65535 {
		return Announce{}, fmt.Errorf("discover: bad port %d", a.Port)
	}
	if utf8.RuneCountInString(a.Name) > MaxNameRunes {
		return Announce{}, errors.New("discover: name too long")
	}
	return a, nil
}
