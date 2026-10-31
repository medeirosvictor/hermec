//go:build !windows

package dedicated

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type readinessLock struct{ file *os.File }

func acquireReadinessLock(path string) (*readinessLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrReadinessLocked
		}
		return nil, err
	}
	return &readinessLock{file: f}, nil
}

func (l *readinessLock) Close() error {
	if l.file == nil {
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
