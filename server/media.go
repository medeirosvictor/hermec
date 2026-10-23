package server

import "github.com/medeirosvictor/hermec/core/proto"

type mediaSubscription struct {
	Fingerprint string
	Gen         uint32
}

func (c *conn) hasCapability(capability string) bool {
	return c.capabilities[capability]
}

func (c *conn) publishing(kind proto.MediaKind) bool {
	return c.published[kind]
}

func (c *conn) mediaKinds() []proto.MediaKind {
	media := make([]proto.MediaKind, 0, 2)
	for _, kind := range []proto.MediaKind{proto.MediaCamera, proto.MediaScreen} {
		if c.publishing(kind) {
			media = append(media, kind)
		}
	}
	return media
}

func validMediaKind(kind proto.MediaKind) bool {
	return kind == proto.MediaCamera || kind == proto.MediaScreen
}

// mediaSet applies a negotiated modern publisher declaration.
func (s *Server) mediaSet(c *conn, kind proto.MediaKind, on bool) string {
	if !c.hasCapability(proto.CapabilityMediaLanesV1) {
		return proto.CodeBadRequest
	}
	return s.setMedia(c, kind, on)
}

func (s *Server) setMedia(c *conn, kind proto.MediaKind, on bool) string {
	if !validMediaKind(kind) {
		return proto.CodeBadRequest
	}
	s.chMu.Lock()
	ch := c.voiceCh
	if ch == "" {
		s.chMu.Unlock()
		return "not_joined"
	}
	if c.publishing(kind) == on {
		s.chMu.Unlock()
		return ""
	}
	if on && kind == proto.MediaCamera {
		active := 0
		for peer := range s.voice[ch] {
			if peer.publishing(proto.MediaCamera) {
				active++
			}
		}
		if active >= proto.MaxCameraSlots {
			s.chMu.Unlock()
			return proto.CodeMediaFull
		}
	}
	if c.published == nil {
		c.published = make(map[proto.MediaKind]bool)
	}
	if on {
		c.published[kind] = true
		if vs := s.sessions[ch]; vs != nil {
			vs.mediaStarted(c, kind)
		}
	} else {
		delete(c.published, kind)
		if kind == proto.MediaScreen {
			s.clearSubscribersLocked(ch, c.fingerprint, kind)
		}
		if vs := s.sessions[ch]; vs != nil {
			vs.mediaStopped(c, kind)
		}
	}
	dead := s.broadcastVoiceLocked(ch)
	s.chMu.Unlock()
	dropSlow(dead)
	return ""
}

// mediaSubscribe applies a negotiated modern subscription. Only screen is
// subscription-controlled in media_lanes_v1; cameras are assigned automatically.
func (s *Server) mediaSubscribe(c *conn, kind proto.MediaKind, fp string, gen uint32) string {
	if !c.hasCapability(proto.CapabilityMediaLanesV1) || kind != proto.MediaScreen {
		return proto.CodeBadRequest
	}
	return s.setMediaSubscription(c, kind, fp, gen)
}

func (s *Server) setMediaSubscription(c *conn, kind proto.MediaKind, fp string, gen uint32) string {
	s.chMu.Lock()
	defer s.chMu.Unlock()
	ch := c.voiceCh
	if ch == "" {
		return "not_joined"
	}
	if c.subscriptions == nil {
		c.subscriptions = make(map[proto.MediaKind]mediaSubscription)
	}
	vs := s.sessions[ch]
	if fp == "" {
		delete(c.subscriptions, kind)
		if vs != nil {
			vs.setMediaSubscription(c, nil, kind, gen)
		}
		return ""
	}

	current := c.subscriptions[kind]
	if current.Fingerprint == fp {
		if current.Gen != gen {
			c.subscriptions[kind] = mediaSubscription{Fingerprint: fp, Gen: gen}
			if vs != nil {
				vs.resendMediaSource(c, kind, gen)
			}
		}
		return ""
	}

	var target *conn
	for peer := range s.voice[ch] {
		if peer != c && peer.fingerprint == fp && peer.publishing(kind) {
			target = peer
			break
		}
	}
	if target == nil || vs == nil {
		for other, members := range s.voice {
			if other == ch {
				continue
			}
			for peer := range members {
				if peer.fingerprint == fp {
					return proto.CodeForbidden
				}
			}
		}
		return proto.CodeNotPublishing
	}
	c.subscriptions[kind] = mediaSubscription{Fingerprint: fp, Gen: gen}
	vs.setMediaSubscription(c, target, kind, gen)
	return ""
}

func (s *Server) clearSubscribersLocked(ch, fp string, kind proto.MediaKind) {
	for peer := range s.voice[ch] {
		if sub := peer.subscriptions[kind]; sub.Fingerprint == fp {
			delete(peer.subscriptions, kind)
		}
	}
}

func (s *Server) clearMediaLocked(ch string, c *conn) {
	c.published = nil
	c.subscriptions = nil
	for peer := range s.voice[ch] {
		for kind, sub := range peer.subscriptions {
			if sub.Fingerprint == c.fingerprint {
				delete(peer.subscriptions, kind)
			}
		}
	}
}
