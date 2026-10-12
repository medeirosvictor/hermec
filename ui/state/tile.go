package state

// TileFacts are the inputs that decide how a server's rail tile looks.
type TileFacts struct {
	Viewed       bool // this server is the one on screen
	HasSession   bool // a live or reconnecting session exists
	Reconnecting bool
	InCall       bool
	Unread       bool
}

// TileState is the resulting look of a rail tile.
type TileState struct {
	Accent bool // highlighted as the viewed server
	Dim    bool // no session, or reconnecting
	Pulse  bool // animate while reconnecting
	Dot    bool // unread marker
	Bust   bool // in-call marker
}

// Tile maps facts to a tile look.
func Tile(f TileFacts) TileState {
	return TileState{
		Accent: f.Viewed,
		Dim:    !f.HasSession || f.Reconnecting,
		Pulse:  f.Reconnecting,
		Dot:    f.Unread,
		Bust:   f.InCall,
	}
}
