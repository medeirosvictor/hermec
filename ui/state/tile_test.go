package state

import "testing"

func TestTile(t *testing.T) {
	cases := []struct {
		name string
		f    TileFacts
		want TileState
	}{
		{"no session", TileFacts{}, TileState{Dim: true}},
		{"idle session", TileFacts{HasSession: true}, TileState{}},
		{"reconnecting", TileFacts{HasSession: true, Reconnecting: true}, TileState{Dim: true, Pulse: true}},
		{"viewed", TileFacts{HasSession: true, Viewed: true}, TileState{Accent: true}},
		{"unread", TileFacts{HasSession: true, Unread: true}, TileState{Dot: true}},
		{"in call", TileFacts{HasSession: true, InCall: true}, TileState{Bust: true}},
	}
	for _, c := range cases {
		if got := Tile(c.f); got != c.want {
			t.Errorf("%s: Tile(%+v) = %+v, want %+v", c.name, c.f, got, c.want)
		}
	}
}
