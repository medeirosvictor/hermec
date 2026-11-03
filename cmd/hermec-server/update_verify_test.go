package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// With HERMEC_SUMS set, update.sh installs only a candidate that is the
// hermec-server of a release archive listed in correctly signed checksums.
func TestUpdateVerifiesSignedRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dedicated lifecycle scripts require POSIX shell")
	}
	if os.Geteuid() == 0 {
		t.Skip("update.sh takes its systemd-run branch as root")
	}
	dir := t.TempDir()
	tool := filepath.Join(dir, "hermec-release")
	if out, err := exec.Command("go", "build", "-o", tool, "../hermec-release").CombinedOutput(); err != nil {
		t.Fatalf("build hermec-release: %v\n%s", err, out)
	}
	keyFile := filepath.Join(dir, "release.key")
	out, err := exec.Command(tool, "keygen", "-out", keyFile).Output()
	if err != nil {
		t.Fatal(err)
	}
	pub := strings.TrimSpace(string(out))
	seed, _ := os.ReadFile(keyFile)

	body := "#!/bin/sh\n[ \"$1\" = -version ] && echo 'hermec-server test'\nexit 0\n"
	archive := filepath.Join(dir, "hermec-server-linux-amd64.tar.gz")
	writeArchive(t, archive, body)
	sum := sha256.Sum256(mustRead(t, archive))
	sums := filepath.Join(dir, "SHA256SUMS.txt")
	if err := os.WriteFile(sums, []byte(hex.EncodeToString(sum[:])+"  hermec-server-linux-amd64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sign := exec.Command(tool, "sign", "-key-env", "TEST_KEY", sums)
	sign.Env = append(os.Environ(), "TEST_KEY="+strings.TrimSpace(string(seed)))
	if out, err := sign.CombinedOutput(); err != nil {
		t.Fatalf("sign: %v\n%s", err, out)
	}
	systemctl := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/bin/sh\n[ \"$1\" = is-active ] && exit 1\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "packaging", "dedicated", "update.sh"))
	if err != nil {
		t.Fatal(err)
	}

	run := func(name, candidateBody string, env ...string) (installed bool, output string) {
		t.Helper()
		bin := filepath.Join(dir, name, "bin", "hermec-server")
		candidate := filepath.Join(dir, name+"-candidate")
		if err := os.WriteFile(candidate, []byte(candidateBody), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", script, "update", candidate)
		cmd.Env = append(os.Environ(), "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_BIN="+bin,
			"HERMEC_PREVIOUS="+filepath.Join(dir, name, "previous"), "HERMEC_SYSTEMCTL="+systemctl,
			"HERMEC_SUMS="+sums, "HERMEC_ARCHIVE="+archive, "HERMEC_RELEASE_TOOL="+tool, "HERMEC_RELEASE_PUB="+pub)
		cmd.Env = append(cmd.Env, env...)
		o, _ := cmd.CombinedOutput()
		_, err := os.Stat(bin)
		return err == nil, string(o)
	}

	if ok, out := run("good", body); !ok {
		t.Fatalf("good release not installed:\n%s", out)
	}
	if ok, _ := run("tampered", body+"# extra\n"); ok {
		t.Error("candidate differing from the archive was installed")
	}
	other := exec.Command(tool, "keygen", "-out", filepath.Join(dir, "other.key"))
	otherPub, err := other.Output()
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := run("wrongkey", body, "HERMEC_RELEASE_PUB="+strings.TrimSpace(string(otherPub))); ok {
		t.Error("release signed by another key was installed")
	}
	if err := os.WriteFile(sums, append(mustRead(t, sums), []byte(strings.Repeat("0", 64)+"  extra\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := run("badsums", body); ok {
		t.Error("tampered checksums were accepted")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeArchive(t *testing.T, path, serverBody string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "./hermec-server", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(serverBody))}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte(serverBody))
	tw.Close()
	gz.Close()
}
