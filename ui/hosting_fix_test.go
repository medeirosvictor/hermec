package ui

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/server"
)

func TestStartHostedDoesNotBlock(t *testing.T) {
	g, path := hostedTestGame(t, freeLoopback(t), true)
	g.selfAddrs = func(context.Context) (string, string) {
		time.Sleep(400 * time.Millisecond)
		return "", "127.0.0.1"
	}
	start := time.Now()
	g.bootHosted(path)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("bootHosted took %v with a slow address lookup", d)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(g.hosted.srv.Active().Addrs) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("advertised addresses never arrived")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// testChains returns a 3-entry chain and its 2-entry prefix.
func testChains(t *testing.T) (long, short *chain.Chain) {
	t.Helper()
	k := mustIdentity(t)
	g, err := chain.NewEntry(k, nil, chain.OpGenesis, chain.GenesisBody{
		Founder: []byte(k.PublicKey()), Salt: []byte("0123456789abcdef"),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	long, err = chain.New(g)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		head := long.Head()
		e, err := chain.NewEntry(k, &head, chain.OpGrant, chain.GrantBody{Fingerprint: "x", Roles: []string{"bot"}}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := long.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	short, err = chain.FromEntries(long.Entries()[:2])
	if err != nil {
		t.Fatal(err)
	}
	return long, short
}

func TestReconcileChainKeepsLonger(t *testing.T) {
	long, short := testChains(t)
	path := filepath.Join(t.TempDir(), "c.jsonl")
	if err := long.SetPath(path); err != nil {
		t.Fatal(err)
	}
	got, err := reconcileChain(path, short)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got.Entries()); n != 3 {
		t.Fatalf("kept %d entries, want 3", n)
	}
	if disk, err := chain.Load(path); err != nil || len(disk.Entries()) != 3 {
		t.Fatalf("file was shortened: %v", err)
	}
	// The other way round the file is extended.
	path2 := filepath.Join(t.TempDir(), "d.jsonl")
	if err := short.SetPath(path2); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileChain(path2, long); err != nil {
		t.Fatal(err)
	}
	if disk, err := chain.Load(path2); err != nil || len(disk.Entries()) != 3 {
		t.Fatalf("file not extended: %v", err)
	}
}

func TestReconcileChainRefusesFork(t *testing.T) {
	long, _ := testChains(t)
	other, _ := testChains(t)
	path := filepath.Join(t.TempDir(), "c.jsonl")
	if err := long.SetPath(path); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileChain(path, other); err == nil {
		t.Fatal("a forked chain was accepted")
	}
	if disk, err := chain.Load(path); err != nil || len(disk.Entries()) != 3 {
		t.Fatal("local file was overwritten")
	}
}

func TestFriendStopAsyncAndRestartWaits(t *testing.T) {
	g := friendGame(t)
	_, s, id := grantedFriend(t, g, "")
	port := g.friends[id].port
	release := make(chan struct{})
	old := shutdownHostFn
	shutdownHostFn = func(h *server.Host) {
		<-release
		old(h)
	}
	t.Cleanup(func() { shutdownHostFn = old })
	start := time.Now()
	g.stopFriend(id)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("stopFriend blocked for %v", d)
	}
	g.setFriendHosting(s, true) // quick restart while the old host still holds its port
	if g.friends[id] == nil || g.friends[id].port != port {
		t.Fatalf("restart moved port: %+v", g.friends[id])
	}
	close(release)
	pumpUntil(t, g, friendRole(g, id))
}

func TestFriendStartBacksOff(t *testing.T) {
	g := friendGame(t)
	_, s, id := grantedFriend(t, g, "")
	g.stopFriend(id)
	g.friendBackoff = map[string]time.Time{id: time.Now().Add(time.Minute)}
	g.syncFriendHosting(s)
	if g.friends[id] != nil {
		t.Fatal("start retried during backoff")
	}
}

func TestFriendSetKeyPersists(t *testing.T) {
	g := friendGame(t)
	_, _, id := grantedFriend(t, g, "")
	key := make([]byte, 32)
	key[0] = 7
	g.friends[id].setKey(key)
	if hf := readHosting(t, g, id); hf.PasswordKey != hex.EncodeToString(key) {
		t.Fatalf("password_key = %q", hf.PasswordKey)
	}
}

// A damaged hosting file at boot must not start a host (or panic): a file that
// does not parse is skipped, a bad password_key is reported to the user.
func TestBootFriendsSurvivesBadHostingFile(t *testing.T) {
	for _, tc := range []struct {
		name, content, notice string
	}{
		{"corrupt toml", "not [valid", ""},
		{"key not hex", "enabled = true\nport = 7800\npassword_key = \"zz\"\n", "bad password_key"},
		{"key wrong length", "enabled = true\nport = 7800\npassword_key = \"abcd\"\n", "bad password_key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := friendGame(t)
			_, _, id := grantedFriend(t, g, "")
			g.stopFriends()
			if err := os.WriteFile(hostingPath(g, id), []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			g2 := friendGame(t)
			g2.id, g2.fp = g.id, g.fp
			g2.hostedPath = g.hostedPath
			g2.bootFriends()
			g2.pumpSessions()
			if len(g2.friends) != 0 {
				t.Fatalf("started a host from a bad hosting file: %v", g2.friends)
			}
			if !strings.Contains(g2.notice, tc.notice) || (tc.notice == "" && g2.notice != "") {
				t.Fatalf("notice = %q, want %q", g2.notice, tc.notice)
			}
		})
	}
}
