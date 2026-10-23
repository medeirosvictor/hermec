package server

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/hshake"
	"github.com/medeirosvictor/hermec/core/proto"
)

// mergePace spaces the history pages sent to the winner of a split brain, so
// a long history stays under its per-connection message limit.
const mergePace = 40 * time.Millisecond

// mergeAckTimeout is how long the winner has to confirm it merged our history.
const mergeAckTimeout = 30 * time.Second

// noHistory is a HistoryLastTS far enough ahead that the peer sends none.
func noHistory() time.Time { return time.Now().AddDate(100, 0, 0) }

// dialMerge opens a host link to the winner of a split brain at url, asking for
// its whole chain (our own may be ahead of or diverge from it) and none of its
// history. It returns the link's socket, ready for sendMergeHistory, and the
// winner's chain entries after genesis.
func dialMerge(ctx context.Context, url string, s *Server) (*websocket.Conn, []chain.Entry, error) {
	ws, err := dialHost(ctx, url, s, 0)
	if err != nil {
		return nil, nil, err
	}
	deadline := time.Now().Add(hshake.DefaultAuthTimeout)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	_ = ws.SetReadDeadline(deadline)
	_ = ws.SetWriteDeadline(deadline)
	stop := watchClose(ctx, ws)
	defer stop()
	voice, clients := s.load()
	hello := proto.LinkHello{
		Addrs:         s.advertise(),
		Name:          s.cfg.HostName,
		HistoryLastTS: noHistory(),
		// The winner checks it still wins against this load.
		Yield: &proto.YieldLoad{VoiceMembers: voice, Clients: clients, Priority: s.selfInfo().Priority},
	}
	raw, err := proto.Encode(proto.TypeLinkHello, hello)
	if err == nil {
		err = ws.WriteMessage(websocket.TextMessage, raw)
	}
	if err != nil {
		ws.Close()
		return nil, nil, fmt.Errorf("server: merge hello: %w", err)
	}
	// The chain frames come first, then the first link_sync (the password key).
	var entries []chain.Entry
	err = readFrames(ws, "merge", func(env proto.Envelope) (bool, error) {
		switch env.Type {
		case proto.TypeChain:
			es, err := decodeChainFrame(env)
			entries = append(entries, es...)
			return false, err
		case proto.TypeLinkSync:
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		ws.Close()
		return nil, nil, err
	}
	_ = ws.SetReadDeadline(time.Time{})
	_ = ws.SetWriteDeadline(time.Time{})
	return ws, entries, nil
}

// sendMergeHistory sends every chat line we hold to the winner as link_sync
// pages and waits until it has merged them: a link_probe sent after the last
// page is answered after the pages were handled.
func (s *Server) sendMergeHistory(ws *websocket.Conn) error {
	if s.hist != nil {
		var ts time.Time
		id := ""
		for {
			msgs, more := s.hist.After(ts, id, historyPage)
			raw, n := encodeLinkSync(msgs, more)
			if raw == nil || (n == 0 && len(msgs) > 0) || len(raw) > proto.MaxMessageSize {
				return fmt.Errorf("server: merge: cannot encode history")
			}
			_ = ws.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
				return fmt.Errorf("server: merge history: %w", err)
			}
			if n > 0 {
				ts, id = msgs[n-1].TS, msgs[n-1].ID
			}
			if n == len(msgs) && !more {
				break
			}
			time.Sleep(mergePace)
		}
	}
	raw, err := proto.Encode(proto.TypeLinkProbe, struct{}{})
	if err == nil {
		_ = ws.SetWriteDeadline(time.Now().Add(writeTimeout))
		err = ws.WriteMessage(websocket.TextMessage, raw)
	}
	if err != nil {
		return fmt.Errorf("server: merge history: %w", err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(mergeAckTimeout))
	return readFrames(ws, "merge history", func(env proto.Envelope) (bool, error) {
		return env.Type == proto.TypeLinkProbeReply, nil
	})
}

// watchClose closes ws when ctx ends, so a read or write blocked on it fails;
// the returned stop ends the watch.
func watchClose(ctx context.Context, ws *websocket.Conn) (stop func()) {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			ws.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// replaceChain makes the chain es (genesis first) ours, in place, and applies
// it as a link would: group state, history limits, links of removed hosts,
// and the kick of everyone banned.
func (s *Server) replaceChain(es []chain.Entry) error {
	s.chainMu.Lock()
	defer s.chainMu.Unlock()
	if err := s.chain.Replace(es); err != nil {
		return err
	}
	s.afterChainChange()
	s.dropRemovedLinks()
	st := s.chain.State()
	for fp := range st.Bans {
		s.kick(fp)
	}
	return nil
}
