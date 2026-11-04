package ui

import (
	"image/color"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/state"
)

// ebitenPTTDown maps a push-to-talk key id to Ebiten input, for when no OS-wide
// state is available (focused window only).
func ebitenPTTDown(id string) bool {
	switch id {
	case "rctrl":
		return ebiten.IsKeyPressed(ebiten.KeyControlRight)
	case "grave":
		return ebiten.IsKeyPressed(ebiten.KeyBackquote)
	case "capslock":
		return ebiten.IsKeyPressed(ebiten.KeyCapsLock)
	case "f13":
		return ebiten.IsKeyPressed(ebiten.KeyF13)
	case "mouse4":
		return ebiten.IsMouseButtonPressed(ebiten.MouseButton3)
	}
	return false
}

// pttHeld reports whether the push-to-talk key is down: system-wide where the
// OS can say (Windows), else only while the window has focus.
func pttHeld(id string) bool {
	if down, ok := osKeyDown(id); ok {
		return down
	}
	return ebitenPTTDown(id)
}

// textFocused reports whether typed characters go to a text field this tick.
func (g *game) textFocused() bool {
	switch {
	case g.modal || g.dmKey != "":
		return true
	case g.settingsOpen:
		return g.setRow == rowName
	}
	switch g.cur().st.Phase {
	case state.PhaseMain:
		return !g.callViewActive() // the chat input always has focus
	case state.PhaseConnect:
		return true
	}
	return false
}

// stepPTT runs the transmit gate for this tick and applies it to the client.
// In open-mic mode the gate is always open; explicit mute is the client's own
// switch and wins either way.
func (g *game) stepPTT(now time.Time) {
	key := g.set.PTTKeyChoice()
	in := state.GateInput{
		PTT:           g.set.PushToTalk(),
		Muted:         g.vc.muted,
		WindowFocused: ebiten.IsFocused(),
		KeyTypes:      key.Types,
	}
	if in.PTT && g.barVisible() { // no OS key polling outside a call or join
		in.Held = pttHeld(key.ID)
		in.TextFocus = g.textFocused()
	}
	g.transmitting = g.gate.Step(in, now) && in.PTT
	if s := g.callSess(); s != nil && s.c != nil {
		s.c.SetPushToTalk(in.PTT)
		switch {
		case in.Muted:
			s.c.SetTransmitUntil(time.Time{})
		case g.gate.Held():
			// Renewed every tick: a frozen UI loop closes the gate by itself.
			s.c.SetTransmitUntil(now.Add(state.PTTHangover))
		}
	}
}

// pttTagWidest is the longest tag, for reserving bar space.
const pttTagWidest = "PTT muted"

// pttColor is the tag colour: bright only while transmitting.
func (g *game) pttColor() color.RGBA {
	if g.transmitting {
		return g.th.Bright
	}
	return g.th.Dim
}

// pttTag is the call bar / voice panel marker: "" in open-mic mode.
func (g *game) pttTag() string {
	switch {
	case !g.set.PushToTalk():
		return ""
	case g.vc.muted:
		return "PTT muted"
	case g.transmitting:
		return "TALK"
	}
	return "PTT"
}
