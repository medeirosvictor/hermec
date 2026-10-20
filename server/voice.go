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
	var dead []*conn
	if slow {
		dead = append(dead, c)
	}
	// The roster goes to everyone, c included, after c's voice snapshot.
	dead = append(dead, s.broadcastAuthedLocked(s.rosterLocked())...)
	s.chMu.Unlock()
	dropSlow(dead)
}

// voiceStateLocked encodes the current participants of ch. Caller holds chMu.
func (s *Server) voiceStateLocked(ch string) []byte {
	members := make([]proto.VoiceMember, 0, len(s.voice[ch]))
	for m, muted := range s.voice[ch] {
		members = append(members, proto.VoiceMember{Fingerprint: m.fingerprint, Name: m.name, Muted: muted, Sharing: m.sharing})
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
	return s.broadcastAuthedLocked(s.voiceStateLocked(ch))
}

// broadcastAuthedLocked sends raw to every authenticated connection. Caller
// holds chMu. Returns the conns that could not accept it.
func (s *Server) broadcastAuthedLocked(raw []byte) []*conn {
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
	s.clearShareLocked(ch, c)
	c.watching = ""
	delete(s.voice[ch], c)
	c.voiceCh = ""
	s.sessionLeaveLocked(ch, c) // may destroy the channel's media session
	return ch
}

// clearShareLocked clears c's sharing flag and the watch of every viewer of c
// in ch. The session side (video tracks) is cleared by the caller's path:
// shareSet posts it, and a leaving peer's forwarders die with its media.
// Caller holds chMu.
func (s *Server) clearShareLocked(ch string, c *conn) {
	c.sharing = false
	for m := range s.voice[ch] {
		if m.watching == c.fingerprint {
			m.watching = ""
		}
	}
}

// shareSet sets or clears c's sharing flag and re-broadcasts the channel's
// voice_state. Outside a call it is ignored.
func (s *Server) shareSet(c *conn, on bool) {
	s.chMu.Lock()
	ch := c.voiceCh
	if ch == "" {
		s.chMu.Unlock()
		return
	}
	if on {
		c.sharing = true
		if vs := s.sessions[ch]; vs != nil {
			vs.shareStarted(c)
		}
	} else {
		s.clearShareLocked(ch, c)
		if vs := s.sessions[ch]; vs != nil {
			vs.shareStopped(c)
		}
	}
	dead := s.broadcastVoiceLocked(ch)
	s.chMu.Unlock()
	dropSlow(dead)
}

// watch records that c watches the sharer with fingerprint fp ("" stops).
// Returns "" on success, "not_joined" outside a call, or "not_sharing" when
// fp is not sharing in c's channel.
func (s *Server) watch(c *conn, fp string) string {
	s.chMu.Lock()
	defer s.chMu.Unlock()
	ch := c.voiceCh
	if ch == "" {
		return "not_joined"
	}
	vs := s.sessions[ch]
	if fp == "" {
		c.watching = ""
		if vs != nil {
			vs.setWatch(c, nil)
		}
		return ""
	}
	if c.watching == fp {
		return "" // already watching: no renegotiation, no extra keyframe
	}
	var target *conn
	for m := range s.voice[ch] {
		if m != c && m.sharing && m.fingerprint == fp {
			target = m
			break
		}
	}
	if target == nil || vs == nil {
		return proto.CodeNotSharing
	}
	c.watching = fp
	vs.setWatch(c, target)
	return ""
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
		if len(s.voice[ch]) >= s.maxVoice() {
			s.chMu.Unlock()
			return proto.CodeVoiceFull
		}
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
	if s.voice[ch][c] == muted {
		s.chMu.Unlock()
		return "" // unchanged: nothing to tell anyone
	}
	s.voice[ch][c] = muted
	dead := s.broadcastVoiceLocked(ch)
	s.chMu.Unlock()
	dropSlow(dead)
	return ""
}

// routeVoice handles voice_* messages. It reports whether env was one.
func (c *conn) routeVoice(env proto.Envelope) bool {
	if c.srv.cfg.LegacyHost {
		switch env.Type {
		case proto.TypeShareStart, proto.TypeShareStop, proto.TypeWatch:
			return false // a v0.4 host does not know these
		}
	}
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
		case proto.CodeVoiceFull:
			c.sendError(proto.CodeVoiceFull, "voice channel is full")
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
	case proto.TypeShareStart:
		c.srv.shareSet(c, true)
	case proto.TypeShareStop:
		c.srv.shareSet(c, false)
	case proto.TypeWatch:
		var m proto.Watch
		if json.Unmarshal(env.Data, &m) != nil {
			c.sendError("bad_request", "malformed watch")
			return true
		}
		switch c.srv.watch(c, m.Fingerprint) {
		case "not_joined":
			c.sendError("not_joined", "not in a voice channel")
		case proto.CodeNotSharing:
			c.sendError(proto.CodeNotSharing, "that member is not sharing in your channel")
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

// maxVoice is the effective per-channel participant cap.
func (s *Server) maxVoice() int {
	if s.cfg.MaxVoice > 0 {
		return s.cfg.MaxVoice
	}
	return DefaultMaxVoice
}
