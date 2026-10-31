package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/server/serverctl"
)

func TestUpdateAndRollbackPreserveState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dedicated lifecycle scripts require POSIX shell")
	}
	if os.Geteuid() == 0 {
		t.Skip("update.sh takes its systemd-run branch as root")
	}
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	result, err := serverctl.InitDedicated(state)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := server.LoadConfig(result.Config)
	if err != nil {
		t.Fatal(err)
	}
	// A loopback config makes the real candidate's -check independent of a
	// system trust store while still checking the generated persistent state.
	cfg.Addr, cfg.PublicMode, cfg.TLSCert, cfg.TLSKey = "127.0.0.1:7697", false, "", ""
	raw, err := cfg.MarshalTOML()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.Config, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	contents := make(map[string][]byte)
	for _, name := range []string{"hermec-server.key", "chain.jsonl", "history.jsonl"} {
		contents[name], err = os.ReadFile(filepath.Join(state, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	bin, previous := filepath.Join(dir, "bin", "hermec-server"), filepath.Join(dir, "lib", "previous")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	newer := filepath.Join(dir, "new")
	if output, err := exec.Command("go", "build", "-o", newer, ".").CombinedOutput(); err != nil {
		t.Fatalf("build real candidate: %v\n%s", err, output)
	}
	systemctl := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/bin/sh\n[ \"$1\" = is-active ] && exit 0\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "packaging", "dedicated", "update.sh"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("sh", append([]string{script}, args...)...)
		cmd.Env = append(os.Environ(), "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_BIN="+bin, "HERMEC_PREVIOUS="+previous, "HERMEC_STATE="+state, "HERMEC_SYSTEMCTL="+systemctl, "HERMEC_HEALTH_DELAY=0")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output)
		}
	}
	run("update", newer)
	run("rollback")
	for name, want := range contents {
		got, err := os.ReadFile(filepath.Join(state, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("state %s changed: %q, %v", name, got, err)
		}
	}
	got, err := os.ReadFile(bin)
	if err != nil || !bytes.Contains(got, []byte("echo old")) {
		t.Fatalf("rollback binary = %q, %v", got, err)
	}
}

func TestUpdateRejectsCandidateBeforeStoppingService(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dedicated lifecycle scripts require POSIX shell")
	}
	if os.Geteuid() == 0 {
		t.Skip("update.sh takes its systemd-run branch as root")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "systemctl.log")
	systemctl := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$HERMEC_TEST_LOG\"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "packaging", "dedicated", "update.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for name, candidateBody := range map[string]string{
		"wrong version output": "echo not-hermec; exit 0",
		"state check failure":  "[ \"$1\" = -version ] && { echo 'hermec-server test'; exit 0; }; exit 1",
	} {
		t.Run(name, func(t *testing.T) {
			candidate := filepath.Join(t.TempDir(), "bad-server")
			if err := os.WriteFile(candidate, []byte("#!/bin/sh\n"+candidateBody+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", script, "update", candidate)
			binDir := t.TempDir()
			cmd.Env = append(os.Environ(), "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_BIN="+filepath.Join(binDir, "hermec-server"), "HERMEC_SYSTEMCTL="+systemctl, "HERMEC_TEST_LOG="+logPath)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("bad candidate accepted: %s", output)
			}
			if left, _ := os.ReadDir(binDir); len(left) != 0 {
				t.Fatalf("rejected candidate left files behind: %v", left)
			}
		})
	}
	if data, err := os.ReadFile(logPath); err == nil && len(data) != 0 {
		t.Fatalf("systemctl called before candidate validation: %q", data)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestUpdateRollsBackAfterPostStartHealthFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dedicated lifecycle scripts require POSIX shell")
	}
	if os.Geteuid() == 0 {
		t.Skip("update.sh takes its systemd-run branch as root")
	}
	dir := t.TempDir()
	bin, previous := filepath.Join(dir, "bin", "hermec-server"), filepath.Join(dir, "lib", "previous")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(dir, "new")
	if err := os.WriteFile(candidate, []byte("#!/bin/sh\n[ \"$1\" = -version ] && { echo 'hermec-server test'; exit 0; }\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	starts := filepath.Join(dir, "starts")
	systemctl := filepath.Join(dir, "systemctl")
	fakeSystemctl := "#!/bin/sh\ncase \"$1\" in\nstart) n=$(cat \"$HERMEC_TEST_STARTS\" 2>/dev/null || echo 0); echo $((n + 1)) > \"$HERMEC_TEST_STARTS\" ;;\nis-active) n=$(cat \"$HERMEC_TEST_STARTS\" 2>/dev/null || echo 0); [ \"$n\" -ne 1 ] ;;\nesac\n"
	if err := os.WriteFile(systemctl, []byte(fakeSystemctl), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "packaging", "dedicated", "update.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, "update", candidate)
	cmd.Env = append(os.Environ(), "HERMEC_ALLOW_NON_ROOT=1", "HERMEC_BIN="+bin, "HERMEC_PREVIOUS="+previous, "HERMEC_SYSTEMCTL="+systemctl, "HERMEC_TEST_STARTS="+starts, "HERMEC_HEALTH_ATTEMPTS=1", "HERMEC_HEALTH_DELAY=0")
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("unhealthy candidate accepted: %s", output)
	}
	got, err := os.ReadFile(bin)
	if err != nil || !bytes.Contains(got, []byte("echo old")) {
		t.Fatalf("rollback binary = %q, %v", got, err)
	}
	if data, err := os.ReadFile(starts); err != nil || strings.TrimSpace(string(data)) != "2" {
		t.Fatalf("starts = %q, %v; want new and rollback starts", data, err)
	}
}

func TestServerVersionFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary build in -short mode")
	}
	name := "hermec-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(t.TempDir(), name)
	if output, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, exe, "-version").CombinedOutput()
	if err != nil {
		t.Fatalf("hermec-server -version: %v\n%s", err, output)
	}
	if got, want := string(output), "hermec-server dev\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}
