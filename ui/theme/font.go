package theme

import (
	"bytes"
	_ "embed"
	"sync"

	"github.com/hajimehoshi/ebiten/v2/text/v2"
)

// VT323 by The VT323 Project Authors, SIL Open Font License 1.1
// (see VT323-LICENSE.txt).
//
//go:embed VT323-Regular.ttf
var vt323TTF []byte

var (
	srcOnce sync.Once
	src     *text.GoTextFaceSource
	srcErr  error
)

// Face returns a VT323 face at the given size, sharing one cached source.
func Face(size float64) (*text.GoTextFace, error) {
	srcOnce.Do(func() {
		src, srcErr = text.NewGoTextFaceSource(bytes.NewReader(vt323TTF))
	})
	if srcErr != nil {
		return nil, srcErr
	}
	return &text.GoTextFace{Source: src, Size: size}, nil
}
