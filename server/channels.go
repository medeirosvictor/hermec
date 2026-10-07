package server

import (
	"encoding/json"
	"sort"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/core/roles"
)

const (
	pingInterval = 15 * time.Second
	maxMissed    = 2 // unanswered pings before a connection is dropped
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
}

func (c *conn) member() proto.Member {
	return proto.Member{
		Fingerprint: c.fingerprint,
		Name:        c.name,
		Roles:       c.srv.cfg.Roles.RolesFor(c.fingerprint),
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
	sort.Slice(members, func(i, j int) bool {
		if members[i].Name != members[j].Name {
			return members[i].Name < members[j].Name
		}
		return members[i].Fingerprint < members[j].Fingerprint
	})
	raw, _ := proto.Encode(proto.TypePresence, proto.Presence{Channel: ch, Members: members})
	return raw
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
	delete(s.authed, c)
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
	if !s.cfg.Roles.Has(c.fingerprint, roles.PermSendChat) {
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
		if !c.srv.cfg.Roles.Has(c.fingerprint, roles.PermJoinChannel) {
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
		switch c.srv.chat(c, m.Channel, m.Text) {
		case "not_joined":
			c.sendError("not_joined", "not in channel "+m.Channel)
		case "forbidden":
			c.sendError("forbidden", "not permitted to send chat")
		}
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
