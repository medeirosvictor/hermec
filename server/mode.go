package server

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/medeirosvictor/hermec/core/proto"
)

// Mode is what a host does with client connections.
type Mode int

const (
	// ModeActive serves clients. It is the zero value, so a Config that does
	// not mention modes behaves as before.
	ModeActive Mode = iota
	// ModeStandby refuses full sessions and holds warm ones.
	ModeStandby
	// ModeIdle is a host that is not part of the pair; it behaves like standby
	// toward clients.
	ModeIdle
)

// Heartbeat timing. The grace is how long a receiver waits without an hb
// before calling the sender dead. Variables so tests can shorten them.
var (
	heartbeatInterval = 500 * time.Millisecond
	heartbeatGrace    = 1500 * time.Millisecond
)

// serverMode holds the current mode and the host list the orchestrator set.
type serverMode struct {
	mode  atomic.Int32
	hosts atomic.Pointer[proto.Hosts]
	hbSeq atomic.Uint64
}

// Mode returns the host's current mode.
func (s *Server) Mode() Mode { return Mode(s.modeState.mode.Load()) }

// SetMode switches the host's mode. Promoting warm sessions is the caller's
// job (promoteWarmLocked).
func (s *Server) SetMode(m Mode) { s.modeState.mode.Store(int32(m)) }

// SetHosts sets the host list a refused full auth is answered with.
func (s *Server) SetHosts(h proto.Hosts) { s.modeState.hosts.Store(&h) }

// hostsInfo is the host list for a hosts frame; with none set, this host alone.
func (s *Server) hostsInfo() proto.Hosts {
	if h := s.modeState.hosts.Load(); h != nil {
		return *h
	}
	return proto.Hosts{Active: proto.HostInfo{Fingerprint: s.chain.State().Founder}}
}

// promoteWarmLocked moves every warm conn into authed, as if it had just
// joined, and returns the conns that could not take the snapshot (the caller
// closes them with dropSlow after unlocking). Caller holds chMu.
func (s *Server) promoteWarmLocked() []*conn {
	var dead []*conn
	for c := range s.warm {
		delete(s.warm, c)
		dead = append(dead, s.markAuthedLocked(c)...)
	}
	return dead
}

// broadcastChainLocked sends a chain frame to every authenticated and warm
// connection. Caller holds chMu.
func (s *Server) broadcastChainLocked(raw []byte) []*conn {
	dead := s.broadcastAuthedLocked(raw)
	for m := range s.warm {
		if !m.enqueue(raw) {
			dead = append(dead, m)
		}
	}
	return dead
}

// heartbeatLoop sends hb every heartbeatInterval until Shutdown. The active
// host heartbeats authed and warm conns; any other mode only the warm ones.
func (s *Server) heartbeatLoop() {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stopHist:
			return
		case <-t.C:
			s.sendHeartbeat()
		}
	}
}

func (s *Server) sendHeartbeat() {
	raw, err := proto.Encode(proto.TypeHeartbeat, proto.Heartbeat{Seq: s.modeState.hbSeq.Add(1)})
	if err != nil {
		return
	}
	s.chMu.Lock()
	var dead []*conn
	if s.Mode() == ModeActive {
		dead = s.broadcastAuthedLocked(raw)
	}
	for m := range s.warm {
		if !m.enqueue(raw) {
			dead = append(dead, m)
		}
	}
	s.chMu.Unlock()
	dropSlow(dead)
}

// restricted reports whether c may only send the messages a standby or warm
// session is allowed.
func (s *Server) restricted(c *conn) bool {
	s.chMu.Lock()
	_, warm := s.warm[c]
	s.chMu.Unlock()
	return warm || s.Mode() != ModeActive
}

// allowedWhenRestricted reports whether a restricted conn may send typ.
func allowedWhenRestricted(typ string) bool {
	return typ == proto.TypeTakeover || typ == proto.TypeLinkProbe || strings.HasPrefix(typ, "warm_")
}
