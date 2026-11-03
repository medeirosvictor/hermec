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

// serverScript is a stand-in hermec-server that reports version v.
func serverScript(v string) string {
	return "#!/bin/sh\n[ \"$1\" = -version ] && echo 'hermec-server " + v + "'\nexit 0\n"
}

// signedUpdate is a fixture for update.sh in its default, verifying mode.
type signedUpdate struct {
	t         *testing.T
	dir       string
	tool, pub string
	seed      string
	script    string
	systemctl string
	sums      string
	archive   string
}

func newSignedUpdate(t *testing.T) *signedUpdate {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("dedicated lifecycle scripts require POSIX shell")
	}
	if os.Geteuid() == 0 {
		t.Skip("update.sh takes its systemd-run branch as root")
	}
	s := &signedUpdate{t: t, dir: t.TempDir()}
	s.tool = filepath.Join(s.dir, "hermec-release")
	if out, err := exec.Command("go", "build", "-o", s.tool, "../hermec-release").CombinedOutput(); err != nil {
		t.Fatalf("build hermec-release: %v\n%s", err, out)
	}
	keyFile := filepath.Join(s.dir, "release.key")
	out, err := exec.Command(s.tool, "keygen", "-out", keyFile).Output()
	if err != nil {
		t.Fatal(err)
	}
	s.pub = strings.TrimSpace(string(out))
	s.seed = strings.TrimSpace(string(mustRead(t, keyFile)))
	s.systemctl = filepath.Join(s.dir, "systemctl")
	if err := os.WriteFile(s.systemctl, []byte("#!/bin/sh\n[ \"$1\" = is-active ] && exit 1\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.script, err = filepath.Abs(filepath.Join("..", "..", "packaging", "dedicated", "update.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// release writes an archive holding entries (name -> body) and SHA256SUMS.txt
// signed for tag.
func (s *signedUpdate) release(tag string, entries map[string]string, order ...string) {
	s.t.Helper()
	s.archive = filepath.Join(s.dir, "hermec-server-linux-amd64.tar.gz")
	writeArchive(s.t, s.archive, entries, order)
	sum := sha256.Sum256(mustRead(s.t, s.archive))
	s.sums = filepath.Join(s.dir, "SHA256SUMS.txt")
	if err := os.WriteFile(s.sums, []byte(hex.EncodeToString(sum[:])+"  hermec-server-linux-amd64.tar.gz\n"), 0o644); err != nil {
		s.t.Fatal(err)
	}
	s.sign(tag, s.seed)
}

func (s *signedUpdate) sign(tag, seed string) {
	s.t.Helper()
	sign := exec.Command(s.tool, "sign", "-tag", tag, "-key-env", "TEST_KEY", s.sums)
	sign.Env = append(os.Environ(), "TEST_KEY="+seed)
	if out, err := sign.CombinedOutput(); err != nil {
		s.t.Fatalf("sign: %v\n%s", err, out)
	}
}

// run runs `update.sh update` in a fresh install dir; installed, when set, is
// the script already installed at the binary's path. It reports whether a
// server binary ended up installed.
func (s *signedUpdate) run(name, installed string, env ...string) (bool, string) {
	s.t.Helper()
	bin := filepath.Join(s.dir, name, "bin", "hermec-server")
	if installed != "" {
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			s.t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte(installed), 0o755); err != nil {
			s.t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", s.script, "update")
	cmd.Env = append(os.Environ(), "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_BIN="+bin,
		"HERMEC_PREVIOUS="+filepath.Join(s.dir, name, "previous"), "HERMEC_SYSTEMCTL="+s.systemctl,
		"HERMEC_SUMS="+s.sums, "HERMEC_ARCHIVE="+s.archive, "HERMEC_TAG=v2.0.0",
		"HERMEC_TEST_RELEASE_TOOL="+s.tool, "HERMEC_TEST_RELEASE_PUB="+s.pub)
	cmd.Env = append(cmd.Env, env...)
	o, _ := cmd.CombinedOutput()
	got, err := os.ReadFile(bin)
	return err == nil && string(got) != installed, string(o)
}

// Verification is the default: update.sh installs only the hermec-server of a
// release archive listed in checksums whose signature, bound to HERMEC_TAG,
// verifies.
func TestUpdateVerifiesSignedRelease(t *testing.T) {
	s := newSignedUpdate(t)
	s.release("v2.0.0", map[string]string{"./hermec-server": serverScript("v2.0.0")}, "./hermec-server")

	if ok, out := s.run("good", ""); !ok || !strings.Contains(out, "signature ok for tag v2.0.0") {
		t.Fatalf("good release not installed:\n%s", out)
	}
	if ok, out := s.run("upgrade", serverScript("v1.9.0")); !ok {
		t.Fatalf("upgrade over an older install refused:\n%s", out)
	}

	other, err := exec.Command(s.tool, "keygen", "-out", filepath.Join(s.dir, "other.key")).Output()
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.run("wrongkey", "", "HERMEC_TEST_RELEASE_PUB="+strings.TrimSpace(string(other))); ok {
		t.Error("release signed by another key was installed")
	}
	// The signature is for v2.0.0: claiming another tag must not verify.
	if ok, _ := s.run("wrongtag", "", "HERMEC_TAG=v2.0.1"); ok {
		t.Error("a signature was accepted for a different tag")
	}
	if ok, _ := s.run("badtag", "", "HERMEC_TAG=latest"); ok {
		t.Error("a malformed tag was accepted")
	}
	// The archive on disk is what is checked: swap it for another one.
	good := mustRead(t, s.archive)
	writeArchive(t, s.archive, map[string]string{"./hermec-server": serverScript("v2.0.0") + "# extra\n"}, []string{"./hermec-server"})
	if ok, _ := s.run("tampered", ""); ok {
		t.Error("an archive that is not in the signed sums was installed")
	}
	if err := os.WriteFile(s.archive, good, 0o644); err != nil {
		t.Fatal(err)
	}
	// A signed release must install the version that was signed.
	s.release("v2.0.0", map[string]string{"./hermec-server": serverScript("v1.0.0")}, "./hermec-server")
	if ok, _ := s.run("wrongversion", ""); ok {
		t.Error("a binary reporting another version than the signed tag was installed")
	}
	// The binary must sit at the archive root, exactly once.
	for name, entries := range map[string][]string{
		"nested":    {"./sub/hermec-server"},
		"duplicate": {"./hermec-server", "hermec-server"},
	} {
		bodies := map[string]string{}
		for _, e := range entries {
			bodies[e] = serverScript("v2.0.0")
		}
		s.release("v2.0.0", bodies, entries...)
		if ok, _ := s.run(name, ""); ok {
			t.Errorf("%s: installed", name)
		}
	}
	s.release("v2.0.0", map[string]string{"./hermec-server": serverScript("v2.0.0")}, "./hermec-server")
	if err := os.WriteFile(s.sums, append(mustRead(t, s.sums), []byte(strings.Repeat("0", 64)+"  extra\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.run("badsums", ""); ok {
		t.Error("tampered checksums were accepted")
	}
}

// A validly signed older release must not replace a newer install.
func TestUpdateRefusesDowngrade(t *testing.T) {
	s := newSignedUpdate(t)
	s.release("v2.0.0", map[string]string{"./hermec-server": serverScript("v2.0.0")}, "./hermec-server")
	for name, installed := range map[string]string{
		"newer":    serverScript("v3.0.0"),
		"same":     serverScript("v2.0.0"),
		"patch":    serverScript("v2.0.1"),
		"dev":      serverScript("dev"),
		"unparsed": serverScript("garbage"),
	} {
		if ok, out := s.run(name, installed); ok || !strings.Contains(out, "HERMEC_ALLOW_DOWNGRADE=1") {
			t.Errorf("%s: installed=%v\n%s", name, ok, out)
		}
	}
	if ok, out := s.run("allowed", serverScript("v3.0.0"), "HERMEC_ALLOW_DOWNGRADE=1"); !ok || !strings.Contains(out, "warning") {
		t.Errorf("escape hatch did not install:\n%s", out)
	}
}

// Without the tool and the inputs update.sh refuses; HERMEC_UNSIGNED=1 is the
// only way to skip verification and it says so loudly.
func TestUpdateIsVerifiedByDefault(t *testing.T) {
	s := newSignedUpdate(t)
	s.release("v2.0.0", map[string]string{"./hermec-server": serverScript("v2.0.0")}, "./hermec-server")

	for name, env := range map[string][]string{
		"no tag":     {"HERMEC_TAG="},
		"no sums":    {"HERMEC_SUMS="},
		"no archive": {"HERMEC_ARCHIVE="},
		"extra arg":  nil,
	} {
		bin := filepath.Join(s.dir, name, "bin", "hermec-server")
		args := []string{s.script, "update"}
		if name == "extra arg" {
			args = append(args, filepath.Join(s.dir, "whatever"))
		}
		cmd := exec.Command("sh", args...)
		cmd.Env = append(os.Environ(), "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_BIN="+bin,
			"HERMEC_SUMS="+s.sums, "HERMEC_ARCHIVE="+s.archive, "HERMEC_TAG=v2.0.0",
			"HERMEC_TEST_RELEASE_TOOL="+s.tool, "HERMEC_TEST_RELEASE_PUB="+s.pub,
			"HERMEC_PREVIOUS="+filepath.Join(s.dir, name, "previous"), "HERMEC_SYSTEMCTL="+s.systemctl)
		cmd.Env = append(cmd.Env, env...)
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Errorf("%s: update succeeded:\n%s", name, out)
		}
		if _, err := os.Stat(bin); err == nil {
			t.Errorf("%s: installed", name)
		}
	}

	// The default verifier is the root-owned installed copy, never anything
	// found on PATH or next to the archive.
	if _, err := os.Stat("/usr/local/lib/hermec/hermec-release"); err != nil {
		bin := filepath.Join(s.dir, "notool", "bin", "hermec-server")
		cmd := exec.Command("sh", s.script, "update")
		cmd.Env = append(os.Environ(), "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_BIN="+bin,
			"HERMEC_SUMS="+s.sums, "HERMEC_ARCHIVE="+s.archive, "HERMEC_TAG=v2.0.0",
			"PATH="+filepath.Dir(s.tool)+string(os.PathListSeparator)+os.Getenv("PATH"),
			"HERMEC_PREVIOUS="+filepath.Join(s.dir, "notool", "previous"), "HERMEC_SYSTEMCTL="+s.systemctl)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "/usr/local/lib/hermec/hermec-release") {
			t.Errorf("update did not insist on the installed verifier: %v\n%s", err, out)
		}
		if _, err := os.Stat(bin); err == nil {
			t.Error("installed without the installed verifier")
		}
	}

	candidate := filepath.Join(s.dir, "candidate")
	if err := os.WriteFile(candidate, []byte(serverScript("v9.9.9")), 0o755); err != nil {
		t.Fatal(err)
	}
	ubin := filepath.Join(s.dir, "unsigned", "bin", "hermec-server")
	cmd := exec.Command("sh", s.script, "update", candidate)
	cmd.Env = append(os.Environ(), "HERMEC_UNSIGNED=1", "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_BIN="+ubin,
		"HERMEC_PREVIOUS="+filepath.Join(s.dir, "unsigned", "previous"), "HERMEC_SYSTEMCTL="+s.systemctl)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "WARNING") || !strings.Contains(string(out), "NO signature") {
		t.Fatalf("unsigned update: %v\n%s", err, out)
	}
	if got, err := os.ReadFile(ubin); err != nil || string(got) != serverScript("v9.9.9") {
		t.Fatalf("unsigned update did not install: %v", err)
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

// writeArchive writes a .tar.gz with the given regular files in order.
func writeArchive(t *testing.T, path string, entries map[string]string, order []string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, name := range order {
		body := entries[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
}
