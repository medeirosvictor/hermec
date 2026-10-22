package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/server"
	"github.com/medeirosvictor/hermec/ui"
)

// The README recipe for an app host: with the app closed, -apply-config with
// the app's identity.key and chains/<id>.jsonl changes history_days.
func TestApplyConfigToAppHostedChain(t *testing.T) {
	cfgDir := filepath.Join(t.TempDir(), "hermec") // stands in for <config>/hermec
	keyPath := filepath.Join(cfgDir, "identity.key")
	k, err := ui.LoadOrCreateIdentity(keyPath) // the app's own key writer
	if err != nil {
		t.Fatal(err)
	}

	// Found the chain the way the app does: K is founder and host, the file
	// sits at chains/<server-id>.jsonl.
	hosted := server.DefaultConfig()
	hosted.ServerName = "den"
	hosted.VoiceChannels = []string{"voice"}
	c, _, err := server.OpenChain(filepath.Join(t.TempDir(), "chain.jsonl"), k, hosted)
	if err != nil {
		t.Fatal(err)
	}
	chainPath := filepath.Join(cfgDir, "chains", c.ID().String()+".jsonl")
	if err := os.MkdirAll(filepath.Dir(chainPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPath(chainPath); err != nil {
		t.Fatal(err)
	}

	// addr is the hosted port, so the running-server check means something.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	tomlPath := filepath.Join(t.TempDir(), "history.toml")
	body := fmt.Sprintf("addr = %q\nhistory_days = 0\n", addr)
	if err := os.WriteFile(tomlPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := server.LoadConfig(tomlPath)
	if err != nil {
		t.Fatal(err)
	}

	// While the app still listens on the port, the apply is refused.
	var out bytes.Buffer
	if err := applyConfigFile(tomlPath, keyPath, chainPath, cfg, &out); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("apply with the port busy: err = %v", err)
	}
	ln.Close()

	out.Reset()
	if err := applyConfigFile(tomlPath, keyPath, chainPath, cfg, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "appended 1 entries") {
		t.Errorf("output = %q", out.String())
	}
	got, err := chain.Load(chainPath)
	if err != nil {
		t.Fatal(err)
	}
	st := got.State().Config
	if st.HistoryDays == nil || *st.HistoryDays != 0 {
		t.Fatalf("HistoryDays = %v, want 0", st.HistoryDays)
	}
	if st.Name != "den" || len(st.VoiceChannels) != 1 {
		t.Errorf("other fields changed: %+v", st)
	}
	if err := server.CheckHost(got, k); err != nil {
		t.Errorf("app key no longer hosts: %v", err)
	}
}
