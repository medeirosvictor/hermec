package state

import "time"

// layoutKey is everything the wrapped scrollback depends on. Colours are
// applied at draw time, so a palette swap needs no entry here.
type layoutKey struct {
	channel         string
	n               int
	firstTS, lastTS time.Time // first catches the capped window sliding
	cols            int
	fontSize        float64
	font            string // font id: equal cols and size can still differ in glyph shapes
}

// LayoutCache memoizes Scrollback for the active channel and recomputes it
// only when the key changes: new message, channel switch, resize or font
// size or font change. The zero value is an empty cache.
type LayoutCache struct {
	key   layoutKey
	lines []Line
	valid bool
}

// Lines returns the laid-out scrollback of msgs and whether it was
// recomputed. The returned slice is shared with the cache; callers must not
// modify it.
func (c *LayoutCache) Lines(channel string, msgs []Message, cols int, fontSize float64, font string) ([]Line, bool) {
	k := layoutKey{channel: channel, n: len(msgs), cols: cols, fontSize: fontSize, font: font}
	if len(msgs) > 0 {
		k.firstTS, k.lastTS = msgs[0].TS, msgs[len(msgs)-1].TS
	}
	if c.valid && k == c.key {
		return c.lines, false
	}
	c.key, c.valid = k, true
	c.lines = Scrollback(msgs, cols)
	return c.lines, true
}
