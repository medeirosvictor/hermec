package server

import (
	"errors"
	"io"
	"path/filepath"
)

// ErrDedicatedReadinessLocked means another running source owns the readiness
// journal. A lock file may remain after a crash; only the OS lock is authority.
var ErrDedicatedReadinessLocked = errors.New("server: dedicated readiness attestation is in use; stop the running source service before resetting it")

// AcquireDedicatedReadinessLock exclusively owns the source readiness journal
// for a service lifetime or an offline maintenance operation.
func AcquireDedicatedReadinessLock(transitionPath string) (io.Closer, error) {
	if transitionPath == "" {
		return nil, nil
	}
	return acquireDedicatedReadinessLock(filepath.Join(filepath.Dir(transitionPath), DedicatedReadinessFile+".lock"))
}
