package state

// MenuOrigin places a w x h context menu anchored at (ax, ay) inside a
// winW x winH window: moved left/up just enough to fit, never below 0.
func MenuOrigin(ax, ay, w, h, winW, winH float64) (x, y float64) {
	x, y = ax, ay
	if x+w > winW {
		x = winW - w
	}
	if y+h > winH {
		y = winH - h
	}
	return max(x, 0), max(y, 0)
}
