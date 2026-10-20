// Package screencap captures whole monitors as BGRA frames for screen share.
// Only Windows (GDI) is implemented; other platforms return ErrUnsupported.
package screencap

import (
	"errors"
	"fmt"
)

// ErrUnsupported is returned on platforms without a capture backend.
var ErrUnsupported = errors.New("screencap: screen capture is not supported on this platform")

// Monitor describes one display in desktop (virtual-screen) coordinates.
// Name is "screen N (WxH)" with N = Index+1.
type Monitor struct {
	Index      int
	Name       string
	X, Y, W, H int
}

// BGRA is a captured frame, 4 bytes per pixel (B, G, R, unused alpha). It is
// meant to be reused between captures: Pix is only reallocated when the
// monitor size grows.
type BGRA struct {
	W, H, Stride int
	Pix          []byte
}

// Source enumerates and captures monitors. It is not safe to Capture from
// several goroutines at once on different monitors without a Source each.
type Source interface {
	Monitors() ([]Monitor, error)
	Capture(m Monitor, dst *BGRA) error
	Close() error
}

// MonitorName formats the menu label for the monitor at index (0-based).
func MonitorName(index, w, h int) string {
	return fmt.Sprintf("screen %d (%d×%d)", index+1, w, h)
}
