package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/server"
)

// The server refuses to start with a key the chain does not list as a host,
// and the message names the key file.
func TestCheckHostKeyNamesTheKeyFile(t *testing.T) {
	founder, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	c, _, err := server.OpenChain(filepath.Join(dir, "chain.jsonl"), founder, server.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "hermec-server.key")
	if err := checkHostKey(keyPath, c, founder); err != nil {
		t.Fatalf("founder: %v", err)
	}
	lost, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	err = checkHostKey(keyPath, c, lost)
	if err == nil {
		t.Fatal("a key outside the host list was accepted")
	}
	for _, want := range []string{keyPath, "not an approved host of server srv-"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, missing %q", err, want)
		}
	}
}
