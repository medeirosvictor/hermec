// Package fsutil holds small filesystem helpers shared across hermec.
package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
)

// WriteFileAtomic replaces path with data through a synced temp file in the
// same directory, so a crash leaves either the old or the new contents. The
// parent directory is synced too, so the rename itself is durable; where the
// platform cannot sync a directory (Windows) that step is skipped.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(perm)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		if runtime.GOOS == "windows" {
			return nil
		}
		return err
	}
	err = d.Sync()
	_ = d.Close()
	if err != nil && runtime.GOOS == "windows" {
		return nil
	}
	return err
}
