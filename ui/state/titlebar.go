package state

// Zone is a hit region of the custom title bar or the window's resize border.
type Zone int

const (
	ZoneNone Zone = iota
	ZoneCaption
	ZoneMin
	ZoneMax
	ZoneClose
	ZoneLeft
	ZoneRight
	ZoneTop
	ZoneBottom
	ZoneTopLeft
	ZoneTopRight
	ZoneBottomLeft
	ZoneBottomRight
)

// Title bar metrics in logical pixels.
const (
	TitleBarH = 32.0
	TitleEdge = 6.0
)

// TitleMetrics is the bar height and resize band in physical pixels at a DPI
// scale (1.5 for 150%); a non-positive scale counts as 1.
func TitleMetrics(scale float64) (bar, edge float64) {
	if scale <= 0 {
		scale = 1
	}
	return TitleBarH * scale, TitleEdge * scale
}

// TitleZonePhysical is TitleZone for a point and window size in physical
// pixels (what the Win32 hit test sees) at the given DPI scale.
func TitleZonePhysical(x, y, w, h, scale float64, maximized bool) Zone {
	if scale <= 0 {
		scale = 1
	}
	return TitleZone(x/scale, y/scale, w/scale, h/scale, TitleBarH, TitleEdge, maximized)
}

// TitleBtnW is the width of each caption button; they are barH tall.
const TitleBtnW = 46.0

// TitleZone classifies a screen-space point of a w x h window whose title bar
// is barH tall. edge is the resize band width; there are no resize zones
// while maximized. The corner squares win over the caption buttons, and the
// buttons win over the rest of the top band.
func TitleZone(x, y, w, h, barH, edge float64, maximized bool) Zone {
	if x < 0 || y < 0 || x >= w || y >= h {
		return ZoneNone
	}
	if !maximized {
		l, r := x < edge, x >= w-edge
		t, b := y < edge, y >= h-edge
		switch {
		case t && l:
			return ZoneTopLeft
		case t && r:
			return ZoneTopRight
		case b && l:
			return ZoneBottomLeft
		case b && r:
			return ZoneBottomRight
		}
		if z := buttonZone(x, y, w, barH); z != ZoneNone {
			return z
		}
		switch {
		case l:
			return ZoneLeft
		case r:
			return ZoneRight
		case t:
			return ZoneTop
		case b:
			return ZoneBottom
		}
	}
	if z := buttonZone(x, y, w, barH); z != ZoneNone {
		return z
	}
	if y < barH {
		return ZoneCaption
	}
	return ZoneNone
}

func buttonZone(x, y, w, barH float64) Zone {
	switch {
	case y >= barH || x < w-3*TitleBtnW:
		return ZoneNone
	case x >= w-TitleBtnW:
		return ZoneClose
	case x >= w-2*TitleBtnW:
		return ZoneMax
	default:
		return ZoneMin
	}
}

// HTFor maps a zone to its Win32 hit-test code for WM_NCLBUTTONDOWN; 0 for
// the buttons and for no zone.
func HTFor(z Zone) uintptr {
	switch z {
	case ZoneCaption:
		return 2
	case ZoneLeft:
		return 10
	case ZoneRight:
		return 11
	case ZoneTop:
		return 12
	case ZoneTopLeft:
		return 13
	case ZoneTopRight:
		return 14
	case ZoneBottom:
		return 15
	case ZoneBottomLeft:
		return 16
	case ZoneBottomRight:
		return 17
	}
	return 0
}
