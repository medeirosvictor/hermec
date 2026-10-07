package theme

import (
	"bytes"
	"sync"

	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/gomono"
)

var (
	srcOnce sync.Once
	src     *text.GoTextFaceSource
	srcErr  error
)

// Face returns a Go Mono face at the given size, sharing one cached source.
func Face(size float64) (*text.GoTextFace, error) {
	srcOnce.Do(func() {
		src, srcErr = text.NewGoTextFaceSource(bytes.NewReader(gomono.TTF))
	})
	if srcErr != nil {
		return nil, srcErr
	}
	return &text.GoTextFace{Source: src, Size: size}, nil
}
