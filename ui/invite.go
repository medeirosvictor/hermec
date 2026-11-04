package ui

import (
	"strings"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/medeirosvictor/hermec/ui/state"
)

// addInvite is the "paste invite link" input of the Add a Server page. Its
// text can hold a password for as long as it takes to parse it, so a submit
// always wipes it.
type addInvite struct {
	f     field
	focus bool
	msg   string // outcome of the last paste, shown under the input
}

func (a *addInvite) clear() {
	for i := range a.f.runes {
		a.f.runes[i] = 0
	}
	a.f.runes, a.f.back = nil, 0
}

// inviteFor is the invite to copy for the saved server at key, and the menu
// eligibility check: only a dedicated (wss, pinned) entry has one. A regular
// LAN entry is not exportable just because it happens to have a server ID.
// The password is never part of it: the app keeps only a derived key.
func (g *game) inviteFor(key string) (state.Invite, bool) {
	e, ok := g.entryFor(key)
	if !ok {
		return state.Invite{}, false
	}
	d, err := state.ValidateDedicatedDetails(state.DedicatedDetails{WSSURL: e.URL, ServerID: e.ServerID})
	if err != nil {
		return state.Invite{}, false
	}
	return state.Invite{Addr: d.WSSURL, ServerID: d.ServerID, Name: state.CleanName(e.Name, state.MaxServerNameRunes)}, true
}

// copyInviteLink copies the invite link for a dedicated server. Neither the
// link nor its parts are logged.
func (g *game) copyInviteLink(inv state.Invite) {
	link, err := state.FormatInvite(inv)
	if err == nil {
		err = clipboardWrite(link)
	}
	if err != nil {
		g.cur().st.Status = "copy failed"
	} else {
		g.cur().st.Status = "invite link copied"
	}
}

// guardInvite wipes the paste-invite text unless its input is on screen and
// focused: leaving the Add page or clicking elsewhere must not leave a
// password sitting in the field.
func (g *game) guardInvite() {
	if g.ai.focus && g.add == addChoose && g.cur().st.Phase == state.PhaseConnect {
		return
	}
	g.ai.focus = false
	g.ai.clear()
}

// joinInviteText parses pasted text and starts the matching join. It returns
// a friendly message for the page on failure and "" on success. The text and
// anything parsed from it are never logged.
func (g *game) joinInviteText(text string) string {
	inv, err := state.ParseInvite(text)
	if err != nil {
		return "That is not a valid HERMEC invite link. Ask your host to copy it again."
	}
	if strings.HasPrefix(inv.Addr, "wss://") {
		g.openDedicatedInvite(inv)
		return ""
	}
	return g.joinPinned(inv)
}

// openDedicatedInvite opens the Dedicated join form prefilled from inv. It
// does not connect: the user still presses Validate / Connect, which runs the
// pinned admission and the media check.
func (g *game) openDedicatedInvite(inv state.Invite) {
	g.invalidateDedicatedValidation()
	g.df.reset()
	g.add = addDedicated
	g.df.wss.setText(inv.Addr)
	g.df.serverID.setText(inv.ServerID)
	if inv.Password != "" {
		g.df.password.setText(inv.Password)
		g.df.focus = 0
		g.df.status = "invite loaded, including its password. Press Validate / Connect."
	} else {
		g.df.focus = 2
		g.df.status = "invite loaded. Enter the server password if it has one, then press Validate / Connect."
	}
}

// joinPinned joins a ws:// invite through the normal join flow with the
// server ID pinned before the first dial, so a different server answering at
// that address is refused.
func (g *game) joinPinned(inv state.Invite) string {
	name := g.defaultName()
	if name == "" {
		return "Enter your name under Join a server first, then paste the link again."
	}
	key := g.keyFor(g.realURL(inv.Addr))
	if s := g.sess[key]; s != nil && s.c != nil {
		if id := s.c.ServerID(); id != "" && id != inv.ServerID {
			return "You are already connected to a different server at that address. Leave it first."
		}
	}
	if e, ok := g.entryFor(key); ok {
		if e.ServerID != "" && e.ServerID != inv.ServerID {
			return "That address is already saved with a different server ID. Leave that server first."
		}
		g.servers = state.PinID(g.servers, key, inv.ServerID)
	} else if g.sess[key] == nil {
		label := state.CleanName(inv.Name, state.MaxServerNameRunes)
		if label == "" {
			label = g.labelFor(key, key)
		}
		g.servers = append([]state.ServerEntry{{URL: key, Label: label, Pending: true, ServerID: inv.ServerID}}, g.servers...)
	}
	s := g.openSession(key, name)
	if s == nil {
		return "Could not join that server."
	}
	g.view(s.key)
	return ""
}

// updateInviteField handles the paste-invite input on the choice page. A
// paste or Enter submits; it reports that the click or key was used.
func (g *game) updateInviteField() {
	a := &g.ai
	if !a.focus {
		return
	}
	k := readEditKeys()
	a.f.edit(k, ebiten.AppendInputChars(nil), state.MaxInviteBytes, nil, false)
	if k.paste || enterPressed() {
		text := a.f.String()
		a.clear()
		a.msg = g.joinInviteText(text)
	}
}

// inviteShown is the tail of s that fits room columns, with everything after
// the "?" hidden so a secret (pw=, however it is spelled or split) is never
// drawn.
func inviteShown(s string, room int) string {
	if i := strings.Index(s, "?"); i >= 0 {
		s = s[:i+1] + strings.Repeat("•", len([]rune(s[i+1:])))
	}
	r := []rune(s)
	if room < 1 {
		room = 1
	}
	if len(r) > room {
		r = r[len(r)-room:]
	}
	return string(r)
}
