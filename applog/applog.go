// Package applog creates the per-run session log file.
package applog

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	prefix = "hermec-"
	suffix = ".log"
	stamp  = "2006-01-02_15-04-05"
)

// Dir is the log directory under a user config directory.
func Dir(configDir string) string { return filepath.Join(configDir, "hermec", "logs") }

// Open creates dir if needed, prunes it to keep-1 older hermec-*.log files
// (oldest first; the names sort by time) and creates the file for now.
func Open(dir string, now time.Time, keep int) (f *os.File, path string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	old, _ := filepath.Glob(filepath.Join(dir, prefix+"*"+suffix))
	sort.Strings(old)
	if keep < 1 {
		keep = 1
	}
	for len(old) > keep-1 {
		_ = os.Remove(old[0])
		old = old[1:]
	}
	path = filepath.Join(dir, prefix+now.Format(stamp)+suffix)
	f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, "", err
	}
	return f, path, nil
}
