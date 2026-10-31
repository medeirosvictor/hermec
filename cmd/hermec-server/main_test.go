package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/server"
)

func TestFailedNewPublicStartupLeavesNoState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary build in -short mode")
	}
	dir := t.TempDir()
	cfg := server.DefaultConfig()
	cfg.PublicMode, cfg.Discoverable = true, false
	raw, err := cfg.MarshalTOML()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "server.toml")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "hermec-server")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin, "-config", configPath).CombinedOutput(); err == nil {
		t.Fatalf("unsafe public candidate started: %s", out)
	}
	for _, name := range []string{"hermec-server.key", "chain.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("failed startup persisted %s: %v", name, err)
		}
	}
}

func TestFlagsAdvertiseHosts(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "server.toml")
	f, err := parseFlags([]string{"-config", cfgPath, "-hosts", "ws://a:1/, ws://b:2/", "-advertise", "ws://c:3/", "-name", "gamma"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "known_hosts.txt"), []byte("ws://b:2/\r\n\nws://d:4/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := server.DefaultConfig()
	cfg.Addr = "127.0.0.1:9000"
	var notes []string
	hc := buildHostConfig(f, cfg, dir, func(s string) { notes = append(notes, s) })
	if got := hc.Server.Advertise; !slices.Equal(got, []string{"ws://c:3/"}) {
		t.Errorf("Advertise = %v", got)
	}
	if hc.Server.HostName != "gamma" {
		t.Errorf("HostName = %q", hc.Server.HostName)
	}
	if got := hc.Known(); !slices.Equal(got, []string{"ws://a:1/", "ws://b:2/", "ws://d:4/"}) {
		t.Errorf("Known = %v", got)
	}
	hc.SaveKnown([]string{"ws://e:5/"})
	b, err := os.ReadFile(filepath.Join(dir, "known_hosts.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "ws://e:5/") || !strings.Contains(string(b), "ws://d:4/") {
		t.Errorf("known_hosts.txt = %q", b)
	}
	hc.Notify("hosting: active")
	if !slices.Equal(notes, []string{"hosting: active"}) {
		t.Errorf("notes = %v", notes)
	}
}

func TestHostURLsValidatedAndNormalised(t *testing.T) {
	dir := t.TempDir()
	f, err := parseFlags([]string{"-hosts", "ws://a:1,ws://a:1/,http://b:2/,nonsense,wss://c:3,ws:///x", "-advertise", "ws://d:4,ftp://e"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ws://a:1/", "wss://c:3/"}; !slices.Equal(f.hosts, want) {
		t.Errorf("hosts = %v, want %v", f.hosts, want)
	}
	if want := []string{"ws://d:4/"}; !slices.Equal(f.advertise, want) {
		t.Errorf("advertise = %v, want %v", f.advertise, want)
	}
	if err := os.WriteFile(filepath.Join(dir, "known_hosts.txt"), []byte("ws://a:1\nbogus\nws://z:9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hc := buildHostConfig(f, server.DefaultConfig(), dir, nil)
	if want := []string{"ws://a:1/", "wss://c:3/", "ws://z:9/"}; !slices.Equal(hc.Known(), want) {
		t.Errorf("Known = %v, want %v", hc.Known(), want)
	}
}

func TestDefaultAdvertise(t *testing.T) {
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.168.1.5"), net.ParseIP("fe80::1"), net.ParseIP("2001:db8::7")}
	got := defaultAdvertise("0.0.0.0:7777", false, ips)
	want := []string{"ws://192.168.1.5:7777/", "ws://[2001:db8::7]:7777/"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := defaultAdvertise(":7777", true, ips[1:2]); !slices.Equal(got, []string{"wss://192.168.1.5:7777/"}) {
		t.Errorf("tls: %v", got)
	}
	if got := defaultAdvertise("127.0.0.1:7777", false, ips); !slices.Equal(got, []string{"ws://127.0.0.1:7777/"}) {
		t.Errorf("bound host: %v", got)
	}
}

func TestDefaultStatePathsFollowConfig(t *testing.T) {
	f := flags{config: filepath.Join("etc", "hermec", "server.toml"), chain: "custom.jsonl"}
	if dir := defaultStatePaths(&f); dir != filepath.Join("etc", "hermec") {
		t.Errorf("dir = %q", dir)
	}
	if f.key != filepath.Join("etc", "hermec", "hermec-server.key") || f.chain != "custom.jsonl" || f.history != filepath.Join("etc", "hermec", "history.jsonl") {
		t.Errorf("paths = %q %q %q", f.key, f.chain, f.history)
	}
	g := flags{}
	if dir := defaultStatePaths(&g); dir != "." || g.chain != "chain.jsonl" {
		t.Errorf("no config: dir %q chain %q", dir, g.chain)
	}
}

func TestRunRejectsConflictingFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-init-dedicated", t.TempDir(), "-check"}, "-init-dedicated cannot be combined"},
		{[]string{"-apply-config"}, "-apply-config requires -config"},
		{[]string{"-dedicated-host-key", "-dedicated-status"}, "-dedicated-host-key cannot be combined"},
		{[]string{"-dedicated-status", "-dedicated-retry"}, "-dedicated-status cannot be combined"},
		{[]string{"-dedicated-attestation-status", "-dedicated-status"}, "cannot be combined with another dedicated workflow flag"},
	} {
		f, err := parseFlags(tc.args)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if err := run(f); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
}
