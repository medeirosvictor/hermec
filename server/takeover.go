package server

import (
	"sync"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

// takeoverState is what takeover needs from the orchestrator.
type takeoverState struct {
	mu        sync.Mutex // serializes takeovers, so a promotion happens once
	alive     func(time.Time) bool
	standby   func() bool
	onPromote func()
}

// SetLinkView sets how takeover asks the host link whether the active host is
// alive and whether this host is the standby.
func (s *Server) SetLinkView(alive func(time.Time) bool, standby func() bool) {
	s.takeover.mu.Lock()
	s.takeover.alive, s.takeover.standby = alive, standby
	s.takeover.mu.Unlock()
}

// SetPromoteHook sets a function called once per promotion, after the warm
// sessions went live.
func (s *Server) SetPromoteHook(f func()) {
	s.takeover.mu.Lock()
	s.takeover.onPromote = f
	s.takeover.mu.Unlock()
}

// handleTakeover answers a client's takeover: it refuses unless this host is
// the standby and the active host is silent, else promotes (once; a takeover
// that finds the host already promoted just gets the session).
func (c *conn) handleTakeover() {
	s := c.srv
	if st := s.chain.State(); !st.IsHost(s.cfg.HostKey.PublicKey()) {
		c.sendError(proto.CodeHostNotAuthorized, "this host is no longer on the chain")
		return
	}
	t := &s.takeover
	t.mu.Lock()
	promotedNow := false
	if s.Mode() != ModeActive {
		if s.Mode() != ModeStandby || t.standby == nil || t.alive == nil || !t.standby() {
			t.mu.Unlock()
			c.sendError(proto.CodeNotStandby, "this host is not the standby")
			return
		}
		if t.alive(s.clock()) {
			t.mu.Unlock()
			c.sendError(proto.CodeActiveAlive, "the active host is alive")
			return
		}
		s.SetMode(ModeActive)
		s.promoted.Store(true)
		promotedNow = true
	} else if !s.promoted.Load() && !s.isWarm(c) {
		// Active from the start: there is nothing to take over.
		t.mu.Unlock()
		c.sendError(proto.CodeNotStandby, "this host is not the standby")
		return
	}
	// Idempotent: conns promoted earlier are no longer warm.
	s.PromoteWarm()
	if promotedNow {
		s.announcePromotion()
	}
	hook := t.onPromote
	t.mu.Unlock()
	if !promotedNow {
		c.sendMsg(proto.TypeHosts, s.hostsInfo())
	} else if hook != nil {
		// The announcement's hosts frame already reached c. The hook runs
		// outside the lock so it may call back into the server.
		hook()
	}
}

func (s *Server) isWarm(c *conn) bool {
	s.chMu.Lock()
	defer s.chMu.Unlock()
	_, ok := s.warm[c]
	return ok
}

// announcePromotion tells the authenticated conns who the active host is and
// adds the host-changed line to every text channel.
func (s *Server) announcePromotion() {
	s.chMu.Lock()
	var dead []*conn
	if raw, err := proto.Encode(proto.TypeHosts, s.hostsInfo()); err == nil {
		dead = s.broadcastAuthedLocked(raw)
	}
	for ch := range s.chans {
		dead = append(dead, s.systemChatLocked(ch, "host changed to "+s.cfg.HostName)...)
	}
	s.chMu.Unlock()
	dropSlow(dead)
}

// systemChatLocked adds a system line to ch's history and sends it to the
// channel's members and the links. Caller holds chMu.
func (s *Server) systemChatLocked(ch, text string) []*conn {
	id, err := newMessageID()
	if err != nil {
		s.logf("server: chat id: %v", err)
		return nil
	}
	// Same strictly increasing timestamps as chat.
	ts := time.Now().UTC()
	if !ts.After(s.lastChatTS) {
		ts = s.lastChatTS.Add(time.Nanosecond)
	}
	s.lastChatTS = ts
	msg := proto.ChatMessage{ID: id, Channel: ch, Text: text, TS: ts, System: true}
	if s.hist != nil {
		if herr := s.hist.Add(msg); herr != nil {
			s.logf("server: history: %v", herr)
		}
	}
	var dead []*conn
	if raw, err := proto.Encode(proto.TypeChatMessage, msg); err == nil {
		dead = s.broadcastLocked(ch, raw)
	}
	return append(dead, s.forwardChatLocked(msg)...)
}
