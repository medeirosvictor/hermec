package ui

import (
	"testing"

	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
)

// The chat input sits directly under the chat pane in the right column, the
// channel pane runs beside it, and the call bar (when shown) is the bottom
// strip. All of it comes from geom(), which draw and hit-testing share.
func TestGeomInputUnderChatPane(t *testing.T) {
	th := theme.Default()
	face, err := theme.Face(th.FontSize)
	if err != nil {
		t.Skip(err)
	}
	g := &game{st: state.New(), th: th, face: face, w: 960, h: 600}
	gm := g.geom()
	if gm.inputY <= gm.paneY1 || gm.inputY-gm.paneY1 > pad+1 {
		t.Fatalf("input not directly under chat pane: pane end %v input %v", gm.paneY1, gm.inputY)
	}
	if gm.chanY1 != gm.inputY+gm.lh {
		t.Fatalf("channel pane should end level with the input line: %v vs %v", gm.chanY1, gm.inputY+gm.lh)
	}
	noBar := gm
	g.st.SetInCall("voice")
	gm = g.geom()
	if gm.barY+gm.lh+pad != float64(g.h) || gm.chanY1 != gm.barY-pad {
		t.Fatalf("bar not the bottom strip: %+v", gm)
	}
	if gm.inputY >= noBar.inputY || gm.rightX != noBar.rightX {
		t.Fatalf("input should shift up above the bar, same column: %+v vs %+v", gm, noBar)
	}
}
