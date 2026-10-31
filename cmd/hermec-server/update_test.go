package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The file that passed validation is the file installed: replacing the
// caller's candidate while it is being validated has no effect.
func TestUpdateInstallsTheValidatedCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dedicated lifecycle scripts require POSIX shell")
	}
	if os.Geteuid() == 0 {
		t.Skip("update.sh takes its systemd-run branch as root")
	}
	dir := t.TempDir()
	bin, previous := filepath.Join(dir, "bin", "hermec-server"), filepath.Join(dir, "lib", "previous")
	candidate := filepath.Join(dir, "new")
	body := `#!/bin/sh
if [ "$1" = -version ]; then
  printf '#!/bin/sh\necho swapped\n' > "$HERMEC_TEST_SOURCE"
  echo 'hermec-server test'
fi
exit 0
`
	if err := os.WriteFile(candidate, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	systemctl := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/bin/sh\n[ \"$1\" = is-active ] && exit 1\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "packaging", "dedicated", "update.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, "update", candidate)
	cmd.Env = append(os.Environ(), "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_BIN="+bin, "HERMEC_PREVIOUS="+previous,
		"HERMEC_SYSTEMCTL="+systemctl, "HERMEC_TEST_SOURCE="+candidate)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update: %v\n%s", err, output)
	}
	if got, _ := os.ReadFile(candidate); !strings.Contains(string(got), "swapped") {
		t.Fatal("test did not replace the source during validation")
	}
	if got, err := os.ReadFile(bin); err != nil || string(got) != body {
		t.Fatalf("installed %q, %v; want the validated copy", got, err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(bin), ".hermec-server.candidate.*")); len(left) != 0 {
		t.Fatalf("staged copy left behind: %v", left)
	}
}
