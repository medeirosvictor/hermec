//go:build windows

package update

import (
	"errors"

	"golang.org/x/sys/windows"
)

// fileLock is an exclusive OS lock on a file. The OS drops it when the process
// dies, so a crash never leaves a stale lock behind.
type fileLock struct{ handle windows.Handle }

func lockFile(path string) (*fileLock, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	l := &fileLock{handle: h}
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		_ = windows.CloseHandle(h)
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return l, nil
}

func (l *fileLock) Close() error {
	if l == nil || l.handle == 0 || l.handle == windows.InvalidHandle {
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
