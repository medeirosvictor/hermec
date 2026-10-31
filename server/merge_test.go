package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

func fixedLoad(clients int) func() (int, int) {
	return func() (int, int) { return 0, clients }
}

func TestMergeRefusedWhenWinnerWouldLose(t *testing.T) {
	a, b, aURL, _, _, _, _ := linkPair(t, "pw")
	a.load = fixedLoad(1)
	b.load = fixedLoad(3) // the loser claims more clients than the winner has
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := DialMerge(ctx, aURL, b)
	if err == nil {
		ws.Close()
		t.Fatal("the winner accepted a merge it would itself lose")
	}
}

func TestMergeLinkIsNotAStandbyCandidate(t *testing.T) {
	a, b, aURL, _, _, _, _ := linkPair(t, "pw")
	a.load = fixedLoad(1)
	b.load = fixedLoad(0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := DialMerge(ctx, aURL, b)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	waitFor(t, "merge link", func() bool { return len(a.Links()) == 1 })
	if fp := standbyFP(a); fp != "" {
		t.Fatalf("a merge link was picked as standby: %s", fp)
	}
}

func TestSendMergeHistoryOversizeLineErrors(t *testing.T) {
	s, _ := startModeServer(t, Config{Channels: []string{"general"}})
	big := proto.ChatMessage{ID: "big", Channel: "general", Text: strings.Repeat("x", 70*1024), TS: time.Now().UTC()}
	if err := s.hist.Add(big); err != nil {
		t.Skipf("history refuses the line: %v", err)
	}
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("sendMergeHistory panicked: %v", r)
			}
		}()
		err = s.SendMergeHistory(nil)
	}()
	if err == nil {
		t.Fatal("want an error for a line that cannot fit a frame")
	}
}

// A history of several pages reaches the winner whole, in a bounded time: the
// pacing between pages must not make a merge take minutes.
func TestSendMergeHistoryMultiPage(t *testing.T) {
	s, _ := startModeServer(t, Config{Channels: []string{"general"}})
	const n = 6 * historyPage
	seedHistory(t, s, n)

	type result struct{ msgs, pages int }
	got := make(chan result, 1)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var res result
		for {
			_, raw, err := ws.ReadMessage()
			if err != nil {
				return
			}
			env, _ := proto.Decode(raw)
			switch env.Type {
			case proto.TypeLinkSync:
				var ls proto.LinkSync
				_ = json.Unmarshal(env.Data, &ls)
				res.msgs += len(ls.Messages)
				res.pages++
			case proto.TypeLinkProbe: // sent after the last page
				got <- res
				reply, _ := proto.Encode(proto.TypeLinkProbeReply, proto.LinkProbeReply{})
				_ = ws.WriteMessage(websocket.TextMessage, reply)
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	start := time.Now()
	if err := s.SendMergeHistory(ws); err != nil {
		t.Fatal(err)
	}
	res := <-got
	if res.msgs != n || res.pages < 2 {
		t.Fatalf("winner got %d messages in %d pages, want %d in several", res.msgs, res.pages, n)
	}
	if limit := time.Duration(res.pages)*(mergePace+50*time.Millisecond) + 2*time.Second; time.Since(start) > limit {
		t.Fatalf("merging %d pages took %v, want under %v", res.pages, time.Since(start), limit)
	}
}

func TestReplaceChainKicksBannedAndDropsRemovedLinks(t *testing.T) {
	_, b, _, bURL, ak, _, ac := linkPair(t, "pw")
	m := mustID(t)
	mc, env := authWarm(t, bURL, m, "")
	if env.Type != proto.TypeAuthOK {
		t.Fatalf("warm auth: %q", env.Type)
	}
	fc, err := chain.FromEntries(ac.Entries())
	if err != nil {
		t.Fatal(err)
	}
	head := fc.Head()
	e, err := chain.NewEntry(ak, &head, chain.OpBan, chain.FPBody{Fingerprint: identity.Fingerprint(m.PublicKey())}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := fc.Append(e); err != nil {
		t.Fatal(err)
	}
	if err := b.ReplaceChain(fc.Entries()); err != nil {
		t.Fatal(err)
	}
	_ = mc.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := mc.ReadMessage(); err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				t.Fatal("the banned warm session was not closed by replaceChain")
			}
			return
		}
	}
}
