package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
)

func TestDedicatedHostPublicPrintsOnlyPublicMaterial(t *testing.T) {
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "host.key")
	if err := id.Save(key); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := dedicatedHostPublic(key, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), identity.Fingerprint(id.PublicKey())) || strings.Contains(out.String(), "SEED") {
		t.Fatalf("unsafe output %q", out.String())
	}
}

func TestDedicatedPrepareInputsRequirePinnedWSSAndID(t *testing.T) {
	if err := validateDedicatedPrepareInputs("ws://host/", "bad"); err == nil {
		t.Fatal("accepted unpinned inputs")
	}
	for _, raw := range []string{"wss://user:secret@host/", "wss://host/?token=secret", "wss://host/#secret", " wss://host/", "wss://HOST/"} {
		if err := validateDedicatedPrepareInputs(raw, (chain.ID{}).String()); err == nil {
			t.Fatalf("accepted unsafe URL %q", raw)
		}
	}
	if err := validateDedicatedPrepareInputs("wss://host/", (chain.ID{}).String()); err != nil {
		t.Fatal(err)
	}
}

func TestDedicatedPrepareAndRetryConflict(t *testing.T) {
	if _, err := parseFlags([]string{"-dedicated-prepare", "wss://host/", "-dedicated-retry"}); err == nil {
		t.Fatal("accepted prepare and retry together")
	}
}
