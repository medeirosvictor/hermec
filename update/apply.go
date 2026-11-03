package update

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// executable is a var so tests can point the updater at a fake binary.
var executable = os.Executable

// rename is a var so tests can fail one step of the swap.
var rename = os.Rename

// start launches path with args without waiting for it; a var so tests can
// observe which binary a relaunch would run.
var start = func(path string, args []string) error {
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// ErrBusy means another hermec process holds the update lock, so it is
// downloading or installing an update right now.
var ErrBusy = errors.New("update: another hermec is updating")

// MissingError means the swap failed half way and the executable is gone from
// its path: the previous binary is at Old and has to be renamed back by hand.
type MissingError struct {
	Exe, Old string
	Err      error
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("update: %s is missing; the previous version is at %s (rename it back): %v", e.Exe, e.Old, e.Err)
}

func (e *MissingError) Unwrap() error { return e.Err }

// installPath is the running executable with symlinks resolved, so the new
// binary is staged next to the real file.
func installPath() (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func lockPath(exe string) string { return exe + ".lock" }

// hashFile is the SHA-256 of the file at path, which must be a regular file.
func hashFile(path string) ([32]byte, error) {
	var sum [32]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return sum, fmt.Errorf("update: %s is not a regular file", path)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// Apply swaps the staged <exe>.new in for the running executable and returns
// the path it installed to, which is where Relaunch must start the new build
// (on Linux the running file's own name can read back as "... (deleted)", so
// the path is resolved once, here, and never again). want is the SHA-256 that
// Download reported for the extracted binary; the staged file is hashed again
// first and refused if it differs. The previous binary is kept as <exe>.old
// until the new build has started (see Started).
func Apply(want [32]byte) (string, error) {
	exe, err := installPath()
	if err != nil {
		return "", err
	}
	l, err := lockFile(lockPath(exe))
	if err != nil {
		return "", notWritable(err)
	}
	defer l.Close()
	staged, old := exe+".new", exe+".old"
	got, err := hashFile(staged)
	if err != nil {
		return "", err
	}
	if got != want {
		os.Remove(staged)
		return "", errors.New("update: the staged file changed after it was verified")
	}
	if goos == "windows" {
		err = swapRename(exe, staged, old)
	} else {
		err = swapOver(exe, staged, old)
	}
	if err != nil {
		return "", err
	}
	return exe, nil
}

// swapRename is the Windows swap: a running executable cannot be replaced but
// can be renamed. There is a moment with no file at exe; if both renames fail
// the error says where the previous binary is.
func swapRename(exe, staged, old string) error {
	os.Remove(old)
	if err := rename(exe, old); err != nil {
		return err
	}
	if err := rename(staged, exe); err != nil {
		if rerr := rename(old, exe); rerr != nil {
			return &MissingError{Exe: exe, Old: old, Err: fmt.Errorf("%w (and restoring the old binary failed: %v)", err, rerr)}
		}
		return err
	}
	return nil
}

// swapOver is the POSIX swap: keep the previous binary as old (a hard link, or
// a copy where linking is not possible), then rename the new one over exe in a
// single step, so there is never a moment without an executable at exe.
func swapOver(exe, staged, old string) error {
	os.Remove(old)
	if err := os.Link(exe, old); err != nil {
		if cerr := copyFile(exe, old); cerr != nil {
			return cerr
		}
	}
	return rename(staged, exe)
}

func copyFile(from, to string) (err error) {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(to)
		}
	}()
	_, err = io.Copy(out, in)
	return err
}

// Relaunch starts the executable at path (what Apply returned) with this
// process's arguments and does not wait for it.
func Relaunch(path string) error {
	return start(path, os.Args[1:])
}

// withLockIfFree runs f holding the update lock, or does nothing when another
// process holds it (or the lock cannot be taken at all).
func withLockIfFree(f func(exe string)) {
	exe, err := installPath()
	if err != nil {
		return
	}
	l, err := lockFile(lockPath(exe))
	if err != nil {
		return
	}
	defer l.Close()
	f(exe)
}

// Cleanup removes the leftovers of an update that was staged but never
// installed: <exe>.new and half-written downloads. It does nothing while
// another process holds the update lock, so a download in progress elsewhere
// is not pulled out from under it. The previous binary (<exe>.old) is kept;
// Started removes it.
func Cleanup() {
	withLockIfFree(func(exe string) {
		os.Remove(exe + ".new")
		parts, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".hermec-*.part"))
		for _, p := range parts {
			os.Remove(p)
		}
	})
}

// Started is called once the running build has come up (the UI drew its first
// frame). Only now is the previous binary, kept as <exe>.old for recovery,
// removed. On Windows it may still be locked by the exiting process; the next
// start tries again.
func Started() {
	withLockIfFree(func(exe string) { os.Remove(exe + ".old") })
}
