package server

import (
	"encoding/json"

	"github.com/medeirosvictor/hermec/core/chain"
	"github.com/medeirosvictor/hermec/core/hshake"
	"github.com/medeirosvictor/hermec/core/identity"
	"github.com/medeirosvictor/hermec/core/proto"
)

// chainLostMsg is what an author is told when a split-brain merge dropped a
// chain entry of theirs.
const chainLostMsg = "your change was lost during a network split; please redo it"

// measureLoad is the host load for the yield rule: voice members and clients.
func (s *Server) measureLoad() (voice, clients int) {
	s.chMu.Lock()
	defer s.chMu.Unlock()
	for _, set := range s.voice {
		voice += len(set)
	}
	return voice, len(s.authed)
}

// handleLinkSyncIn merges chat lines a linked host sends us (a host that lost
// a split-brain sends its history this way) and passes them on to our other
// links.
func (c *conn) handleLinkSyncIn(env proto.Envelope) {
	s := c.srv
	var ls proto.LinkSync
	if json.Unmarshal(env.Data, &ls) != nil {
		c.sendError(proto.CodeBadRequest, "malformed link_sync")
		return
	}
	if len(ls.Messages) == 0 {
		return
	}
	s.mergeLinkMessages(ls.Messages)
	raw, err := proto.Encode(proto.TypeLinkSync, proto.LinkSync{Messages: ls.Messages})
	if err != nil {
		return
	}
	s.chMu.Lock()
	var dead []*conn
	for o := range s.links {
		if o != c && !o.enqueue(raw) {
			dead = append(dead, o)
		}
	}
	s.chMu.Unlock()
	dropSlow(dead)
}

// sendHandover asks the linked host with this fingerprint to become active.
func (s *Server) sendHandover(fp string) bool {
	s.chMu.Lock()
	defer s.chMu.Unlock()
	for c := range s.links {
		if c.fingerprint == fp {
			return c.sendMsg(proto.TypeHandover, proto.Handover{})
		}
	}
	return false
}

// Promote makes this host active as a takeover would, without the active_alive
// check: warm sessions go live and everyone is told the host changed. It
// reports false if the host was already active.
func (s *Server) Promote() bool {
	t := &s.takeover
	t.mu.Lock()
	defer t.mu.Unlock()
	if s.Mode() == ModeActive {
		return false
	}
	s.SetMode(ModeActive)
	s.promoted.Store(true)
	s.PromoteWarm()
	s.announcePromotion()
	return true
}

// setFollowerMode sets m (standby or idle) unless the host is active, which a
// takeover may have made it meanwhile; it reports whether it set m.
func (s *Server) setFollowerMode(m Mode) bool {
	t := &s.takeover
	t.mu.Lock()
	defer t.mu.Unlock()
	if s.Mode() == ModeActive {
		return false
	}
	s.SetMode(m)
	return true
}

// resumeActive undoes goIdle when a handover or merge is called off, restoring
// whether the host counted as promoted.
func (s *Server) resumeActive(promoted bool) {
	t := &s.takeover
	t.mu.Lock()
	s.SetMode(ModeActive)
	s.promoted.Store(promoted)
	t.mu.Unlock()
}

// handleLinkChainIn appends chain entries a linked host sends that extend our
// head (a merge loser whose chain is a prefix-extension of ours). Entries that
// do not extend it are dropped.
func (c *conn) handleLinkChainIn(env proto.Envelope) {
	var cf proto.ChainFrame
	if json.Unmarshal(env.Data, &cf) != nil {
		c.sendError(proto.CodeBadRequest, "malformed chain frame")
		return
	}
	es, err := hshake.DecodeEntries(cf.Entries)
	if err != nil {
		c.sendError(proto.CodeBadRequest, "malformed chain frame")
		return
	}
	for _, e := range es {
		if err := c.srv.applyLinkEntry(e); err != nil {
			c.srv.logf("server: link chain entry %d: %v", e.Seq, err)
			return
		}
	}
}

// goIdle sets ModeIdle. chainMu is held so that no chain append lands after it
// returns.
func (s *Server) goIdle() {
	s.chainMu.Lock()
	s.SetMode(ModeIdle)
	s.chainMu.Unlock()
}

// Demote leaves the active role: the host goes idle, then every authenticated
// client is told (chain_lost first for those in lostAuthors, by fingerprint)
// to move to `to` and is disconnected once that is flushed, and served host
// links are dropped so their peers look for the new active host. Nothing is
// left connected to serve a client on a host that no longer does.
func (s *Server) Demote(to proto.HostInfo, reason string, lostAuthors map[string]bool) {
	s.goIdle()
	s.chMu.Lock()
	clients := make([]*conn, 0, len(s.authed))
	for c := range s.authed {
		clients = append(clients, c)
	}
	links := make([]*conn, 0, len(s.links))
	for c := range s.links {
		links = append(links, c)
	}
	s.chMu.Unlock()
	for _, c := range clients {
		if lostAuthors[c.fingerprint] {
			c.sendError(proto.CodeChainLost, chainLostMsg)
		}
		c.sendMsg(proto.TypeMove, proto.Move{To: to, Reason: reason})
		c.closeSend()
	}
	for _, c := range links {
		c.closeSend()
	}
}

// lostAuthors returns the fingerprints of the authors of our chain entries
// that the chain `theirs` lacks (compared by hash), for the entries after the
// two chains' common prefix.
func (s *Server) lostAuthors(theirs []chain.Entry) map[string]bool {
	have := make(map[[32]byte]bool, len(theirs))
	for _, e := range theirs {
		have[e.Hash()] = true
	}
	out := make(map[string]bool)
	for _, e := range s.chain.Entries() {
		if !have[e.Hash()] {
			out[identity.Fingerprint(e.Author)] = true
		}
	}
	return out
}
