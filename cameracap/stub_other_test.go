//go:build !windows

package cameracap

import (
	"errors"
	"testing"
)

func TestUnsupportedPlatformContract(t *testing.T) {
	if Supported {
		t.Fatal("stub platform reports camera support")
	}
	if _, err := Devices(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Devices = %v, want ErrUnsupported", err)
	}
	if _, err := Open(""); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Open = %v, want ErrUnsupported", err)
	}
}
