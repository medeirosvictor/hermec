package update

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fakeExe(t *testing.T) string {
	t.Helper()
	// Resolve the temp dir: Apply resolves symlinks (macOS /var -> /private/var).
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "hermec.exe")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := executable
	executable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executable = old })
	return exe
}

func read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func sumOf(s string) [32]byte { return sha256.Sum256([]byte(s)) }

// setOS runs a test under the Windows (rename twice) or POSIX (rename over)
// swap, whatever the host is.
func setOS(t *testing.T, name string) {
	t.Helper()
	old := goos
	goos = name
	t.Cleanup(func() { goos = old })
}

var swapModes = []string{"windows", "linux"}

func TestApplySwaps(t *testing.T) {
	for _, mode := range swapModes {
		t.Run(mode, func(t *testing.T) {
			setOS(t, mode)
			exe := fakeExe(t)
			os.WriteFile(exe+".new", []byte("NEW"), 0o755)
			os.WriteFile(exe+".old", []byte("STALE"), 0o755)
			got, err := Apply(sumOf("NEW"))
			if err != nil {
				t.Fatal(err)
			}
			if got != exe {
				t.Fatalf("Apply returned %q, want %q", got, exe)
			}
			if read(exe) != "NEW" || read(exe+".old") != "OLD" {
				t.Fatalf("exe=%q old=%q", read(exe), read(exe+".old"))
			}
			if _, err := os.Stat(exe + ".new"); err == nil {
				t.Fatal(".new should be consumed")
			}
		})
	}
}

func TestApplyWithoutStagedChangesNothing(t *testing.T) {
	exe := fakeExe(t)
	if _, err := Apply(sumOf("NEW")); err == nil {
		t.Fatal("Apply with nothing staged succeeded")
	}
	if read(exe) != "OLD" {
		t.Fatal("exe changed")
	}
}

// A staged file that is not the one Download verified is refused and removed.
func TestApplyRefusesChangedStagedFile(t *testing.T) {
	for _, mode := range swapModes {
		t.Run(mode, func(t *testing.T) {
			setOS(t, mode)
			exe := fakeExe(t)
			os.WriteFile(exe+".new", []byte("EVIL"), 0o755)
			if _, err := Apply(sumOf("NEW")); err == nil {
				t.Fatal("Apply installed a staged file that differs from the verified hash")
			}
			if read(exe) != "OLD" {
				t.Fatalf("exe = %q", read(exe))
			}
			if _, err := os.Stat(exe + ".new"); err == nil {
				t.Error("the mismatching staged file was kept")
			}
		})
	}
}

// A stale <exe>.old that cannot be replaced (a non-empty directory) stops the
// swap before anything changes; the exe and the staged file must be untouched.
func TestApplyFailureLeavesEverythingInPlace(t *testing.T) {
	for _, mode := range swapModes {
		t.Run(mode, func(t *testing.T) {
			setOS(t, mode)
			exe := fakeExe(t)
			os.WriteFile(exe+".new", []byte("NEW"), 0o755)
			os.Mkdir(exe+".old", 0o755)
			os.WriteFile(filepath.Join(exe+".old", "keep"), nil, 0o644)
			if _, err := Apply(sumOf("NEW")); err == nil {
				t.Fatal("Apply succeeded over a non-empty directory")
			}
			if read(exe) != "OLD" || read(exe+".new") != "NEW" {
				t.Fatalf("exe=%q new=%q", read(exe), read(exe+".new"))
			}
		})
	}
}

// When installing the staged file fails after the old binary was moved aside,
// the old binary is put back.
func TestApplyRollsBackSecondRename(t *testing.T) {
	setOS(t, "windows")
	exe := fakeExe(t)
	os.WriteFile(exe+".new", []byte("NEW"), 0o755)
	real := rename
	defer func() { rename = real }()
	rename = func(from, to string) error {
		if from == exe+".new" {
			return errors.New("boom")
		}
		return real(from, to)
	}
	if _, err := Apply(sumOf("NEW")); err == nil {
		t.Fatal("Apply succeeded")
	}
	if read(exe) != "OLD" || read(exe+".new") != "NEW" {
		t.Fatalf("exe=%q new=%q", read(exe), read(exe+".new"))
	}
}

// If the rollback fails too the executable is gone from its path; the error
// says so and where the previous binary is.
func TestApplyBothRenamesFailReportsRecovery(t *testing.T) {
	setOS(t, "windows")
	exe := fakeExe(t)
	os.WriteFile(exe+".new", []byte("NEW"), 0o755)
	real := rename
	defer func() { rename = real }()
	rename = func(from, to string) error {
		if from == exe+".new" || from == exe+".old" {
			return errors.New("boom")
		}
		return real(from, to)
	}
	_, err := Apply(sumOf("NEW"))
	var me *MissingError
	if !errors.As(err, &me) {
		t.Fatalf("err = %v, want *MissingError", err)
	}
	if me.Exe != exe || me.Old != exe+".old" || read(exe+".old") != "OLD" {
		t.Fatalf("MissingError %+v, old=%q", me, read(exe+".old"))
	}
}

// On the POSIX swap the new binary goes over the exe in one rename, with no
// moment where the path is empty, and the previous one is kept as .old.
func TestApplyPosixRenamesOverInOneStep(t *testing.T) {
	setOS(t, "linux")
	exe := fakeExe(t)
	os.WriteFile(exe+".new", []byte("NEW"), 0o755)
	real := rename
	defer func() { rename = real }()
	var calls [][2]string
	rename = func(from, to string) error {
		calls = append(calls, [2]string{from, to})
		if _, err := os.Stat(exe); err != nil {
			t.Errorf("no executable at %s during the swap: %v", exe, err)
		}
		return real(from, to)
	}
	if _, err := Apply(sumOf("NEW")); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != [2]string{exe + ".new", exe} {
		t.Fatalf("renames = %v", calls)
	}
	if read(exe) != "NEW" || read(exe+".old") != "OLD" {
		t.Fatalf("exe=%q old=%q", read(exe), read(exe+".old"))
	}
}

// Relaunch starts the path Apply installed, even when the running executable
// now reports another one (on Linux the replaced file reads back as
// "<path> (deleted)").
func TestRelaunchUsesTheAppliedPath(t *testing.T) {
	setOS(t, "linux")
	exe := fakeExe(t)
	os.WriteFile(exe+".new", []byte("NEW"), 0o755)
	path, err := Apply(sumOf("NEW"))
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "hermec (deleted)")
	executable = func() (string, error) { return other, nil }
	var started string
	realStart := start
	defer func() { start = realStart }()
	start = func(p string, _ []string) error { started = p; return nil }
	if err := Relaunch(path); err != nil {
		t.Fatal(err)
	}
	if started != exe {
		t.Fatalf("relaunched %q, want %q", started, exe)
	}
}

// Cleanup drops a staged-but-never-installed file and half-written downloads,
// but keeps .old; Started drops .old.
func TestCleanupAndStarted(t *testing.T) {
	exe := fakeExe(t)
	part := filepath.Join(filepath.Dir(exe), ".hermec-123.part")
	other := filepath.Join(filepath.Dir(exe), "keep.txt")
	for _, p := range []string{exe + ".new", exe + ".old", part, other} {
		os.WriteFile(p, []byte("x"), 0o755)
	}
	Cleanup()
	for _, p := range []string{exe + ".new", part} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived Cleanup", p)
		}
	}
	if read(exe+".old") != "x" {
		t.Error("Cleanup removed .old; only Started may, after a successful start")
	}
	if read(exe) != "OLD" || read(other) != "x" {
		t.Fatal("Cleanup touched something else")
	}
	Started()
	if _, err := os.Stat(exe + ".old"); err == nil {
		t.Error(".old survived Started")
	}
	if read(exe) != "OLD" {
		t.Fatal("Started touched the exe")
	}
}

// While another process holds the update lock (it is downloading or
// installing) neither Cleanup nor Started removes anything.
func TestCleanupAndStartedRespectTheLock(t *testing.T) {
	exe := fakeExe(t)
	part := filepath.Join(filepath.Dir(exe), ".hermec-123.part")
	for _, p := range []string{exe + ".new", exe + ".old", part} {
		os.WriteFile(p, []byte("x"), 0o755)
	}
	l, err := lockFile(lockPath(exe))
	if err != nil {
		t.Fatal(err)
	}
	Cleanup()
	Started()
	l.Close()
	for _, p := range []string{exe + ".new", exe + ".old", part} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed while the lock was held", p)
		}
	}
}

func TestApplyBusy(t *testing.T) {
	exe := fakeExe(t)
	os.WriteFile(exe+".new", []byte("NEW"), 0o755)
	l, err := lockFile(lockPath(exe))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := Apply(sumOf("NEW")); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if read(exe) != "OLD" {
		t.Fatal("exe changed while locked")
	}
}
