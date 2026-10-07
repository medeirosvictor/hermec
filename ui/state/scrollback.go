package state

import "time"

// Stamp formats t as local HH:MM.
func Stamp(t time.Time) string { return t.Local().Format("15:04") }

// ClampScroll clamps offset (lines scrolled up from the bottom) to
// [0, max(0, total-visible)].
func ClampScroll(total, visible, offset int) int {
	if max := total - visible; offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}

// Line is one rendered scrollback row. Continuation rows of a wrapped message
// have empty Stamp and Name and Indent set to the first row's prefix width
// in columns.
type Line struct {
	Stamp, Name, Text string
	Indent            int
}

// minBodyCols is the narrowest text column Scrollback will wrap to.
const minBodyCols = 4

// Scrollback lays msgs out as "[HH:MM] name: text" rows wrapped to cols
// columns total.
func Scrollback(msgs []Message, cols int) []Line {
	var lines []Line
	for _, m := range msgs {
		stamp := "[" + Stamp(m.TS) + "] "
		name := m.FromName + ": "
		plen := len([]rune(stamp)) + len([]rune(name))
		body := cols - plen
		if body < minBodyCols {
			body = minBodyCols
		}
		for i, t := range Wrap(m.Text, body) {
			if i == 0 {
				lines = append(lines, Line{Stamp: stamp, Name: name, Text: t})
			} else {
				lines = append(lines, Line{Text: t, Indent: plen})
			}
		}
	}
	return lines
}
