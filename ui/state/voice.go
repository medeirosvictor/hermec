package state

import (
	"errors"
	"fmt"
	"time"

	"github.com/medeirosvictor/hermec/client"
)

// NoVoiceSupport is the notice shown when a server rejects voice_join as a
// bad request, which is what servers predating voice do.
const NoVoiceSupport = "server has no voice support"

// VoiceJoinNotice turns a JoinVoice failure into a status line.
func VoiceJoinNotice(err error) string {
	var se *client.ServerError
	if errors.As(err, &se) && se.Code == "bad_request" {
		return NoVoiceSupport
	}
	return fmt.Sprintf("voice join failed: %v", err)
}

// RowKind classifies one row of the channel pane.
type RowKind int

const (
	RowText     RowKind = iota // a text channel; Index is its position in Channels
	RowDivider                 // the "── voice ──" separator
	RowVoice                   // a voice channel; Channel is its name
	RowOccupant                // a member under a voice channel
)

// Row is one line of the channel pane. Draw and hit-testing share Rows so a
// click always lands on what is drawn.
type Row struct {
	Kind    RowKind
	Index   int    // RowText: index into Channels
	Channel string // channel name (the parent channel for occupants)
	Name    string // RowOccupant: member name
	FP      string // RowOccupant: member fingerprint
	Muted   bool   // RowOccupant
}

// Rows lays out the channel pane: text channels, then (if the server has any)
// a divider and each voice channel followed by its occupants.
func (s *State) Rows() []Row {
	rows := make([]Row, 0, len(s.Channels)+len(s.VoiceChannels)+1)
	for i, ch := range s.Channels {
		rows = append(rows, Row{Kind: RowText, Index: i, Channel: ch})
	}
	if len(s.VoiceChannels) == 0 {
		return rows
	}
	rows = append(rows, Row{Kind: RowDivider})
	for _, ch := range s.VoiceChannels {
		rows = append(rows, Row{Kind: RowVoice, Channel: ch})
		for _, m := range s.Voice[ch] {
			rows = append(rows, Row{Kind: RowOccupant, Channel: ch, Name: m.Name, FP: m.Fingerprint, Muted: m.Muted})
		}
	}
	return rows
}

// VoiceClickAction says what clicking voice channel ch should do: "leave"
// when it is the current call, otherwise "join".
func (s *State) VoiceClickAction(ch string) string {
	if s.InCall == ch {
		return "leave"
	}
	return "join"
}

// FormatElapsed renders a call duration as m:ss, or h:mm:ss from one hour.
func FormatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	sec := int(d / time.Second)
	if sec >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec/60%60, sec%60)
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}
