package server

import (
	"crypto/rand"
	"encoding/json"
	"reflect"
	"sort"
	"time"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/server/dedicated"
)

// maxSyncFrame is the most bytes of messages one history link_sync carries,
// leaving room under MaxMessageSize for the envelope.
const maxSyncFrame = 60 * 1024

// LinkPeer is a host linked to this one.
type LinkPeer struct {
	Fingerprint string
	Name        string
	Addrs       []string
	Priority    int
	LastBeat    time.Time
	Prepared    bool // passive dedicated destination; not standby-capable
}

// Info describes the peer as a host entry.
func (p LinkPeer) Info() proto.HostInfo {
	return proto.HostInfo{Fingerprint: p.Fingerprint, Name: p.Name, Addrs: p.Addrs, Priority: p.Priority}
}

// linkState is what a link conn carries. Fields after hello are guarded by
// Server.chMu.
type linkState struct {
	// peerFingerprint is copied from the authenticated handshake. Certificate
	// authorization must bind to this immutable identity, not mutable hello.
	peerFingerprint string
	hello           proto.LinkHello
	lastBeat        time.Time
	standby         bool // the last link_role sent
	roleSent        bool // a link_role has been sent
	// dedicated and dedicatedHistory are one source-captured boundary for a
	// prepared destination. The copied history cannot be evicted mid-sync.
	dedicated        *proto.DedicatedReadiness
	dedicatedProof   *proto.DedicatedReadinessProof // verified Ready proof
	dedicatedHistory []proto.ChatMessage
}

// SelfInfo describes this host.
func (s *Server) SelfInfo() proto.HostInfo {
	st := s.chain.State()
	pub := s.cfg.HostKey.PublicKey()
	prio, _ := hostPriority(&st, pub)
	return proto.HostInfo{
		Fingerprint: identity.Fingerprint(pub),
		Name:        s.cfg.HostName,
		Addrs:       s.Advertise(),
		Priority:    prio,
	}
}

// hostPriority is the chain priority of the host with this key.
func hostPriority(st *chain.State, pub []byte) (int, bool) {
	for _, h := range st.Hosts {
		if string(h.PubKey) == string(pub) {
			return h.Priority, true
		}
	}
	return 0, false
}

// peerLocked describes link conn c. Caller holds chMu.
func peerLocked(c *conn, prio int) LinkPeer {
	return LinkPeer{
		Fingerprint: c.fingerprint,
		Name:        c.link.hello.Name,
		Addrs:       c.link.hello.Addrs,
		Priority:    prio,
		LastBeat:    c.link.lastBeat,
		Prepared:    c.link.hello.Prepared,
	}
}

// Links returns the hosts linked to this one, best priority first.
func (s *Server) Links() []LinkPeer {
	st := s.chain.State()
	s.chMu.Lock()
	defer s.chMu.Unlock()
	out := make([]LinkPeer, 0, len(s.links))
	for c := range s.links {
		prio, _ := hostPriority(&st, c.pub)
		out = append(out, peerLocked(c, prio))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	return out
}

// pickStandbyLocked returns the linked host that should be standby: the best
// priority among those that at least half of the authenticated clients can
// reach (a client that has not reported counts as reaching it), or nil.
func (s *Server) pickStandbyLocked() *LinkPeer {
	st := s.chain.State()
	var best *LinkPeer
	for c := range s.links {
		prio, ok := hostPriority(&st, c.pub)
		if !ok || c.link.hello.Yield != nil || c.link.hello.Prepared { // merge and prepared links cannot stand by
			continue
		}
		bad := 0
		for a := range s.authed {
			if r, seen := a.reach[c.fingerprint]; seen && !r {
				bad++
			}
		}
		if bad*2 > len(s.authed) {
			continue
		}
		p := peerLocked(c, prio)
		if best == nil || p.Priority < best.Priority ||
			(p.Priority == best.Priority && p.Fingerprint < best.Fingerprint) {
			best = &p
		}
	}
	return best
}

// updateStandbyLocked recomputes the standby after a link or reachability
// change: it tells each link its role and, when the pick changed, sends hosts
// to every authenticated client. Caller holds chMu and closes the returned
// conns after unlocking.
func (s *Server) updateStandbyLocked() []*conn {
	if s.shutdown.Load() { // our links close before our clients: say nothing
		return nil
	}
	pick := s.pickStandbyLocked()
	var dead []*conn
	for c := range s.links {
		want := pick != nil && pick.Fingerprint == c.fingerprint
		if c.link.roleSent && c.link.standby == want {
			continue
		}
		c.link.roleSent, c.link.standby = true, want
		if !c.sendMsg(proto.TypeLinkRole, proto.LinkRole{Standby: want}) {
			dead = append(dead, c)
		}
	}
	var next *proto.HostInfo
	if pick != nil {
		info := pick.Info()
		next = &info
	}
	if cur := s.standby.Load(); reflect.DeepEqual(cur, next) {
		return dead
	}
	s.standby.Store(next)
	if raw, err := proto.Encode(proto.TypeHosts, proto.Hosts{Active: s.SelfInfo(), Standby: next}); err == nil {
		dead = append(dead, s.broadcastAuthedLocked(raw)...)
		for c := range s.links {
			if !c.enqueue(raw) {
				dead = append(dead, c)
			}
		}
	}
	return dead
}

// handleLinkHello turns a warm host conn into a link: it streams the chain
// after the sender's head, the password key and the chat history after its
// newest line, and from then on every chain entry and chat line.
func (c *conn) handleLinkHello(env proto.Envelope) {
	s := c.srv
	var h proto.LinkHello
	if json.Unmarshal(env.Data, &h) != nil {
		c.sendError(proto.CodeBadRequest, "malformed link_hello")
		return
	}
	if c.refuseBuild(h.Build) {
		return
	}
	if code, msg := s.registerLink(c, h); code != "" {
		c.sendError(code, msg)
		return
	}
	if h.Prepared {
		s.streamDedicatedHistory(c)
		return
	}
	s.streamHistory(c, h.HistoryLastTS, h.HistoryLastID)
}

// registerLink checks that c may link and, if so, makes it a link and queues
// the chain after h.ChainSeq and the password key. It returns an error code and
// message on refusal. The checks and the registration share one chainMu hold,
// so a host_remove cannot slip between them; chainMu is not held while the
// history is paged.
func (s *Server) registerLink(c *conn, h proto.LinkHello) (code, msg string) {
	// A readiness capture cannot be crossed by a source mutation or demotion.
	s.dedicatedMu.Lock()
	defer s.dedicatedMu.Unlock()
	// A split-brain loser sends its load; if we would lose to it now, the merge
	// is wrong way round and we refuse.
	if h.Yield != nil {
		if s.Loses(*h.Yield) {
			return proto.CodeNotActive, "this host would yield to you"
		}
	}
	// chainMu first, like joinLive: no entry is appended between the frames
	// below and the first broadcast this link receives.
	s.chainMu.Lock()
	defer s.chainMu.Unlock()
	st := s.chain.State()
	if !st.IsHost(c.pub) || st.Banned(c.fingerprint) {
		return proto.CodeForbidden, "only hosts on the chain may link"
	}
	if s.Mode() == ModeFenced {
		// Recovery is deliberately narrow: only the destination named by the
		// durable certificate may reconnect, and it receives that exact object.
		r, err := dedicated.Load(s.cfg.DedicatedTransitionPath)
		if err != nil || r.State != dedicated.Fenced || r.Certificate == nil || r.Certificate.Destination.Fingerprint != c.fingerprint {
			return proto.CodeNotActive, "this host is fenced"
		}
		s.chMu.Lock()
		if _, ok := s.warm[c]; !ok {
			s.chMu.Unlock()
			return proto.CodeNotActive, "this host is fenced"
		}
		delete(s.warm, c)
		if c.warmHeld.Swap(false) {
			s.releaseWarmSlot()
		}
		c.link = &linkState{peerFingerprint: c.fingerprint, hello: h, lastBeat: s.clock()}
		s.links[c] = struct{}{}
		ok := c.sendMsg(proto.TypeDedicatedCommit, proto.DedicatedCommit{Certificate: *r.Certificate})
		s.chMu.Unlock()
		if !ok {
			c.closeSend()
		}
		return "", ""
	}
	if s.Mode() != ModeActive {
		return proto.CodeNotActive, "this host is not serving clients"
	}
	if h.ChainSeq > s.chain.Head().Seq {
		return proto.CodeBadChain, "your chain is ahead of this host's"
	}
	frames, err := chainFrames(s.chain.Since(h.ChainSeq))
	if err != nil {
		return proto.CodeBadRequest, "cannot encode chain"
	}
	id := s.chain.ID()
	keyFrame, err := proto.Encode(proto.TypeLinkSync, proto.LinkSync{PasswordKey: s.passwordKey(id[:]), More: true})
	if err != nil {
		return proto.CodeBadRequest, "cannot encode key"
	}

	// Keep all prepared-readiness returns inside this scope: validation and
	// nonce generation happen while chMu protects the captured boundary.
	var dead []*conn
	code, msg = func() (string, string) {
		s.chMu.Lock()
		defer s.chMu.Unlock()
		if _, ok := s.warm[c]; !ok {
			return proto.CodeNotActive, "this host is not serving clients"
		}
		// A link counts against neither cap.
		delete(s.warm, c)
		if c.warmHeld.Swap(false) {
			s.releaseWarmSlot()
		}
		c.link = &linkState{peerFingerprint: c.fingerprint, hello: h, lastBeat: s.clock()}
		if h.Prepared {
			var snapshot []proto.ChatMessage
			ts, id := time.Time{}, ""
			if s.hist != nil {
				snapshot, ts, id = s.hist.Snapshot()
			}
			head := s.chain.Head()
			headHash := head.Hash()
			historyCount, historyHash := proto.DedicatedHistoryCommitment(snapshot)
			readiness := proto.DedicatedReadiness{ServerID: s.chain.ID().String(), DestinationFingerprint: c.fingerprint, ChainHead: head.Seq, ChainHeadHash: headHash[:], HistoryLastID: id, HistoryLastUnixMilli: ts.UnixMilli(), HistoryCount: historyCount, HistoryHash: historyHash}
			if h.Dedicated != nil {
				// A restart may only resume the exact boundary the source durably
				// attested; it cannot nominate a replacement nonce.
				readiness.TransitionNonce = append([]byte(nil), h.Dedicated.TransitionNonce...)
				if h.Dedicated.Validate() != nil || !dedicated.SameReadiness(*h.Dedicated, readiness) || s.checkDedicatedReadinessAttestation(readiness, true) != nil {
					return proto.CodeBadRequest, "dedicated preparation boundary changed"
				}
			} else {
				nonce := make([]byte, proto.DedicatedTransitionNonceSize)
				if _, err := rand.Read(nonce); err != nil {
					return proto.CodeBadRequest, "cannot create preparation nonce"
				}
				readiness.TransitionNonce = nonce
				if s.checkDedicatedReadinessAttestation(readiness, false) != nil {
					return proto.CodeBadRequest, "dedicated preparation differs from durable source attestation"
				}
			}
			c.link.dedicated = &readiness
			c.link.dedicatedHistory = snapshot
		}
		s.links[c] = struct{}{}
		ok := true
		for _, f := range frames {
			ok = ok && c.enqueue(f)
		}
		ok = ok && c.enqueue(keyFrame)
		if ok {
			dead = s.updateStandbyLocked()
			if !c.sendMsg(proto.TypeHosts, proto.Hosts{Active: s.SelfInfo(), Standby: s.standby.Load()}) {
				dead = append(dead, c)
			}
		} else {
			dead = append(dead, c)
		}
		return "", ""
	}()
	if code != "" {
		return code, msg
	}
	dropSlow(dead)
	return "", ""
}

// streamDedicatedHistory sends from the beginning so a bootstrap destination
// cannot hide a partial local history.  The proof challenge follows the final
// history frame, preserving the source-captured transfer boundary.
func (s *Server) streamDedicatedHistory(c *conn) {
	s.chMu.Lock()
	r := c.link.dedicated
	snapshot := append([]proto.ChatMessage(nil), c.link.dedicatedHistory...)
	s.chMu.Unlock()
	if r == nil || !streamHistorySnapshot(c, snapshot) {
		return
	}
	_ = c.sendMsg(proto.TypeDedicatedPrepare, proto.DedicatedPrepare{Readiness: *r})
}

// streamHistorySnapshot pages an immutable capture, rather than repeatedly
// reading History while its retention policy may evict early pages.
func streamHistorySnapshot(c *conn, msgs []proto.ChatMessage) bool {
	for len(msgs) > 0 {
		raw, n := encodeLinkSync(msgs, false)
		if raw == nil || n == 0 || !c.enqueueWait(raw) {
			return false
		}
		msgs = msgs[n:]
	}
	return true
}

// streamHistory sends c the chat lines after (ts, id), paged to fit a frame;
// the last frame has More unset. Lines accepted meanwhile also arrive live, so
// the receiver sees some twice and merges by id.
func (s *Server) streamHistory(c *conn, ts time.Time, id string) {
	for {
		var msgs []proto.ChatMessage
		more := false
		if s.hist != nil {
			msgs, more = s.hist.After(ts, id, historyPage)
		}
		raw, n := encodeLinkSync(msgs, more)
		if n > 0 {
			ts, id = msgs[n-1].TS, msgs[n-1].ID
		}
		if raw == nil || !c.enqueueWait(raw) {
			return
		}
		if n == len(msgs) && !more {
			return
		}
	}
}

// encodeLinkSync encodes as many leading msgs as fit maxSyncFrame as a
// link_sync, with More set if any are left out or more already; it returns the
// frame and how many msgs it holds.
func encodeLinkSync(msgs []proto.ChatMessage, more bool) ([]byte, int) {
	size, n := 0, 0
	for _, m := range msgs {
		l, err := encodedLen(m)
		if err != nil || (n > 0 && size+l+1 > maxSyncFrame) {
			break
		}
		size += l + 1
		n++
	}
	raw, err := proto.Encode(proto.TypeLinkSync, proto.LinkSync{Messages: msgs[:n], More: more || n < len(msgs)})
	if err != nil {
		return nil, 0
	}
	return raw, n
}

// syncHeadroom is how many send slots bulk history leaves free for live
// frames (hb, chain, chat), which are enqueued without waiting.
const syncHeadroom = 16

// enqueueWaitTimeout is how long enqueueWait waits for room. A variable so
// tests can shorten it.
var enqueueWaitTimeout = writeTimeout

// enqueueWait is enqueue for bulk history: it waits (up to enqueueWaitTimeout) until
// the buffer has syncHeadroom slots free, so live frames are not refused; it
// reports false if c is closed or stays full.
func (c *conn) enqueueWait(raw []byte) bool {
	timeout := time.NewTimer(enqueueWaitTimeout)
	defer timeout.Stop()
	for {
		c.sendMu.Lock()
		closed := c.closed
		room := len(c.send) <= cap(c.send)-syncHeadroom
		c.sendMu.Unlock()
		if closed {
			return false
		}
		if room && c.enqueue(raw) {
			return true
		}
		select {
		case <-c.drained:
		case <-timeout.C:
			return false
		}
	}
}

// forwardChatLocked sends a newly accepted chat line to every link. Caller
// holds chMu; it returns the conns that could not take it.
func (s *Server) forwardChatLocked(msg proto.ChatMessage) []*conn {
	if len(s.links) == 0 {
		return nil
	}
	raw, err := proto.Encode(proto.TypeLinkSync, proto.LinkSync{Messages: []proto.ChatMessage{msg}})
	if err != nil {
		return nil
	}
	var dead []*conn
	for c := range s.links {
		if !c.enqueue(raw) {
			dead = append(dead, c)
		}
	}
	return dead
}

// dropRemovedLinks closes the links of hosts that are no longer on the chain
// or are banned.
func (s *Server) dropRemovedLinks() {
	st := s.chain.State()
	s.chMu.Lock()
	var hit []*conn
	for c := range s.links {
		if !st.IsHost(c.pub) || st.Banned(c.fingerprint) {
			hit = append(hit, c)
		}
	}
	s.chMu.Unlock()
	for _, c := range hit {
		c.closeSend()
	}
}

// routeLink handles a message from a link conn.
func (c *conn) routeLink(env proto.Envelope) {
	switch env.Type {
	case proto.TypeHeartbeat:
		c.srv.chMu.Lock()
		c.link.lastBeat = c.srv.clock()
		c.srv.chMu.Unlock()
	case proto.TypeLinkSync:
		c.handleLinkSyncIn(env)
	case proto.TypeChain:
		c.handleLinkChainIn(env)
	case proto.TypeLinkProbe:
		c.handleLinkProbe()
	case proto.TypeDedicatedProof:
		c.handleDedicatedProof(env)
	default:
		c.sendError(proto.CodeBadRequest, "unsupported message type "+env.Type)
	}
}

// handleLinkProbe answers a host's link_probe with this host's state.
func (c *conn) handleLinkProbe() {
	s := c.srv
	st := s.chain.State()
	if !st.IsHost(c.pub) {
		c.sendError(proto.CodeForbidden, "only hosts on the chain may probe")
		return
	}
	self := s.SelfInfo()
	voice, clients := s.load()
	c.sendMsg(proto.TypeLinkProbeReply, proto.LinkProbeReply{
		Active:       s.Mode() == ModeActive,
		ServerID:     s.chain.ID().String(),
		VoiceMembers: voice,
		Clients:      clients,
		Priority:     self.Priority,
		Self:         self,
	})
}

// handleReach records a client's report on whether it can reach a host and
// re-picks the standby.
func (c *conn) handleReach(env proto.Envelope) {
	s := c.srv
	var r proto.Reach
	if json.Unmarshal(env.Data, &r) != nil || r.Fingerprint == "" {
		c.sendError(proto.CodeBadRequest, "malformed reach")
		return
	}
	st := s.chain.State()
	known := false
	for _, h := range st.Hosts {
		known = known || h.Fingerprint == r.Fingerprint
	}
	if !known {
		return // only hosts matter; this keeps the map bounded
	}
	s.chMu.Lock()
	if c.reach == nil {
		c.reach = make(map[string]bool)
	}
	c.reach[r.Fingerprint] = r.OK
	dead := s.updateStandbyLocked()
	s.chMu.Unlock()
	dropSlow(dead)
}
