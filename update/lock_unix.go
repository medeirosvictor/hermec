//go:build !windows

package update

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// fileLock is an exclusive OS lock on a file. The OS drops it when the process
// dies, so a crash never leaves a stale lock behind.
type fileLock struct{ file *os.File }

func lockFile(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return &fileLock{file: f}, nil
}

func (l *fileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	if err := unix.Flock(int(f.Fd()), unix.LOCK_UN); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
