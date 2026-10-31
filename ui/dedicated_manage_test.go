package ui

import (
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/client"
	"github.com/medeirosvictor/hermec/ui/state"
)

func TestDedicatedManageCloseAndSessionLossCancelOperation(t *testing.T) {
	for _, sessionLoss := range []bool{false, true} {
		g := testGame(t)
		cancelled := false
		g.dmStop = func() { cancelled = true }
		g.dmKey = "server"
		if sessionLoss {
			// A disconnected/non-founder session is removed by background work.
			g.sess = map[string]*session{"server": {c: &client.Client{}, st: state.New()}}
			g.tickBackground(time.Now())
		} else {
			g.closeDedicatedManage()
		}
		if !cancelled || g.dmStop != nil || g.dmKey != "" {
			t.Fatalf("sessionLoss=%v: operation was not cancelled and cleared", sessionLoss)
		}
	}
}
