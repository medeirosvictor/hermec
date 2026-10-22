package server

import (
	"bytes"
	"encoding/json"
	"sort"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
	"github.com/medeirosvictor/hermec/discover"
)

const (
	pingInterval = 15 * time.Second
	maxMissed    = 2 // unanswered pings before a connection is dropped

	maxChatBytes = 4096 // longest accepted chat text, in bytes
)

// channelSet is the channel registry. All access is guarded by Server.chMu.
// Broadcasts enqueue (non-blocking) while holding the lock so that messages
// reach every member in a consistent order; conns whose buffers are full or
// dead are collected and closed after the lock is released.
type channelSet map[string]map[*conn]struct{}

func (s *Server) initChannels() {
	s.chans = make(channelSet, len(s.cfg.Channels))
	for _, name := range s.cfg.Channels {
		s.chans[name] = make(map[*conn]struct{})
	}
	s.voice = make(voiceSet, len(s.cfg.VoiceChannels))
	for _, name := range s.cfg.VoiceChannels {
		s.voice[name] = make(map[*conn]bool)
	}
	s.authed = make(map[*conn]struct{})
	s.sessions = make(map[string]*voiceSession)
}

func (c *conn) member() proto.Member {
	return proto.Member{
		Fingerprint: c.fingerprint,
		Name:        c.name,
		Roles:       c.srv.roles().RolesFor(c.fingerprint),
	}
}

// broadcastLocked sends raw to every member of ch. Caller holds chMu.
// It returns the conns that could not accept the message.
func (s *Server) broadcastLocked(ch string, raw []byte) []*conn {
	var dead []*conn
	for m := range s.chans[ch] {
		if !m.enqueue(raw) {
			dead = append(dead, m)
		}
	}
	return dead
}

func (s *Server) presenceLocked(ch string) []byte {
	members := make([]proto.Member, 0, len(s.chans[ch]))
	for m := range s.chans[ch] {
		members = append(members, m.member())
	}
	sortMembers(members)
	return encodePresence(ch, members)
}

// sortMembers orders members by name, then fingerprint.
func sortMembers(members []proto.Member) {
	sort.Slice(members, func(i, j int) bool {
		if members[i].Name != members[j].Name {
			return members[i].Name < members[j].Name
		}
		return members[i].Fingerprint < members[j].Fingerprint
	})
}

// rosterLocked encodes every authenticated connection as a proto.Roster.
// Members are cut to MaxRosterMembers and then trimmed from the end until the
// frame fits MaxMessageSize; Total always counts every connection. Caller
// holds chMu.
func (s *Server) rosterLocked() []byte {
	members := make([]proto.Member, 0, len(s.authed))
	for m := range s.authed {
		mem := m.member()
		mem.Name = capRunes(mem.Name, discover.MaxNameRunes) // copy; c.name is untouched
		members = append(members, mem)
	}
	sortMembers(members)
	return encodeRoster(members, len(members))
}

// encodeRoster encodes at most MaxRosterMembers of members as a roster frame
// that fits MaxMessageSize, reporting total as the real connection count. The
// size is worked out from each member's own encoding, so it is encoded once
// for the budget and once for the result, however many members are dropped.
func encodeRoster(members []proto.Member, total int) []byte {
	if len(members) > proto.MaxRosterMembers {
		members = members[:proto.MaxRosterMembers]
	}
	base, err := proto.Encode(proto.TypeRoster, proto.Roster{Members: []proto.Member{}, Total: total})
	if err != nil {
		return nil
	}
	keep := fitMembers(len(base), members)
	raw, _ := proto.Encode(proto.TypeRoster, proto.Roster{Members: members[:keep], Total: total})
	return raw
}

// encodePresence is encodeRoster for one channel's presence frame, trimmed to
// the same size budget; Total is the real member count.
func encodePresence(ch string, members []proto.Member) []byte {
	base, err := proto.Encode(proto.TypePresence, proto.Presence{Channel: ch, Members: []proto.Member{}, Total: len(members)})
	if err != nil {
		return nil
	}
	keep := fitMembers(len(base), members)
	raw, _ := proto.Encode(proto.TypePresence, proto.Presence{Channel: ch, Members: members[:keep], Total: len(members)})
	return raw
}

// fitMembers returns how many leading members fit in MaxMessageSize on top of
// a frame already baseLen bytes long with an empty member list.
func fitMembers(baseLen int, members []proto.Member) int {
	size := baseLen
	keep := 0
	for _, m := range members {
		n, err := encodedLen(m)
		if err != nil {
			break
		}
		if keep > 0 {
			n++ // comma
		}
		if size+n > proto.MaxMessageSize {
			break
		}
		size += n
		keep++
	}
	return keep
}

// encodedLen is the length of v's JSON as the proto package writes it (no HTML
// escaping).
func encodedLen(v any) (int, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return 0, err
	}
	return buf.Len() - 1, nil // minus Encode's newline
}

// capRunes returns s cut to at most n runes (not bytes).
func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// dropSlow closes connections that fell behind. Their read loops then exit
// and the normal disconnect path removes them from channels.
func dropSlow(dead []*conn) {
	for _, d := range dead {
		d.ws.Close()
	}
}

func (s *Server) join(c *conn, ch string) bool {
	s.chMu.Lock()
	set, ok := s.chans[ch]
	if !ok {
		s.chMu.Unlock()
		return false
	}
	set[c] = struct{}{}
	dead := s.broadcastLocked(ch, s.presenceLocked(ch))
	s.chMu.Unlock()
	dropSlow(dead)
	return true
}

// leave removes c from ch and broadcasts presence to the rest. It reports
// whether c was a member.
func (s *Server) leave(c *conn, ch string) bool {
	s.chMu.Lock()
	set := s.chans[ch]
	if _, ok := set[c]; !ok {
		s.chMu.Unlock()
		return false
	}
	delete(set, c)
	dead := s.broadcastLocked(ch, s.presenceLocked(ch))
	s.chMu.Unlock()
	dropSlow(dead)
	return true
}

// leaveAll is the disconnect path: leave every channel c is in.
func (s *Server) leaveAll(c *conn) {
	s.chMu.Lock()
	var dead []*conn
	for ch, set := range s.chans {
		if _, ok := set[c]; !ok {
			continue
		}
		delete(set, c)
		dead = append(dead, s.broadcastLocked(ch, s.presenceLocked(ch))...)
	}
	if _, was := s.authed[c]; was {
		delete(s.authed, c)
		dead = append(dead, s.broadcastAuthedLocked(s.rosterLocked())...)
	}
	if vch := s.removeVoiceLocked(c); vch != "" {
		dead = append(dead, s.broadcastVoiceLocked(vch)...)
	}
	s.chMu.Unlock()
	dropSlow(dead)
}

func (s *Server) chat(c *conn, ch, text string) (code string) {
	s.chMu.Lock()
	if _, ok := s.chans[ch][c]; !ok {
		s.chMu.Unlock()
		return "not_joined"
	}
	if !s.roles().Has(c.fingerprint, roles.PermSendChat) {
		s.chMu.Unlock()
		return "forbidden"
	}
	raw, err := proto.Encode(proto.TypeChatMessage, proto.ChatMessage{
		Channel: ch, From: c.member(), Text: text, TS: time.Now().UTC(),
	})
	var dead []*conn
	if err == nil {
		dead = s.broadcastLocked(ch, raw)
	}
	s.chMu.Unlock()
	dropSlow(dead)
	return ""
}

// route handles one post-auth message.
func (c *conn) route(env proto.Envelope) {
	switch env.Type {
	case proto.TypeJoin:
		var m proto.Join
		if json.Unmarshal(env.Data, &m) != nil {
			c.sendError("bad_request", "malformed join")
			return
		}
		if !c.srv.roles().Has(c.fingerprint, roles.PermJoinChannel) {
			c.sendError("forbidden", "not permitted to join channels")
			return
		}
		if !c.srv.join(c, m.Channel) {
			c.sendError("bad_request", "unknown channel "+m.Channel)
		}
	case proto.TypeLeave:
		var m proto.Leave
		if json.Unmarshal(env.Data, &m) != nil {
			c.sendError("bad_request", "malformed leave")
			return
		}
		if !c.srv.leave(c, m.Channel) {
			c.sendError("not_joined", "not in channel "+m.Channel)
		}
	case proto.TypeChatSend:
		var m proto.ChatSend
		if json.Unmarshal(env.Data, &m) != nil {
			c.sendError("bad_request", "malformed chat_send")
			return
		}
		if len(m.Text) > maxChatBytes {
			c.sendError("bad_request", "message too long")
			return
		}
		switch c.srv.chat(c, m.Channel, m.Text) {
		case "not_joined":
			c.sendError("not_joined", "not in channel "+m.Channel)
		case "forbidden":
			c.sendError("forbidden", "not permitted to send chat")
		}
	case proto.TypeChainAppend:
		c.handleChainAppend(env)
	default:
		if c.routeVoice(env) {
			return
		}
		c.sendError("bad_request", "unsupported message type "+env.Type)
	}
}

// keepalive pings the client every pingInterval and closes the connection
// once maxMissed pings in a row go unanswered. Stops when done is closed.
func (c *conn) keepalive(done <-chan struct{}, missed *atomic.Int32) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if missed.Add(1) > maxMissed {
				c.ws.Close()
				return
			}
			_ = c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout))
		}
	}
}
