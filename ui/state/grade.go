package state

import (
	"fmt"
	"math"
	"time"
)

// Participant is one call participant's audio health, as plain values (the
// audio package is not imported here).
type Participant struct {
	Name      string
	Concealed float64 // fraction of recent slots concealed, 0..1
	Jitter    time.Duration
	OK        bool // false: no recent data; ignored by the worst-sender maths
}

// Grade turns ping and audio health into 0..4 signal bars: 0 without a pong,
// else 4/3/2/1 by the first row whose rtt and concealment limits both hold.
// concealed is the worst sender's ratio in a call, 0 out of one. jitter is
// shown in the detail panel but does not move the grade.
func Grade(rtt time.Duration, rttOK bool, concealed float64, jitter time.Duration) int {
	switch {
	case !rttOK:
		return 0
	case rtt < 80*time.Millisecond && concealed < 0.01:
		return 4
	case rtt < 150*time.Millisecond && concealed < 0.03:
		return 3
	case rtt < 300*time.Millisecond && concealed < 0.08:
		return 2
	}
	return 1
}

// WorstHealth is the highest concealment and jitter among participants with
// data (OK); the two maxima may come from different senders.
func WorstHealth(parts []Participant) (concealed float64, jitter time.Duration) {
	for _, p := range parts {
		if !p.OK {
			continue
		}
		concealed = math.Max(concealed, p.Concealed)
		if p.Jitter > jitter {
			jitter = p.Jitter
		}
	}
	return
}

func roundMs(d time.Duration) int { return int(math.Round(float64(d) / float64(time.Millisecond))) }

// QualityLines is the detail panel text: ping, the worst jitter and
// concealment (only in a call), then one line per participant.
func QualityLines(rtt time.Duration, rttOK bool, parts []Participant) []string {
	if !rttOK {
		return []string{"no data"}
	}
	lines := []string{fmt.Sprintf("ping %d ms", roundMs(rtt))}
	if len(parts) == 0 {
		return lines
	}
	c, j := WorstHealth(parts)
	lines = append(lines, fmt.Sprintf("jitter %d ms", roundMs(j)), fmt.Sprintf("concealed %.1f%%", c*100))
	for _, p := range parts {
		if !p.OK {
			lines = append(lines, p.Name+"  --")
			continue
		}
		lines = append(lines, fmt.Sprintf("%s  %.1f%%  %d ms", p.Name, p.Concealed*100, roundMs(p.Jitter)))
	}
	return lines
}
