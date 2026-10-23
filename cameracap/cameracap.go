// Package cameracap captures webcam frames for the camera media lane.
package cameracap

import (
	"errors"

	"github.com/medeirosvictor/hermec/video"
)

const (
	Width  = 640
	Height = 360
	FPS    = 15
)

var ErrUnsupported = errors.New("cameracap: camera capture is not supported on this platform")

type Device struct {
	ID   string
	Name string
}

type Source interface {
	Next(*video.Frame) error
	Close() error
}
