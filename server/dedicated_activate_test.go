package server

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/server/history"
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

func TestDedicatedActivateFencedResultIsDurableAndFounderOnly(t *testing.T) {
	ch, sourceKey, destinationKey := twoHostChain(t)
	path := filepath.Join(t.TempDir(), "transition.json")
	s := New(Config{HostKey: sourceKey, Chain: ch, DedicatedTransitionPath: path, HostName: "source"})
	h, err := history.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	s.hist = h
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
	request := func(server *Server, fingerprint string, nonce []byte, ref string) proto.DedicatedResult {
		c := &conn{srv: server, fingerprint: fingerprint, send: make(chan []byte, 1)}
		data, _ := json.Marshal(proto.DedicatedActivate{DestinationFingerprint: ready.DestinationFingerprint, TransitionNonce: nonce, Ref: ref})
		c.route(proto.Envelope{Type: proto.TypeDedicatedActivate, Data: data})
		var env proto.Envelope
		_ = json.Unmarshal(<-c.send, &env)
		var result proto.DedicatedResult
		_ = json.Unmarshal(env.Data, &result)
		return result
	}
	founder := identity.Fingerprint(sourceKey.PublicKey())
	if got := request(s, founder, ready.TransitionNonce, "first"); got.Status != "success" {
		t.Fatalf("initial activation = %+v", got)
	}
	// The result can be lost: the same nonce reports the persisted success,
	// while a changed nonce and a non-founder cannot learn or alter it.
	if got := request(s, founder, ready.TransitionNonce, "lost-result"); got.Status != "success" || got.Readiness == nil {
		t.Fatalf("fenced retry = %+v", got)
	}
	other := append([]byte(nil), ready.TransitionNonce...)
	other[0]++
	if got := request(s, founder, other, "changed"); got.Status != "retry" {
		t.Fatalf("changed transition = %+v", got)
	}
	if got := request(s, "not-founder", ready.TransitionNonce, "not-founder"); got.Status != "error" {
		t.Fatalf("non-founder retry = %+v", got)
	}

	// A new process restores fenced mode from the journal and returns the same
	// result, rather than requiring the founder to create another transition.
	restarted := New(Config{HostKey: sourceKey, Chain: ch, DedicatedTransitionPath: path, HostName: "source"})
	restartedHistory, err := history.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer restartedHistory.Close()
	restarted.hist = restartedHistory
	if err := restarted.restoreDedicatedTransition(); err != nil {
		t.Fatal(err)
	}
	if got := request(restarted, founder, ready.TransitionNonce, "restart"); got.Status != "success" {
		t.Fatalf("restart retry = %+v", got)
	}
}

func TestDedicatedActivateMalformedRequestEchoesRef(t *testing.T) {
	s := New(Config{})
	c := &conn{srv: s, send: make(chan []byte, 1)}
	data, _ := json.Marshal(proto.DedicatedActivate{Ref: "correlate"})
	c.handleDedicatedActivate(proto.Envelope{Data: data})
	var env proto.Envelope
	_ = json.Unmarshal(<-c.send, &env)
	var result proto.DedicatedResult
	_ = json.Unmarshal(env.Data, &result)
	if result.Ref != "correlate" || result.Status != "error" {
		t.Fatalf("malformed result = %+v", result)
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
