package state

import "time"

// TooltipDelay is how long the pointer must rest on a target before its
// tooltip appears.
const TooltipDelay = 400 * time.Millisecond

// Ellipsis is the one-rune marker Ellipsize puts where text was cut. The
// embedded VT323 face has this glyph (see TestFaceHasEllipsis in ui/theme).
const Ellipsis = "…"

// Hover tracks how long the pointer has stayed on one target.
type Hover struct {
	id    int
	since time.Time
	on    bool
}

// Step feeds the target under the pointer (id < 0 for none) and reports
// whether the same target has now been held for TooltipDelay.
func (h *Hover) Step(id int, now time.Time) bool {
	if id < 0 {
		h.on = false
		return false
	}
	if !h.on || h.id != id {
		h.id, h.since, h.on = id, now, true
	}
	return now.Sub(h.since) >= TooltipDelay
}

// TooltipOrigin places a w x h tooltip to the right of a tile whose right
// edge is tileX1 and whose vertical extent is tileY0..tileY1: a 6 px gap,
// vertically centred, then clamped inside the winW x winH window (never
// negative, even when the window is smaller than the tooltip).
func TooltipOrigin(tileX1, tileY0, tileY1, w, h, winW, winH float64) (x, y float64) {
	x = tileX1 + 6
	y = (tileY0+tileY1)/2 - h/2
	x = min(x, winW-w)
	y = min(y, winH-h)
	return max(x, 0), max(y, 0)
}

// Ellipsize shortens s to at most cols runes, ending in Ellipsis when cut.
func Ellipsize(s string, cols int) string {
	if cols < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= cols {
		return s
	}
	return string(r[:cols-1]) + Ellipsis
}
