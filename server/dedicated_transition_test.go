package server

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/server/dedicated"
	"github.com/medeirosvictor/hermec/server/history"
)

func testDedicatedTransition() dedicated.Record {
	return dedicated.Record{Version: 1, State: dedicated.Prepared, ServerID: "server", DestinationFingerprint: "destination", ChainHead: 4, ChainHeadHash: bytes.Repeat([]byte{2}, 32), HistoryLastID: "history", HistoryLastUnixMilli: 9, HistoryHash: bytes.Repeat([]byte{3}, 32), TransitionNonce: bytes.Repeat([]byte{1}, proto.DedicatedTransitionNonceSize), Recovery: dedicated.RecoveryRetryPreparation}
}

func TestStartRetryRevalidatesInvalidDedicatedJournal(t *testing.T) {
	c, source, _ := twoHostChain(t)
	path := filepath.Join(t.TempDir(), dedicated.TransitionFile)
	if err := os.WriteFile(path, []byte(`{"version":1,"state":"invalid"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(Config{
		Addr: "127.0.0.1:0", HostKey: source, Chain: c,
		HistoryPath: filepath.Join(t.TempDir(), "history.jsonl"), DedicatedTransitionPath: path,
	})
	for attempt := 1; attempt <= 2; attempt++ {
		if err := s.Start(); err == nil {
			t.Fatalf("Start attempt %d accepted invalid dedicated journal", attempt)
		}
		if s.hist != nil {
			t.Fatalf("Start attempt %d retained history after failure", attempt)
		}
		if s.Addr() != "" {
			t.Fatalf("Start attempt %d became active at %q", attempt, s.Addr())
		}
	}
}

func TestRestorePreparingTransitionRemainsPassiveAndExact(t *testing.T) {
	c, _, destination := twoHostChain(t)
	h, err := history.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	head := c.Head()
	headHash := head.Hash()
	_, historyHash := proto.DedicatedHistoryCommitment(nil)
	path := filepath.Join(t.TempDir(), dedicated.TransitionFile)
	r := dedicated.Record{Version: 1, State: dedicated.Preparing, ServerID: c.ID().String(), DestinationFingerprint: identity.Fingerprint(destination.PublicKey()), ChainHead: head.Seq, ChainHeadHash: headHash[:], HistoryHash: historyHash, TransitionNonce: bytes.Repeat([]byte{1}, proto.DedicatedTransitionNonceSize), Recovery: dedicated.RecoveryRetryPreparation, ActiveWSSURL: "wss://source.example/"}
	if err := dedicated.Save(path, r); err != nil {
		t.Fatal(err)
	}
	s := New(Config{HostKey: destination, Chain: c, DedicatedTransitionPath: path})
	s.hist = h
	if err := s.restoreDedicatedTransition(); err != nil {
		t.Fatal(err)
	}
	if s.Mode() != ModePrepared || !s.Prepared() {
		t.Fatalf("preparing recovery activated destination: mode=%v prepared=%v", s.Mode(), s.Prepared())
	}
	s.SetMode(ModeActive) // the public guard must retain passive mode
	if s.Mode() != ModePrepared {
		t.Fatal("prepared destination accepted activation")
	}
}
