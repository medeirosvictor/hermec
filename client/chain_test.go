package client

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/server"
)

func chainServer(t *testing.T, founder *identity.Identity, grants map[string][]string) string {
	t.Helper()
	cfg := server.Config{
		HostKey:       founder,
		Roles:         roles.Config{Roles: roles.Builtin(), DefaultRoles: []string{"user"}, Grants: grants},
		Channels:      []string{"general"},
		VoiceChannels: []string{"lounge"},
	}
	return startServer(t, cfg)
}

func fpOf(id *identity.Identity) string { return identity.Fingerprint(id.PublicKey()) }

func mustGen(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// waitChainEvent returns the next chain entry event, skipping others.
func waitChainEvent(t *testing.T, c *Client) chain.Entry {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				t.Fatal("events closed before a chain event")
			}
			if ev.Chain != nil {
				return *ev.Chain
			}
		case <-timeout:
			t.Fatal("no chain event")
		}
	}
}

func TestChainAppendBroadcasts(t *testing.T) {
	founder, other := mustGen(t), mustGen(t)
	url := chainServer(t, founder, nil)
	a, err := Dial(testCtx(t), url, founder, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Dial(testCtx(t), url, other, "b", "")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	x := fpOf(mustGen(t))
	if err := a.ChainAppend(testCtx(t), chain.OpGrant, chain.GrantBody{Fingerprint: x, Roles: []string{"bot"}}); err != nil {
		t.Fatalf("ChainAppend: %v", err)
	}
	e := waitChainEvent(t, b)
	var g chain.GrantBody
	if e.Op != chain.OpGrant || e.DecodeBody(&g) != nil || g.Fingerprint != x {
		t.Fatalf("event entry = %+v", e)
	}
	for _, c := range []*Client{a, b} {
		st := c.Chain().State()
		if got := st.Grants[x]; len(got) != 1 || got[0] != "bot" {
			t.Errorf("local grants[x] = %v", got)
		}
	}
}

func TestChainAppendForbiddenForUser(t *testing.T) {
	founder, user := mustGen(t), mustGen(t)
	url := chainServer(t, founder, nil)
	u, err := Dial(testCtx(t), url, user, "u", "")
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	err = u.ChainAppend(testCtx(t), chain.OpGrant, chain.GrantBody{Fingerprint: "x", Roles: []string{"admin"}})
	var se *ServerError
	if !errors.As(err, &se) || se.Code != "forbidden" {
		t.Fatalf("err = %v, want forbidden", err)
	}
}

func TestChainAppendConflictRetries(t *testing.T) {
	founder, admin := mustGen(t), mustGen(t)
	url := chainServer(t, founder, map[string][]string{fpOf(admin): {"admin"}})
	a, err := Dial(testCtx(t), url, founder, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Dial(testCtx(t), url, admin, "b", "")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	go drain(a)
	go drain(b)

	const each = 8
	var wg sync.WaitGroup
	errs := make(chan error, 2*each)
	for _, c := range []*Client{a, b} {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				errs <- c.ChainAppend(testCtx(t), chain.OpGrant,
					chain.GrantBody{Fingerprint: fpOf(mustGen(t)), Roles: []string{"bot"}})
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ChainAppend: %v", err)
		}
	}
	// Either client may hold the last entry before the other receives it, so
	// both heads are re-read until they agree.
	deadline := time.Now().Add(5 * time.Second)
	for a.Chain().Head().Seq != b.Chain().Head().Seq && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ha, hb := a.Chain().Head().Seq, b.Chain().Head().Seq; ha != hb {
		t.Fatalf("heads differ: a=%d b=%d", ha, hb)
	}
}

func drain(c *Client) {
	for range c.Events() {
	}
}

func TestBanEndsConnectionAndRedialRefused(t *testing.T) {
	founder, bad := mustGen(t), mustGen(t)
	url := chainServer(t, founder, nil)
	a, err := Dial(testCtx(t), url, founder, "a", "")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Dial(testCtx(t), url, bad, "b", "")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := a.ChainAppend(testCtx(t), chain.OpBan, chain.FPBody{Fingerprint: fpOf(bad)}); err != nil {
		t.Fatalf("ban: %v", err)
	}
	sawBanned := false
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-b.Events():
			if !ok {
				break loop
			}
			var se *ServerError
			if errors.As(ev.Err, &se) && se.Code == proto.CodeBanned {
				sawBanned = true
			}
		case <-timeout:
			t.Fatal("banned client's events never closed")
		}
	}
	if !sawBanned {
		t.Error("no banned error event")
	}
	_, err = Dial(testCtx(t), url, bad, "b", "")
	var se *ServerError
	if !errors.As(err, &se) || se.Code != proto.CodeBanned {
		t.Fatalf("redial err = %v, want banned", err)
	}
}
