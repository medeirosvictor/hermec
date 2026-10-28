package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/hshake"
	"github.com/medeirosvictor/hermec/core/identity"
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

	prepareMu  sync.Mutex
	prepared   *proto.DedicatedReadiness
	prepareAck chan error // exactly one result for a dedicated preparation

	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	err  error // why the link ended; nil for a deliberate Close
}

// dialHost opens a warm, password-less session to the host at url with s's
// host key, checking that it serves our chain and a head of at least minSeq.
func dialHost(ctx context.Context, url string, s *Server, minSeq uint64) (*websocket.Conn, error) {
	dialer := s.hostDialer()
	ws, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("server: dial host: %w", err)
	}
	name := s.cfg.HostName
	if name == "" {
		name = "host"
	}
	res, err := hshake.Run(ctx, ws, s.cfg.HostKey, name, "", hshake.Opts{Pin: s.chain.ID().String(), Warm: true, MinChainSeq: minSeq})
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
	ws, err := dialHost(ctx, url, s, s.chain.Head().Seq) // never follow a host behind our chain
	if err != nil {
		return nil, err
	}
	l := &Link{s: s, ws: ws, done: make(chan struct{}), roleCh: make(chan struct{}, 1), handover: make(chan struct{}, 1), prepareAck: make(chan error, 1)}
	l.lastBeat.Store(time.Now().UnixNano())
	hello := proto.LinkHello{
		ChainSeq: s.chain.Head().Seq,
		Addrs:    s.advertise(),
		Name:     s.cfg.HostName,
		Prepared: s.Prepared(),
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
// A host whose chain is behind ours does not answer.
func ProbeHost(ctx context.Context, url string, s *Server) (proto.LinkProbeReply, error) {
	return probeHost(ctx, url, s, s.chain.Head().Seq)
}

// probeHost is ProbeHost accepting a host whose chain head is at least minSeq;
// a split-brain probe passes 0, for the other side may be behind or diverged.
func probeHost(ctx context.Context, url string, s *Server, minSeq uint64) (proto.LinkProbeReply, error) {
	var reply proto.LinkProbeReply
	ws, err := dialHost(ctx, url, s, minSeq)
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
	err = readFrames(ws, "probe", func(env proto.Envelope) (bool, error) {
		if env.Type != proto.TypeLinkProbeReply {
			return false, nil
		}
		if err := json.Unmarshal(env.Data, &reply); err != nil {
			return true, fmt.Errorf("server: probe: bad reply: %w", err)
		}
		return true, nil
	})
	return reply, err
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
	l.finish(readFrames(l.ws, "link", func(env proto.Envelope) (bool, error) {
		switch env.Type {
		case proto.TypeHeartbeat:
			l.lastBeat.Store(time.Now().UnixNano())
		case proto.TypeChain:
			es, err := decodeChainFrame(env)
			for _, e := range es {
				if err != nil {
					break
				}
				err = l.s.applyLinkEntry(e)
			}
			return false, err
		case proto.TypeLinkSync:
			var ls proto.LinkSync
			if json.Unmarshal(env.Data, &ls) != nil {
				return false, errors.New("server: link: malformed link_sync")
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
			if json.Unmarshal(env.Data, &r) != nil {
				l.s.logf("server: link: malformed link_role ignored")
				break
			}
			l.standby.Store(r.Standby)
			signal(l.roleCh)
		case proto.TypeHosts:
			var h proto.Hosts
			if json.Unmarshal(env.Data, &h) != nil {
				l.s.logf("server: link: malformed hosts ignored")
				break
			}
			l.evMu.Lock()
			l.hosts = &h
			l.evMu.Unlock()
			l.s.SetHosts(h)
		case proto.TypeHandover:
			signal(l.handover)
		case proto.TypeError:
			var e proto.ErrorMsg
			if json.Unmarshal(env.Data, &e) != nil {
				return false, errors.New("server: link: malformed error")
			}
			return false, fmt.Errorf("server: link refused: %s: %s", e.Code, e.Message)
		case proto.TypeDedicatedPrepare:
			var p proto.DedicatedPrepare
			if json.Unmarshal(env.Data, &p) != nil || p.Readiness.Validate() != nil {
				return false, errors.New("server: link: malformed dedicated preparation")
			}
			if p.Readiness.ServerID != l.s.chain.ID().String() || p.Readiness.DestinationFingerprint != identity.Fingerprint(l.s.cfg.HostKey.PublicKey()) {
				return false, errors.New("server: link: dedicated preparation names another destination")
			}
			// A source that changed while this link was catching up must retry;
			// never sign an apparently complete but mixed boundary.
			ts, id := time.Time{}, ""
			if l.s.hist != nil {
				ts, id = l.s.hist.Last()
			}
			head := l.s.chain.Head()
			headHash := head.Hash()
			snapshot, _, _ := l.s.hist.Snapshot()
			historyCount, historyHash := proto.DedicatedHistoryCommitment(snapshot)
			if head.Seq != p.Readiness.ChainHead || string(headHash[:]) != string(p.Readiness.ChainHeadHash) || id != p.Readiness.HistoryLastID || ts.UnixMilli() != p.Readiness.HistoryLastUnixMilli || historyCount != p.Readiness.HistoryCount || string(historyHash) != string(p.Readiness.HistoryHash) {
				return false, errors.New("server: link: dedicated boundary did not arrive intact")
			}
			sig, err := proto.SignDedicatedReadiness(l.s.cfg.HostKey, p.Readiness)
			if err != nil {
				return false, err
			}
			l.prepareMu.Lock()
			l.prepared = &p.Readiness
			l.prepareMu.Unlock()
			if err := l.write(proto.TypeDedicatedProof, proto.DedicatedReadinessProof{Readiness: p.Readiness, Signature: sig}); err != nil {
				return false, err
			}
		case proto.TypeDedicatedAck:
			var ack proto.DedicatedAck
			if json.Unmarshal(env.Data, &ack) != nil || ack.Readiness.Validate() != nil {
				return false, errors.New("server: link: malformed dedicated acknowledgement")
			}
			l.prepareMu.Lock()
			ok := l.prepared != nil && reflect.DeepEqual(*l.prepared, ack.Readiness)
			l.prepareMu.Unlock()
			if !ok {
				return false, errors.New("server: link: replayed dedicated acknowledgement")
			}
			select {
			case l.prepareAck <- nil:
			default:
			}
		}
		return false, nil
	}))
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

// AwaitDedicatedReadiness waits for the source's acknowledgement of this
// link's nonce-bound readiness proof. It is only useful for a prepared link.
func (l *Link) AwaitDedicatedReadiness(ctx context.Context) error {
	select {
	case err := <-l.prepareAck:
		return err
	case <-l.done:
		if err := l.Err(); err != nil {
			return err
		}
		return errors.New("server: dedicated link closed before acknowledgement")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// PersistDedicatedPreparation writes the restart-safe Prepared record only
// after the active source acknowledged the exact signed boundary.
func (l *Link) PersistDedicatedPreparation(ctx context.Context, path string) error {
	if err := l.AwaitDedicatedReadiness(ctx); err != nil {
		return err
	}
	l.prepareMu.Lock()
	r := l.prepared
	l.prepareMu.Unlock()
	if r == nil {
		return errors.New("server: missing acknowledged dedicated readiness")
	}
	return SaveDedicatedTransition(path, DedicatedTransitionRecord{
		Version: 1, State: DedicatedTransitionPrepared, ServerID: r.ServerID,
		DestinationFingerprint: r.DestinationFingerprint, ChainHead: r.ChainHead, ChainHeadHash: append([]byte(nil), r.ChainHeadHash...),
		HistoryLastID: r.HistoryLastID, HistoryLastUnixMilli: r.HistoryLastUnixMilli, HistoryCount: r.HistoryCount, HistoryHash: append([]byte(nil), r.HistoryHash...),
		TransitionNonce: append([]byte(nil), r.TransitionNonce...), Recovery: DedicatedRecoveryRetryPreparation,
	})
}

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
	s.dedicatedMu.Lock()
	defer s.dedicatedMu.Unlock()
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
	s.afterChainChange()
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
	// A dedicated snapshot must not race a merge-link history mutation.
	s.dedicatedMu.Lock()
	defer s.dedicatedMu.Unlock()
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
