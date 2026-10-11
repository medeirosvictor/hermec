package state

// HitKind says what a click target does; the cursor shape derives from it.
type HitKind int

const (
	HitChannel     HitKind = iota // text channel row; ID is the channel index
	HitVoice                      // voice channel row; Key is the channel name
	HitRailTile                   // server tile in the rail; ID is the server index
	HitRailAdd                    // the "+" rail tile
	HitGear                       // the settings (gear) rail tile
	HitSettingsRow                // a row on the settings page; ID is the row
	HitBarMute                    // call-bar mute button
	HitBarLeave                   // call-bar leave button
	HitBanner                     // the update banner
	HitSignal                     // connection-quality bars
	HitText                       // text input area (text cursor); ID is the field
	HitDiscovered                 // discovered-server row on the connect page; ID is the row
)

// Hit is one click target: the half-open rect [X0,X1) x [Y0,Y1).
type Hit struct {
	X0, Y0, X1, Y1 float64
	Kind           HitKind
	ID             int
	Key            string
}

// Hits is the per-frame list of click targets. Click handlers and the cursor
// shape both query it, so they cannot disagree.
type Hits struct {
	list []Hit
}

// Reset empties the list, keeping its storage.
func (h *Hits) Reset() { h.list = h.list[:0] }

// Add appends a target. Later targets take priority over earlier ones.
func (h *Hits) Add(t Hit) { h.list = append(h.list, t) }

// At returns the topmost (last added) target containing (x, y).
func (h *Hits) At(x, y float64) (Hit, bool) {
	for i := len(h.list) - 1; i >= 0; i-- {
		t := h.list[i]
		if InRect(x, y, t.X0, t.Y0, t.X1, t.Y1) {
			return t, true
		}
	}
	return Hit{}, false
}
