package state

// RowAt maps a y coordinate to a row index in a list of count rows of height
// rowH starting at top, or -1 when y falls outside the list.
func RowAt(y, top, rowH float64, count int) int {
	if rowH <= 0 || y < top {
		return -1
	}
	i := int((y - top) / rowH)
	if i >= count {
		return -1
	}
	return i
}

// InRect reports whether (x, y) lies in [x0,x1) x [y0,y1).
func InRect(x, y, x0, y0, x1, y1 float64) bool {
	return x >= x0 && x < x1 && y >= y0 && y < y1
}

// WheelLines converts a wheel delta (in notches) into whole scroll lines,
// carrying the fractional remainder (in lines) between frames. perNotch is
// lines per notch; positive means scroll up.
func WheelLines(carry, delta float64, perNotch int) (lines int, newCarry float64) {
	total := carry + delta*float64(perNotch)
	lines = int(total) // truncates toward zero
	return lines, total - float64(lines)
}
