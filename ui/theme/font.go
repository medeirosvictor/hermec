package theme

import (
	"bytes"
	_ "embed"
	"sync"

	"github.com/hajimehoshi/ebiten/v2/text/v2"
)

// Bundled fonts, all SIL Open Font License 1.1 (see the *-LICENSE.txt files
// next to each). The UI layout assumes a monospace face, so every entry here
// must be monospace.
//
//go:embed VT323-Regular.ttf
var vt323TTF []byte

//go:embed JetBrainsMono-Regular.ttf
var jetbrainsMonoTTF []byte

//go:embed IBMPlexMono-Regular.ttf
var ibmPlexMonoTTF []byte

//go:embed GeistMono-Regular.ttf
var geistMonoTTF []byte

// fontEntry is one selectable font.
type fontEntry struct {
	id, label string
	data      []byte
	once      sync.Once
	src       *text.GoTextFaceSource
	err       error
}

// fonts is in cycle order; the first entry is the fallback for unknown ids.
var fonts = []*fontEntry{
	{id: "vt323", label: "VT323", data: vt323TTF},
	{id: "jetbrains-mono", label: "JetBrains Mono", data: jetbrainsMonoTTF},
	{id: "ibm-plex-mono", label: "IBM Plex Mono", data: ibmPlexMonoTTF},
	{id: "geist-mono", label: "Geist Mono", data: geistMonoTTF},
}

func fontByID(id string) *fontEntry {
	for _, f := range fonts {
		if f.id == id {
			return f
		}
	}
	return nil
}

// Fonts returns the font ids in cycle order.
func Fonts() []string {
	ids := make([]string, len(fonts))
	for i, f := range fonts {
		ids[i] = f.id
	}
	return ids
}

// FontLabel is the display name of a font id; an unknown id shows as VT323,
// the font Face falls back to.
func FontLabel(id string) string {
	if f := fontByID(id); f != nil {
		return f.label
	}
	return fonts[0].label
}

// NextFont returns the font after id, wrapping around. An unknown or empty id
// is followed by jetbrains-mono.
func NextFont(id string) string {
	for i, f := range fonts {
		if f.id == id {
			return fonts[(i+1)%len(fonts)].id
		}
	}
	return "jetbrains-mono"
}

// Face returns a face of the given font id at the given size, sharing one
// lazily built source per font. An unknown or empty id falls back to VT323.
func Face(font string, size float64) (*text.GoTextFace, error) {
	f := fontByID(font)
	if f == nil {
		f = fonts[0]
	}
	f.once.Do(func() {
		f.src, f.err = text.NewGoTextFaceSource(bytes.NewReader(f.data))
	})
	if f.err != nil {
		return nil, f.err
	}
	return &text.GoTextFace{Source: f.src, Size: size}, nil
}
