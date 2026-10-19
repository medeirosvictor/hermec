//go:build !windows

package screencap

// Supported reports whether this platform has a capture backend.
const Supported = false

type stub struct{}

// New returns a Source whose methods all fail with ErrUnsupported.
func New() Source { return stub{} }

func (stub) Monitors() ([]Monitor, error) { return nil, ErrUnsupported }
func (stub) Capture(Monitor, *BGRA) error { return ErrUnsupported }
func (stub) Close() error                 { return nil }
