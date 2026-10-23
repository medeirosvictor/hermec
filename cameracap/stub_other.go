//go:build !windows

package cameracap

const Supported = false

func Devices() ([]Device, error)  { return nil, ErrUnsupported }
func Open(string) (Source, error) { return nil, ErrUnsupported }
