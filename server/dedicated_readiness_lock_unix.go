//go:build !windows

package server

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type dedicatedReadinessLock struct{ file *os.File }

func acquireDedicatedReadinessLock(path string) (*dedicatedReadinessLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrDedicatedReadinessLocked
		}
		return nil, err
	}
	return &dedicatedReadinessLock{file: f}, nil
}

func (l *dedicatedReadinessLock) Close() error {
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
