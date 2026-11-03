package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// executable is a var so tests can point the updater at a fake binary.
var executable = os.Executable

// rename is a var so tests can fail one step of the swap.
var rename = os.Rename

// installPath is the running executable with symlinks resolved, so the new
// binary is staged next to the real file.
func installPath() (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// createStaged creates <exe>.new, empty and executable. A folder that refuses
// it is ErrNotWritable.
func createStaged(exe string) (*os.File, error) {
	p := exe + ".new"
	os.Remove(p)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotWritable, err)
	}
	return f, nil
}

// Apply swaps the staged <exe>.new in for the running executable, which is kept
// as <exe>.old (a running executable can be renamed, on Windows too). If the
// second rename fails the first is undone.
func Apply() error {
	exe, err := installPath()
	if err != nil {
		return err
	}
	staged, old := exe+".new", exe+".old"
	if _, err := os.Stat(staged); err != nil {
		return err
	}
	os.Remove(old)
	if err := rename(exe, old); err != nil {
		return err
	}
	if err := rename(staged, exe); err != nil {
		if rerr := rename(old, exe); rerr != nil {
			return fmt.Errorf("%w (and restoring the old binary failed: %v)", err, rerr)
		}
		return err
	}
	return nil
}

// Relaunch starts the (updated) executable with this process's arguments and
// does not wait for it.
func Relaunch() error {
	exe, err := installPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Cleanup removes the leftovers of an update: the previous binary and any
// staged one. Best effort; on Windows the old binary may still be locked by
// the exiting process, in which case the next start removes it.
func Cleanup() {
	exe, err := installPath()
	if err != nil {
		return
	}
	os.Remove(exe + ".old")
	os.Remove(exe + ".new")
}
