package dedicated

import (
	"errors"
	"io"
	"path/filepath"
)

// ErrReadinessLocked means another running source owns the readiness
// journal. A lock file may remain after a crash; only the OS lock is authority.
var ErrReadinessLocked = errors.New("server: dedicated readiness attestation is in use; stop the running source service before resetting it")

// AcquireReadinessLock exclusively owns the source readiness journal
// for a service lifetime or an offline maintenance operation.
func AcquireReadinessLock(transitionPath string) (io.Closer, error) {
	if transitionPath == "" {
		return nil, nil
	}
	return acquireReadinessLock(filepath.Join(filepath.Dir(transitionPath), ReadinessFile+".lock"))
}
