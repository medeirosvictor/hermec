package main

import (
	"net"
	"strings"
	"testing"
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

func TestCheckNotRunningSkipsUnboundPorts(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1:0", ":0", "nonsense"} {
		if err := checkNotRunning(addr); err != nil {
			t.Errorf("checkNotRunning(%q) = %v, want nil", addr, err)
		}
	}
}
