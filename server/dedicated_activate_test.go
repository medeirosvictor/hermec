package server

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

func TestDedicatedActivateRequiresFounderThenFencesExactReadyBoundary(t *testing.T) {
	ch, sourceKey, destinationKey := twoHostChain(t)
	s := New(Config{HostKey: sourceKey, Chain: ch, DedicatedTransitionPath: filepath.Join(t.TempDir(), "transition.json"), HostName: "source"})
	head := ch.Head()
	hash := head.Hash()
	count, historyHash := proto.DedicatedHistoryCommitment(nil)
	ready := proto.DedicatedReadiness{ServerID: ch.ID().String(), DestinationFingerprint: identity.Fingerprint(destinationKey.PublicKey()), ChainHead: head.Seq, ChainHeadHash: hash[:], HistoryLastUnixMilli: time.Time{}.UnixMilli(), HistoryCount: count, HistoryHash: historyHash, TransitionNonce: make([]byte, proto.DedicatedTransitionNonceSize)}
	sig, err := proto.SignDedicatedReadiness(destinationKey, ready)
	if err != nil {
		t.Fatal(err)
	}
	dst := &conn{srv: s, fingerprint: ready.DestinationFingerprint, pub: destinationKey.PublicKey(), send: make(chan []byte, sendBuffer), drained: make(chan struct{}), link: &linkState{peerFingerprint: ready.DestinationFingerprint, hello: proto.LinkHello{Name: "vps"}, dedicated: &ready, dedicatedProof: &proto.DedicatedReadinessProof{Readiness: ready, Signature: sig}}}
	s.links[dst] = struct{}{}

	request := func(c *conn, nonce []byte) proto.DedicatedResult {
		b, _ := json.Marshal(proto.DedicatedActivate{DestinationFingerprint: ready.DestinationFingerprint, TransitionNonce: nonce, Ref: "request"})
		c.handleDedicatedActivate(proto.Envelope{Data: b})
		var env proto.Envelope
		if err := json.Unmarshal(<-c.send, &env); err != nil {
			t.Fatal(err)
		}
		var r proto.DedicatedResult
		if err := json.Unmarshal(env.Data, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	nonFounder := &conn{srv: s, fingerprint: "not-founder", send: make(chan []byte, 1)}
	if got := request(nonFounder, nil); got.Status != "error" || s.Mode() != ModeActive {
		t.Fatalf("non-founder result=%+v mode=%v", got, s.Mode())
	}
	founder := &conn{srv: s, fingerprint: identity.Fingerprint(sourceKey.PublicKey()), send: make(chan []byte, 2)}
	if got := request(founder, nil); got.Status != "ready" || got.Readiness == nil {
		t.Fatalf("ready result=%+v", got)
	}
	if got := request(founder, ready.TransitionNonce); got.Status != "success" || s.Mode() != ModeFenced {
		t.Fatalf("activate result=%+v mode=%v", got, s.Mode())
	}
	// The source queued the one durable commit for the destination.
	if len(dst.send) != 1 {
		t.Fatalf("destination commits=%d, want 1", len(dst.send))
	}
}

func TestDedicatedActivateVoiceGateAndUnknownRemainUnfenced(t *testing.T) {
	ch, sourceKey, _ := twoHostChain(t)
	s := New(Config{HostKey: sourceKey, Chain: ch, DedicatedTransitionPath: filepath.Join(t.TempDir(), "transition.json")})
	founder := &conn{srv: s, fingerprint: identity.Fingerprint(sourceKey.PublicKey()), send: make(chan []byte, 1)}
	b, _ := json.Marshal(proto.DedicatedActivate{DestinationFingerprint: "unknown", Ref: "request"})
	founder.handleDedicatedActivate(proto.Envelope{Data: b})
	var env proto.Envelope
	_ = json.Unmarshal(<-founder.send, &env)
	var r proto.DedicatedResult
	_ = json.Unmarshal(env.Data, &r)
	if r.Status != "pending" || s.Mode() != ModeActive {
		t.Fatalf("unknown result=%+v mode=%v", r, s.Mode())
	}
}
