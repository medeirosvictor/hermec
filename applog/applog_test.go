package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 19, 18, 30, 5, 0, time.Local)

func logsIn(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "hermec-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestOpenCreatesNamedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	f, path, err := Open(dir, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := filepath.Base(path); got != "hermec-2026-10-19_18-30-05.log" {
		t.Fatalf("name = %q", got)
	}
	if !regexp.MustCompile(`^hermec-\d{4}-\d\d-\d\d_\d\d-\d\d-\d\d\.log$`).MatchString(filepath.Base(path)) {
		t.Fatal("name shape")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestOpenKeepsNewestTen(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 12; i++ {
		n := fmt.Sprintf("hermec-2026-10-%02d_10-00-00.log", i+1)
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f, path, err := Open(dir, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got := logsIn(t, dir); len(got) != 10 {
		t.Fatalf("%d files remain, want 10", len(got))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("new file missing")
	}
	for _, old := range []string{"01", "02", "03"} {
		if _, err := os.Stat(filepath.Join(dir, "hermec-2026-10-"+old+"_10-00-00.log")); err == nil {
			t.Fatalf("oldest %s not pruned", old)
		}
	}
}

func TestOpenIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	others := []string{"notes.txt", "hermec.log", "other-2026.log"}
	for _, n := range others {
		_ = os.WriteFile(filepath.Join(dir, n), nil, 0o600)
	}
	for i := 0; i < 12; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("hermec-2026-10-%02d_10-00-00.log", i+1)), nil, 0o600)
	}
	f, _, err := Open(dir, now, 3)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	for _, n := range others {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Fatalf("%s was removed", n)
		}
	}
	if len(logsIn(t, dir)) != 3 {
		t.Fatal("want 3 session logs")
	}
}

func TestOpenFallsBackWhenDirUnwritable(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocker, []byte("x"), 0o600)
	f, path, err := Open(filepath.Join(blocker, "logs"), now, 10)
	if err == nil {
		f.Close()
		t.Fatal("want an error")
	}
	if f != nil || path != "" {
		t.Fatal("want nil file and empty path on error")
	}
}

func TestDir(t *testing.T) {
	if got, want := Dir("cfg"), filepath.Join("cfg", "hermec", "logs"); got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
}
