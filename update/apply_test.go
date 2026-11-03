package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fakeExe(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "hermec.exe")
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

func TestApplySwaps(t *testing.T) {
	exe := fakeExe(t)
	os.WriteFile(exe+".new", []byte("NEW"), 0o755)
	os.WriteFile(exe+".old", []byte("STALE"), 0o755)
	if err := Apply(); err != nil {
		t.Fatal(err)
	}
	if read(exe) != "NEW" || read(exe+".old") != "OLD" {
		t.Fatalf("exe=%q old=%q", read(exe), read(exe+".old"))
	}
	if _, err := os.Stat(exe + ".new"); err == nil {
		t.Fatal(".new should be consumed")
	}
}

func TestApplyWithoutStagedChangesNothing(t *testing.T) {
	exe := fakeExe(t)
	if err := Apply(); err == nil {
		t.Fatal("Apply with nothing staged succeeded")
	}
	if read(exe) != "OLD" {
		t.Fatal("exe changed")
	}
}

// A stale <exe>.old that cannot be replaced (a non-empty directory) makes the
// first rename fail; the exe and the staged file must be untouched.
func TestApplyFailureLeavesEverythingInPlace(t *testing.T) {
	exe := fakeExe(t)
	os.WriteFile(exe+".new", []byte("NEW"), 0o755)
	os.Mkdir(exe+".old", 0o755)
	os.WriteFile(filepath.Join(exe+".old", "keep"), nil, 0o644)
	if err := Apply(); err == nil {
		t.Fatal("Apply succeeded over a non-empty directory")
	}
	if read(exe) != "OLD" || read(exe+".new") != "NEW" {
		t.Fatalf("exe=%q new=%q", read(exe), read(exe+".new"))
	}
}

// When installing the staged file fails after the old binary was moved aside,
// the old binary is put back.
func TestApplyRollsBackSecondRename(t *testing.T) {
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
	if err := Apply(); err == nil {
		t.Fatal("Apply succeeded")
	}
	if read(exe) != "OLD" || read(exe+".new") != "NEW" {
		t.Fatalf("exe=%q new=%q", read(exe), read(exe+".new"))
	}
}

func TestCleanup(t *testing.T) {
	exe := fakeExe(t)
	os.WriteFile(exe+".new", []byte("x"), 0o755)
	os.WriteFile(exe+".old", []byte("x"), 0o755)
	Cleanup()
	for _, p := range []string{exe + ".new", exe + ".old"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived Cleanup", p)
		}
	}
	if read(exe) != "OLD" {
		t.Fatal("Cleanup touched the exe")
	}
}
