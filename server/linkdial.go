package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/hshake"
	"github.com/medeirosvictor/hermec/core/proto"
)

// Link is this host's connection to the active host: it mirrors the active
// host's chain, password key and chat history into the local Server and
// tracks its heartbeats.
type Link struct {
	s  *Server
	ws *websocket.Conn

	writeMu sync.Mutex // gorilla permits a single concurrent writer

	lastBeat atomic.Int64 // unix nanoseconds of the newest frame from the active host
	standby  atomic.Bool

	evMu     sync.Mutex
	hosts    *proto.Hosts  // the newest hosts frame from the active host
	roleCh   chan struct{} // signaled (cap 1) when link_role arrives
	handover chan struct{} // signaled (cap 1) when the active host hands over

	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	err  error // why the link ended; nil for a deliberate Close
}

// dialHost opens a warm, password-less session to the host at url with s's
// host key, checking that it serves our chain.
func dialHost(ctx context.Context, url string, s *Server) (*websocket.Conn, error) {
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("server: dial host: %w", err)
	}
	name := s.cfg.HostName
	if name == "" {
		name = "host"
	}
	res, err := hshake.Run(ctx, ws, s.cfg.HostKey, name, "", hshake.Opts{Pin: s.chain.ID().String(), Warm: true})
	if err != nil {
		ws.Close()
		return nil, err
	}
	if res.ServerID != s.chain.ID().String() {
		ws.Close()
		return nil, fmt.Errorf("server: host serves another server")
	}
	return ws, nil
}

// DialLink connects to the active host at url, tells it where our chain and
// history end, and returns the link. Run must be called to apply what the
// active host sends. A host that is not active refuses with not_active, which
// surfaces from Run as an error.
func DialLink(ctx context.Context, url string, s *Server) (*Link, error) {
	return dialLinkOpts(ctx, url, s, false)
}

// dialLinkOpts is DialLink; with full set it asks for the active host's whole
// history, not just the lines after our newest (a merged host holds lines the
// active host older than that lacks to us).
func dialLinkOpts(ctx context.Context, url string, s *Server, full bool) (*Link, error) {
	ws, err := dialHost(ctx, url, s)
	if err != nil {
		return nil, err
	}
	l := &Link{s: s, ws: ws, done: make(chan struct{}), roleCh: make(chan struct{}, 1), handover: make(chan struct{}, 1)}
	l.lastBeat.Store(time.Now().UnixNano())
	hello := proto.LinkHello{
		ChainSeq: s.chain.Head().Seq,
		Addrs:    s.cfg.Advertise,
		Name:     s.cfg.HostName,
	}
	if s.hist != nil && !full {
		hello.HistoryLastTS, hello.HistoryLastID = s.hist.Last()
	}
	if err := l.write(proto.TypeLinkHello, hello); err != nil {
		ws.Close()
		return nil, err
	}
	return l, nil
}

// ProbeHost asks the host at url for its state: active or not, its load and
// priority. It authenticates warm with our host key, so no password is needed.
func ProbeHost(ctx context.Context, url string, s *Server) (proto.LinkProbeReply, error) {
	var reply proto.LinkProbeReply
	ws, err := dialHost(ctx, url, s)
	if err != nil {
		return reply, err
	}
	defer ws.Close()
	deadline := time.Now().Add(hshake.DefaultAuthTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = ws.SetReadDeadline(deadline)
	_ = ws.SetWriteDeadline(deadline)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			ws.Close()
		case <-stop:
		}
	}()
	raw, err := proto.Encode(proto.TypeLinkProbe, struct{}{})
	if err != nil {
		return reply, err
	}
	if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		return reply, fmt.Errorf("server: probe: %w", err)
	}
	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return reply, fmt.Errorf("server: probe: %w", err)
		}
		env, err := proto.Decode(raw)
		if err != nil {
			continue
		}
		switch env.Type {
		case proto.TypeLinkProbeReply:
			if err := json.Unmarshal(env.Data, &reply); err != nil {
				return reply, fmt.Errorf("server: probe: bad reply: %w", err)
			}
			return reply, nil
		case proto.TypeError:
			return reply, remoteError(env)
		}
	}
}

func remoteError(env proto.Envelope) error {
	var em proto.ErrorMsg
	_ = json.Unmarshal(env.Data, &em)
	return &hshake.ServerError{Code: em.Code, Message: em.Message}
}

func (l *Link) write(typ string, payload any) error {
	raw, err := proto.Encode(typ, payload)
	if err != nil {
		return err
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	_ = l.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := l.ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		return fmt.Errorf("server: link write: %w", err)
	}
	return nil
}

// finish ends the link with err (nil for a deliberate close).
func (l *Link) finish(err error) {
	l.once.Do(func() {
		l.mu.Lock()
		l.err = err
		l.mu.Unlock()
		l.ws.Close()
		close(l.done)
	})
}

// Close ends the link. It is safe to call more than once.
func (l *Link) Close() { l.finish(nil) }

// Done is closed when the link has ended, for any reason.
func (l *Link) Done() <-chan struct{} { return l.done }

// Err is why the link ended, nil while it is up or after a deliberate Close.
func (l *Link) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// Standby reports whether the active host has named this host its standby.
func (l *Link) Standby() bool { return l.standby.Load() }

// LastBeat is when the newest heartbeat arrived (the link's start until then).
func (l *Link) LastBeat() time.Time { return time.Unix(0, l.lastBeat.Load()) }

// ActiveAlive reports whether the active host has been heard from within
// heartbeatGrace of now.
func (l *Link) ActiveAlive(now time.Time) bool {
	return now.Sub(l.LastBeat()) < heartbeatGrace
}

// Run applies the active host's frames to the local server until the link
// ends, and heartbeats toward it. A local chain append that fails as a
// conflict or a bad chain ends the link with that error.
func (l *Link) Run() {
	beatDone := make(chan struct{})
	go func() {
		defer close(beatDone)
		l.beat()
	}()
	defer func() { <-beatDone }() // finish closes done, which ends beat
	gotKey := false
	for {
		_, raw, err := l.ws.ReadMessage()
		if err != nil {
			l.finish(fmt.Errorf("server: link: %w", err))
			return
		}
		env, err := proto.Decode(raw)
		if err != nil {
			continue
		}
		switch env.Type {
		case proto.TypeHeartbeat:
			l.lastBeat.Store(time.Now().UnixNano())
		case proto.TypeChain:
			var cf proto.ChainFrame
			if json.Unmarshal(env.Data, &cf) != nil {
				l.finish(errors.New("server: link: malformed chain frame"))
				return
			}
			es, err := hshake.DecodeEntries(cf.Entries)
			if err == nil {
				for _, e := range es {
					if err = l.s.applyLinkEntry(e); err != nil {
						break
					}
				}
			}
			if err != nil {
				l.finish(err)
				return
			}
		case proto.TypeLinkSync:
			var ls proto.LinkSync
			if json.Unmarshal(env.Data, &ls) != nil {
				l.finish(errors.New("server: link: malformed link_sync"))
				return
			}
			// The first link_sync carries the password key, empty when the
			// active host has none.
			if !gotKey {
				gotKey = true
				l.s.setPasswordKey(ls.PasswordKey)
			}
			l.s.mergeLinkMessages(ls.Messages)
		case proto.TypeLinkRole:
			var r proto.LinkRole
			if json.Unmarshal(env.Data, &r) == nil {
				l.standby.Store(r.Standby)
				signal(l.roleCh)
			}
		case proto.TypeHosts:
			var h proto.Hosts
			if json.Unmarshal(env.Data, &h) == nil {
				l.evMu.Lock()
				l.hosts = &h
				l.evMu.Unlock()
				l.s.SetHosts(h)
			}
		case proto.TypeHandover:
			signal(l.handover)
		case proto.TypeError:
			l.finish(remoteError(env))
			return
		}
	}
}

// signal wakes a waiter on ch without blocking; a signal already pending is
// enough.
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// RoleChanged receives a value whenever the active host sent a link_role.
func (l *Link) RoleChanged() <-chan struct{} { return l.roleCh }

// Handover receives a value when the active host hands its role to this host.
func (l *Link) Handover() <-chan struct{} { return l.handover }

// Hosts is the newest host list the active host sent, if any.
func (l *Link) Hosts() (proto.Hosts, bool) {
	l.evMu.Lock()
	defer l.evMu.Unlock()
	if l.hosts == nil {
		return proto.Hosts{}, false
	}
	return *l.hosts, true
}

// beat sends hb to the active host every heartbeatInterval until the link ends.
func (l *Link) beat() {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	var seq uint64
	for {
		select {
		case <-l.done:
			return
		case <-t.C:
			seq++
			if l.write(proto.TypeHeartbeat, proto.Heartbeat{Seq: seq}) != nil {
				return
			}
		}
	}
}

// applyLinkEntry appends an entry from the active host to the local chain and
// applies its effects, as handleChainAppend does for a client's entry. An
// entry already held (the warm session may have carried it first) is skipped
// if it is the same one. ErrConflict and ErrBadChain mean the chains diverged.
func (s *Server) applyLinkEntry(e chain.Entry) error {
	frames, err := chainFrames([]chain.Entry{e})
	if err != nil || len(frames) != 1 {
		return fmt.Errorf("server: link: cannot encode chain entry %d", e.Seq)
	}
	s.chainMu.Lock()
	defer s.chainMu.Unlock()
	if head := s.chain.Head(); e.Seq <= head.Seq {
		if e.Seq > 0 { // genesis is never resent; Seq 0 is a fork
			if mine := s.chain.Since(e.Seq - 1); len(mine) > 0 && string(mine[0].Sig) == string(e.Sig) {
				return nil
			}
		}
		return fmt.Errorf("server: link: entry %d: %w", e.Seq, chain.ErrConflict)
	}
	if err := s.chain.Append(e); err != nil {
		return fmt.Errorf("server: link: entry %d: %w", e.Seq, err)
	}
	s.refreshGroup()
	if s.applyHistoryLimits() {
		s.trimHistory()
	}
	s.chMu.Lock()
	dead := s.broadcastChainLocked(frames[0])
	s.chMu.Unlock()
	dropSlow(dead)
	if e.Op == chain.OpHostRemove || e.Op == chain.OpBan {
		s.dropRemovedLinks()
	}
	if e.Op == chain.OpBan {
		var b chain.FPBody
		if e.DecodeBody(&b) == nil {
			s.kick(b.Fingerprint)
		}
	}
	return nil
}

// mergeLinkMessages stores chat lines from the active host and lifts
// lastChatTS to the newest of them, so this host's own later lines sort after.
func (s *Server) mergeLinkMessages(msgs []proto.ChatMessage) {
	if len(msgs) == 0 || s.hist == nil {
		return
	}
	if _, err := s.hist.Merge(msgs); err != nil {
		s.logf("server: link history: %v", err)
		return
	}
	limit := s.clock().Add(maxStoredSkew)
	var newest time.Time
	for _, m := range msgs {
		if m.ID != "" && m.Channel != "" && !m.TS.After(limit) && m.TS.After(newest) {
			newest = m.TS
		}
	}
	s.chMu.Lock()
	if newest.After(s.lastChatTS) {
		s.lastChatTS = newest
	}
	s.chMu.Unlock()
}
