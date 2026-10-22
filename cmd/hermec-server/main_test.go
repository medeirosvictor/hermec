package main

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/server"
)

func TestCheckNotRunningRefusesBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	err = checkNotRunning(addr)
	if err == nil || !strings.Contains(err.Error(), "stop the running server") {
		t.Fatalf("busy port: err = %v, want a refusal", err)
	}
	ln.Close()
	if err := checkNotRunning(addr); err != nil {
		t.Fatalf("free port: %v", err)
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

func TestCheckNotRunningSkipsUnboundPorts(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1:0", ":0", "nonsense"} {
		if err := checkNotRunning(addr); err != nil {
			t.Errorf("checkNotRunning(%q) = %v, want nil", addr, err)
		}
	}
}
