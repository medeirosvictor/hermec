package server

import (
	"encoding/json"
	"sort"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

// Voice membership state lives under Server.chMu, alongside the chat
// registry, so one lock covers every membership change. Lock order is
// unchanged: chMu is taken first, then a conn's sendMu (inside enqueue,
// which never blocks). Nothing here may block while chMu is held; conns that
// cannot accept a broadcast are collected and closed after unlock (dropSlow).
//
// Media sessions (sfu.go) hang off the same transitions: removeVoiceLocked and
// voiceJoin call into Server.sessions under chMu, and the session API they use
// only queues work for the session's own goroutine (chMu -> session.qmu is the
// single lock edge; session code never takes chMu).
//
// voiceSet maps channel name -> participant -> muted. A conn is in at most
// one voice channel; conn.voiceCh (guarded by chMu) records which.
type voiceSet map[string]map[*conn]bool

// channelType reports "text", "voice", or "" for an unknown channel.
func (s *Server) channelType(name string) string {
	for _, n := range s.cfg.Channels {
		if n == name {
			return "text"
		}
	}
	for _, n := range s.cfg.VoiceChannels {
		if n == name {
			return "voice"
		}
	}
	return ""
}

// channelInfos lists every channel, text first, with its type.
func (s *Server) channelInfos() []proto.ChannelInfo {
	out := make([]proto.ChannelInfo, 0, len(s.cfg.Channels)+len(s.cfg.VoiceChannels))
	for _, n := range s.cfg.Channels {
		out = append(out, proto.ChannelInfo{Name: n, Type: "text"})
	}
	for _, n := range s.cfg.VoiceChannels {
		out = append(out, proto.ChannelInfo{Name: n, Type: "voice"})
	}
	return out
}

// markAuthed registers c to receive server-wide voice_state broadcasts and,
// under the same chMu hold (so no change can slip between snapshot and
// registration), queues a snapshot of every occupied voice channel to c.
// auth_ok is already queued, so the snapshot follows it.
func (s *Server) markAuthed(c *conn) {
	s.chMu.Lock()
	s.authed[c] = struct{}{}
	slow := false
	for _, ch := range s.cfg.VoiceChannels {
		if len(s.voice[ch]) > 0 && !c.enqueue(s.voiceStateLocked(ch)) {
			slow = true
			break
		}
	}
	s.chMu.Unlock()
	if slow {
		dropSlow([]*conn{c})
	}
}

// voiceStateLocked encodes the current participants of ch. Caller holds chMu.
func (s *Server) voiceStateLocked(ch string) []byte {
	members := make([]proto.VoiceMember, 0, len(s.voice[ch]))
	for m, muted := range s.voice[ch] {
		members = append(members, proto.VoiceMember{Fingerprint: m.fingerprint, Name: m.name, Muted: muted})
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].Name != members[j].Name {
			return members[i].Name < members[j].Name
		}
		return members[i].Fingerprint < members[j].Fingerprint
	})
	raw, _ := proto.Encode(proto.TypeVoiceState, proto.VoiceState{Channel: ch, Members: members})
	return raw
}

// broadcastVoiceLocked sends ch's state to every authenticated connection.
// Caller holds chMu. Returns the conns that could not accept it.
func (s *Server) broadcastVoiceLocked(ch string) []*conn {
	raw := s.voiceStateLocked(ch)
	var dead []*conn
	for m := range s.authed {
		if !m.enqueue(raw) {
			dead = append(dead, m)
		}
	}
	return dead
}

// removeVoiceLocked drops c from its voice channel, if any, and returns the
// channel it left ("" if none). Caller holds chMu and broadcasts.
func (s *Server) removeVoiceLocked(c *conn) string {
	ch := c.voiceCh
	if ch == "" {
		return ""
	}
	delete(s.voice[ch], c)
	c.voiceCh = ""
	s.sessionLeaveLocked(ch, c) // may destroy the channel's media session
	return ch
}

// voiceJoin puts c in voice channel ch (moving it if already in another).
// Returns "" on success or an error code.
func (s *Server) voiceJoin(c *conn, ch string) string {
	if s.channelType(ch) != "voice" {
		return "bad_request"
	}
	if !s.cfg.Roles.Has(c.fingerprint, roles.PermJoinChannel) {
		return "forbidden"
	}
	s.chMu.Lock()
	var dead []*conn
	if c.voiceCh == ch {
		// Idempotent re-join: membership and mute state are kept.
		dead = s.broadcastVoiceLocked(ch)
	} else {
		prev := s.removeVoiceLocked(c)
		s.voice[ch][c] = false
		c.voiceCh = ch
		s.sessionJoinLocked(ch, c)
		if prev != "" {
			dead = append(dead, s.broadcastVoiceLocked(prev)...)
		}
		dead = append(dead, s.broadcastVoiceLocked(ch)...)
	}
	s.chMu.Unlock()
	dropSlow(dead)
	return ""
}

// voiceLeave removes c from its voice channel; a no-op outside a call.
func (s *Server) voiceLeave(c *conn) {
	s.chMu.Lock()
	var dead []*conn
	if ch := s.removeVoiceLocked(c); ch != "" {
		dead = s.broadcastVoiceLocked(ch)
	}
	s.chMu.Unlock()
	dropSlow(dead)
}

// voiceMute sets c's mute flag. Returns "not_joined" outside a call.
func (s *Server) voiceMute(c *conn, muted bool) string {
	s.chMu.Lock()
	ch := c.voiceCh
	if ch == "" {
		s.chMu.Unlock()
		return "not_joined"
	}
	s.voice[ch][c] = muted
	dead := s.broadcastVoiceLocked(ch)
	s.chMu.Unlock()
	dropSlow(dead)
	return ""
}

// routeVoice handles voice_* messages. It reports whether env was one.
func (c *conn) routeVoice(env proto.Envelope) bool {
	switch env.Type {
	case proto.TypeVoiceJoin:
		var m proto.VoiceJoin
		if json.Unmarshal(env.Data, &m) != nil {
			c.sendError("bad_request", "malformed voice_join")
			return true
		}
		switch c.srv.voiceJoin(c, m.Channel) {
		case "bad_request":
			c.sendError("bad_request", "not a voice channel: "+m.Channel)
		case "forbidden":
			c.sendError("forbidden", "not permitted to join channels")
		}
	case proto.TypeVoiceLeave:
		c.srv.voiceLeave(c)
	case proto.TypeVoiceMute:
		var m proto.VoiceMute
		if json.Unmarshal(env.Data, &m) != nil {
			c.sendError("bad_request", "malformed voice_mute")
			return true
		}
		if c.srv.voiceMute(c, m.Muted) == "not_joined" {
			c.sendError("not_joined", "not in a voice channel")
		}
	case proto.TypeRTCOffer, proto.TypeRTCAnswer, proto.TypeRTCCandidate:
		c.routeRTC(env)
	default:
		return false
	}
	return true
}

// routeRTC hands signaling to the channel's voice session. Membership is
// checked under chMu; the session only queues the work (non-blocking), so no
// pion call ever happens under chMu.
func (c *conn) routeRTC(env proto.Envelope) {
	c.srv.chMu.Lock()
	var vs *voiceSession
	if c.voiceCh != "" {
		vs = c.srv.sessions[c.voiceCh]
	}
	if vs != nil {
		vs.handleRTC(c, env)
	}
	c.srv.chMu.Unlock()
	if vs == nil {
		c.sendError("not_joined", "not in a voice channel")
	}
}
