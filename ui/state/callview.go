package state

import (
	"sort"
	"strings"

	"github.com/medeirosvictor/hermec/core/proto"
)

// CallParticipant is the render-independent state for one tile in the
// current call view.
type CallParticipant struct {
	Fingerprint string
	Name        string
	Self        bool
	Muted       bool
	Camera      bool
	Sharing     bool
}

// CallParticipants returns a deterministic snapshot of the current call.
func (s *State) CallParticipants() []CallParticipant {
	members := s.Voice[s.InCall]
	out := make([]CallParticipant, 0, len(members))
	for _, member := range members {
		participant := CallParticipant{
			Fingerprint: member.Fingerprint,
			Name:        CleanName(member.Name, MaxServerNameRunes),
			Self:        member.Fingerprint == s.fingerprint,
			Muted:       member.Muted,
			Sharing:     member.Sharing,
		}
		for _, kind := range member.Media {
			switch kind {
			case proto.MediaCamera:
				participant.Camera = true
			case proto.MediaScreen:
				participant.Sharing = true
			}
		}
		out = append(out, participant)
	}
	sort.Slice(out, func(i, j int) bool {
		in, jn := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if in != jn {
			return in < jn
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	return out
}

// OpenCallView shows the active call without changing call membership.
func (s *State) OpenCallView() {
	if s.InCall != "" {
		s.CallViewOpen = true
	}
}

// CloseCallView returns to chat without leaving voice.
func (s *State) CloseCallView() { s.CallViewOpen = false }
