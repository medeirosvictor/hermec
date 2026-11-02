package ui

import "github.com/medeirosvictor/hermec/ui/state"

// rememberCall notes the settled call s is in, with its mute state, when its
// connection is lost, so rejoinVoice can restore it after the reconnect.
func (g *game) rememberCall(s *session) {
	if g.callKey == s.key && !g.vc.busy {
		s.rejoin.Lost(s.st.InCall, g.vc.muted)
		return
	}
	s.rejoin.Lost("", false)
}

// rejoinVoice rejoins the call s had before its connection dropped, muted as
// it was; the camera and screen share stay off. A channel that is gone, or a
// call the user has moved on from, only ends in a status line.
func (g *game) rejoinVoice(s *session) {
	a := s.rejoin.Reconnected(s.st.VoiceChannels)
	switch a.Kind {
	case state.RejoinGone:
		g.logf("voice channel %s on %s is gone; not rejoining", a.Channel, s.url)
		s.st.Status = state.RejoinGoneNotice(a.Channel)
	case state.RejoinJoin:
		if g.vc.busy || g.callSess() != nil {
			return // the user is already doing something with voice
		}
		g.logf("rejoining voice %s on %s", a.Channel, s.url)
		g.joinCallOn(s, a.Channel, a.Muted, true)
	}
}
