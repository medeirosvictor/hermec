package server

import (
	"encoding/json"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/medeirosvictor/hermec/core/proto"
)

// A warm client keeps a data-channel-only PeerConnection open to the standby so
// ICE and DTLS are already done when the standby is promoted. After promotion
// the conn's voice_join adopts that PeerConnection (see takeWarmPeer in
// newPC), and the client's next rtc_offer adds media to it.

const (
	warmChannelLabel = "hermec-warm"
	warmGatherWait   = 5 * time.Second
)

// handleWarmSignal routes warm_offer and warm_candidate. The server never
// offers, so warm_answer is refused.
func (c *conn) handleWarmSignal(env proto.Envelope) {
	switch env.Type {
	case proto.TypeWarmOffer:
		var m proto.WarmOffer
		if json.Unmarshal(env.Data, &m) != nil {
			c.sendError(proto.CodeBadRequest, "malformed warm_offer")
			return
		}
		c.handleWarmOffer(m.SDP)
	case proto.TypeWarmCandidate:
		var m proto.WarmCandidate
		var ci webrtc.ICECandidateInit
		if json.Unmarshal(env.Data, &m) != nil || json.Unmarshal([]byte(m.Candidate), &ci) != nil {
			c.sendError(proto.CodeBadRequest, "malformed warm_candidate")
			return
		}
		// A candidate that trails the voice_join (which adopted the PC) finds
		// no warm peer and is dropped; the rtc_* path carries the rest.
		if pc := c.srv.warmPeer(c); pc != nil {
			_ = pc.AddICECandidate(ci)
		}
	default:
		c.sendError(proto.CodeBadRequest, "unsupported message type "+env.Type)
	}
}

// handleWarmOffer answers a warm client's offer with a fresh PeerConnection
// built like a voice one. Our candidates travel inside the answer (gathering
// is host-only, so it finishes at once); the client's arrive as warm_candidate.
func (c *conn) handleWarmOffer(sdp string) {
	s := c.srv
	if !s.isWarm(c) {
		c.sendError(proto.CodeBadRequest, "warm_offer on a conn that is not warm")
		return
	}
	pc, err := s.newRTCAPI().NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		c.sendError(proto.CodeBadRequest, "cannot create peer connection")
		return
	}
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != warmChannelLabel {
			_ = dc.Close()
			return
		}
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			if string(m.Data) == "ping" {
				_ = dc.SendText("pong")
			}
		})
	})
	err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp})
	var answer webrtc.SessionDescription
	if err == nil {
		answer, err = pc.CreateAnswer(nil)
	}
	if err == nil {
		err = pc.SetLocalDescription(answer)
	}
	if err != nil {
		_ = pc.Close()
		c.sendError(proto.CodeBadRequest, "invalid warm_offer")
		return
	}
	select {
	case <-webrtc.GatheringCompletePromise(pc):
	case <-time.After(warmGatherWait):
	}
	s.setWarmPeer(c, pc)
	c.sendMsg(proto.TypeWarmAnswer, proto.WarmAnswer{SDP: pc.LocalDescription().SDP})
}

// warmPeer returns c's warm PeerConnection, nil if none.
func (s *Server) warmPeer(c *conn) *webrtc.PeerConnection {
	s.warmMu.Lock()
	defer s.warmMu.Unlock()
	return s.warmPeers[c]
}

// setWarmPeer registers pc as c's warm peer, closing one it replaces.
func (s *Server) setWarmPeer(c *conn, pc *webrtc.PeerConnection) {
	s.warmMu.Lock()
	old := s.warmPeers[c]
	s.warmPeers[c] = pc
	s.warmMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

// takeWarmPeer hands c's warm PeerConnection to the caller, which then owns it
// (and must close it). Nil if c has none.
func (s *Server) takeWarmPeer(c *conn) *webrtc.PeerConnection {
	s.warmMu.Lock()
	defer s.warmMu.Unlock()
	pc := s.warmPeers[c]
	delete(s.warmPeers, c)
	return pc
}

// closeWarmPeer closes and forgets c's warm PeerConnection, if any.
func (s *Server) closeWarmPeer(c *conn) {
	if pc := s.takeWarmPeer(c); pc != nil {
		_ = pc.Close()
	}
}

// closeWarmPeers closes every warm PeerConnection (an active host demoted).
func (s *Server) closeWarmPeers() {
	s.warmMu.Lock()
	peers := s.warmPeers
	s.warmPeers = make(map[*conn]*webrtc.PeerConnection)
	s.warmMu.Unlock()
	for _, pc := range peers {
		_ = pc.Close()
	}
}
