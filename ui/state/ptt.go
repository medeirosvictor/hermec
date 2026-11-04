package state

import "time"

// Voice modes, as saved in settings.toml's voice_mode.
const (
	VoiceOpen = "open" // default; never written
	VoicePTT  = "ptt"
)

// PTTKey describes a push-to-talk key choice.
type PTTKey struct {
	ID    string
	Label string
	// Types reports whether the key produces text in a text field, so holding
	// it while typing must not transmit.
	Types bool
}

// PTTKeys are the selectable push-to-talk keys, in cycle order; the first is
// the default.
var PTTKeys = []PTTKey{
	{"rctrl", "Right Ctrl", false},
	{"grave", "` (grave)", true},
	{"capslock", "Caps Lock", false},
	{"f13", "F13", false},
	{"mouse4", "Mouse 4", false},
}

// PushToTalk reports whether the push-to-talk voice mode is on.
func (s Settings) PushToTalk() bool { return s.VoiceMode == VoicePTT }

// VoiceModeLabel is the Settings row value.
func (s Settings) VoiceModeLabel() string {
	if s.PushToTalk() {
		return "push to talk"
	}
	return "open mic"
}

// ToggleVoiceMode flips between open mic and push to talk. Open mic is stored
// as the empty string.
func (s *Settings) ToggleVoiceMode() {
	if s.PushToTalk() {
		s.VoiceMode = ""
	} else {
		s.VoiceMode = VoicePTT
	}
}

// PTTKeyChoice is the saved push-to-talk key; unset or unknown means the
// default (the first entry).
func (s Settings) PTTKeyChoice() PTTKey {
	for _, k := range PTTKeys {
		if k.ID == s.PTTKey {
			return k
		}
	}
	return PTTKeys[0]
}

// NextPTTKey is the key id after cur, wrapping around. Unset or unknown counts
// as the default.
func NextPTTKey(cur string) string {
	id := Settings{PTTKey: cur}.PTTKeyChoice().ID
	for i, k := range PTTKeys {
		if k.ID == id {
			return PTTKeys[(i+1)%len(PTTKeys)].ID
		}
	}
	return PTTKeys[0].ID
}

// CyclePTTKey advances the key; the default is not stored.
func (s *Settings) CyclePTTKey() {
	s.PTTKey = NextPTTKey(s.PTTKey)
	if s.PTTKey == PTTKeys[0].ID {
		s.PTTKey = ""
	}
}

// PTTHangover keeps the gate open briefly after release so the end of a word
// is not clipped.
const PTTHangover = 150 * time.Millisecond

// GateInput is one tick's facts for the transmit gate.
type GateInput struct {
	PTT           bool // push-to-talk mode (false: open mic)
	Held          bool // the push-to-talk key is down
	Muted         bool // explicit mute (Ctrl+M or the button)
	WindowFocused bool // the app window has OS focus
	TextFocus     bool // a text field is taking typed characters
	KeyTypes      bool // the push-to-talk key produces text (PTTKey.Types)
}

// Gate is the local transmit gate. It is pure: the caller passes the time.
type Gate struct {
	until time.Time // transmit through this instant after the key is released
	open  bool
	held  bool // the key counted as held at the last Step
}

// Step returns whether to transmit now. Muted never transmits, open mic always
// does (unless muted), and in push-to-talk the key must be held, except that a
// text-producing key is ignored while the focused window has a text field. The
// gate lingers PTTHangover after release.
func (g *Gate) Step(in GateInput, now time.Time) bool {
	g.held = false
	if in.Muted {
		g.open, g.until = false, time.Time{}
		return false
	}
	if !in.PTT {
		g.open, g.until = true, time.Time{}
		return true
	}
	held := in.Held && !(in.WindowFocused && in.TextFocus && in.KeyTypes)
	switch {
	case held:
		g.held = true
		g.open = true
		g.until = now.Add(PTTHangover)
	case g.open && now.Before(g.until):
	default:
		g.open = false
	}
	return g.open
}

// Held reports whether the key counted as held (not muted, not swallowed by a
// text field) at the last Step; the client's deadline is renewed only then.
func (g *Gate) Held() bool { return g.held }

// Open reports whether the gate was open at the last Step, for the
// transmitting indicator and the meter.
func (g *Gate) Open() bool { return g.open }
