package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// bigChain returns a chain whose chain frames total at least size bytes.
func bigChain(t *testing.T, cfg Config, size int) *chain.Chain {
	t.Helper()
	ch, err := newChain(cfg.HostKey, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for {
		for i := 0; i < 10; i++ {
			head := ch.Head()
			e, err := chain.NewEntry(cfg.HostKey, &head, chain.OpGrant, chain.GrantBody{
				Fingerprint: identity.Fingerprint(mustID(t).PublicKey()), Roles: []string{"user"},
			}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err := ch.Append(e); err != nil {
				t.Fatal(err)
			}
		}
		frames, err := chainFrames(ch.Entries())
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, f := range frames {
			n += len(f)
		}
		if n >= size {
			return ch
		}
	}
}

func TestPreAuthChainBudget(t *testing.T) {
	cfg := rateCfg()
	cfg.HostKey = mustID(t)
	cfg.Chain = bigChain(t, cfg, 400<<10)
	clk := &fakeClock{now: time.Unix(3_000_000, 0)}
	url := startRateServer(t, cfg, clk)

	for i := 0; i < 5; i++ {
		c := dial(t, url)
		hello(t, c)
		_ = c.Close()
	}
	// The 6th chain send in the minute would pass chainSendBudget: the
	// connection is closed before any chain frame.
	c := dial(t, url)
	readChallenge(t, c)
	sendEnv(t, c, proto.TypeHostProofReq, proto.HostProofReq{Nonce: make([]byte, 32)})
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	busy := false
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			break
		}
		env, derr := proto.DecodeHandshake(raw)
		if derr != nil {
			continue
		}
		if env.Type == proto.TypeChain {
			t.Fatal("chain frame sent over budget")
		}
		var em proto.ErrorMsg
		if env.Type == proto.TypeError && json.Unmarshal(env.Data, &em) == nil && em.Code == proto.CodeServerBusy {
			busy = true
		}
	}
	if !busy {
		t.Error("no server_busy error before the close")
	}
	_ = c.Close()

	clk.Advance(time.Minute)
	c = dial(t, url)
	hello(t, c)
	_ = c.Close()
}
