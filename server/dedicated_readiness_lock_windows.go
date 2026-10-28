//go:build windows

package server

import (
	"errors"

	"golang.org/x/sys/windows"
)

type dedicatedReadinessLock struct{ handle windows.Handle }

func acquireDedicatedReadinessLock(path string) (*dedicatedReadinessLock, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	l := &dedicatedReadinessLock{handle: h}
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		_ = windows.CloseHandle(h)
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrDedicatedReadinessLocked
		}
		return nil, err
	}
	return l, nil
}

func (l *dedicatedReadinessLock) Close() error {
	if l.handle == windows.InvalidHandle {
		return nil
	}
	h := l.handle
	l.handle = windows.InvalidHandle
	if err := windows.UnlockFileEx(h, 0, 1, 0, &windows.Overlapped{}); err != nil {
		_ = windows.CloseHandle(h)
		return err
	}
	return windows.CloseHandle(h)
}
