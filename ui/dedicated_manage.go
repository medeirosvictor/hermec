package ui

import (
	"context"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
)

func (g *game) dedicatedFounder(key string) bool {
	s := g.sess[key]
	return s != nil && s.c != nil && s.c.Chain() != nil && s.c.Chain().State().Founder == s.c.Fingerprint()
}
func (g *game) openDedicatedManage(key string) {
	if !g.dedicatedFounder(key) {
		return
	}
	s := g.sess[key]
	g.dmInput = field{}
	if g.dm.Open(s.c.ServerID(), true) {
		g.dmKey = key
		if g.dmCh == nil {
			g.dmCh = make(chan dedicatedManageResult, 4)
		}
	}
}
func (g *game) closeDedicatedManage() {
	if g.dmStop != nil {
		g.dmStop()
		g.dmStop = nil
	}
	g.dm.Close()
	g.dmInput = field{}
	g.dmKey = ""
}
func (g *game) dedicatedManageRect() rectF {
	x := g.addX()
	return rectF{x, g.addTop(), min(float64(g.w)-pad, x+620), g.addTop() + 10*g.lineH()}
}
func (g *game) addDedicatedManageHits() {
	if g.dmKey == "" {
		return
	}
	r, lh := g.dedicatedManageRect(), g.lineH()
	g.hits.Add(state.Hit{X0: r.x0, Y0: r.y0 + lh, X1: r.x1, Y1: r.y0 + 2*lh, Kind: state.HitDedicatedManageKey})
	g.hits.Add(state.Hit{X0: r.x0, Y0: r.y0 + 3*lh, X1: r.x1, Y1: r.y0 + 4*lh, Kind: state.HitDedicatedManageConfirm})
	g.hits.Add(state.Hit{X0: r.x0, Y0: r.y0 + 5*lh, X1: r.x1, Y1: r.y0 + 6*lh, Kind: state.HitDedicatedManageReady})
	g.hits.Add(state.Hit{X0: r.x0, Y0: r.y0 + 7*lh, X1: r.x1, Y1: r.y0 + 8*lh, Kind: state.HitDedicatedManageActivate})
	g.hits.Add(state.Hit{X0: r.x1 - 60, Y0: r.y0, X1: r.x1, Y1: r.y0 + lh, Kind: state.HitDedicatedManageClose})
}

type dedicatedManageResult struct {
	gen, operation uint64
	kind           int
	ready          *proto.DedicatedReadiness
	result         *proto.DedicatedResult
	err            error
}

const (
	dmAuthorized = iota
	dmReady
	dmActivated
)

func (g *game) updateDedicatedManage() bool {
	if g.dmKey == "" {
		return false
	}
	if g.escPressed() {
		g.closeDedicatedManage()
		g.escSpent = true
		return true
	}
	s := g.sess[g.dmKey]
	if s == nil || s.c == nil {
		g.closeDedicatedManage()
		return true
	}
	if !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		// Once parsed, the field is locked so the visible key remains the key
		// authorized by the in-flight workflow. Key/error phases remain editable.
		if (g.dm.Phase == state.ManageKey || g.dm.Phase == state.ManageError) && g.dmInput.edit(readEditKeys(), ebiten.AppendInputChars(nil), maxFieldRunes, nil, false) {
			g.dm.KeyChanged()
		}
		return true
	}
	h, ok := g.hitAt()
	if !ok {
		return true
	}
	switch h.Kind {
	case state.HitDedicatedManageClose:
		g.closeDedicatedManage()
	case state.HitDedicatedManageConfirm:
		if g.dm.Phase == state.ManageKey || g.dm.Phase == state.ManageError {
			g.dm.Parse(g.dmInput.String())
			return true
		}
		pub, priority, operation, ok := g.dm.Confirm(s.c.Chain().State().Hosts)
		if ok {
			g.runDedicatedAuthorize(g.dm.Gen, operation, s, pub, priority)
		}
	case state.HitDedicatedManageReady:
		if operation, ok := g.dm.BeginReady(); ok {
			g.runDedicatedReady(g.dm.Gen, operation, s)
		}
	case state.HitDedicatedManageActivate:
		if operation, ok := g.dm.BeginActivation(); ok {
			g.runDedicatedActivate(g.dm.Gen, operation, s)
		}
	}
	return true
}
func (g *game) dedicatedManageContext() (context.Context, context.CancelFunc) {
	if g.dmStop != nil {
		g.dmStop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	g.dmStop = cancel
	return ctx, cancel
}
func (g *game) sendDedicatedManage(r dedicatedManageResult) {
	select {
	case g.dmCh <- r:
	default:
	}
}
func (g *game) runDedicatedAuthorize(gen, operation uint64, s *session, pub []byte, priority int) {
	c := s.c // retain the client while a concurrent disconnect tears down the session.
	ctx, cancel := g.dedicatedManageContext()
	go func() {
		defer cancel()
		g.sendDedicatedManage(dedicatedManageResult{gen: gen, operation: operation, kind: dmAuthorized, err: c.AuthorizeDedicatedHost(ctx, pub, priority)})
	}()
}
func (g *game) runDedicatedReady(gen, operation uint64, s *session) {
	c, fp := s.c, g.dm.Fingerprint
	ctx, cancel := g.dedicatedManageContext()
	go func() {
		defer cancel()
		r, err := c.DedicatedReady(ctx, fp)
		g.sendDedicatedManage(dedicatedManageResult{gen: gen, operation: operation, kind: dmReady, ready: r, err: err})
	}()
}
func (g *game) runDedicatedActivate(gen, operation uint64, s *session) {
	c, r := s.c, *g.dm.Readiness
	ctx, cancel := g.dedicatedManageContext()
	go func() {
		defer cancel()
		result, err := c.ActivateDedicated(ctx, r)
		g.sendDedicatedManage(dedicatedManageResult{gen: gen, operation: operation, kind: dmActivated, result: result, err: err})
	}()
}
func (g *game) pumpDedicatedManage() {
	if g.dmCh == nil {
		return
	}
	select {
	case r := <-g.dmCh:
		switch r.kind {
		case dmAuthorized:
			if g.dm.Authorized(r.gen, r.operation, r.err) {
				if s := g.sess[g.dmKey]; s != nil {
					if operation, ok := g.dm.BeginReady(); ok {
						g.runDedicatedReady(r.gen, operation, s)
					}
				}
			}
		case dmReady:
			g.dm.Ready(r.gen, r.operation, r.ready, r.err)
		case dmActivated:
			g.dm.Activation(r.gen, r.operation, r.result, r.err)
		}
	default:
	}
}
func (g *game) drawDedicatedManage(screen *ebiten.Image) {
	if g.dmKey == "" {
		return
	}
	r, lh, th := g.dedicatedManageRect(), g.lineH(), g.th
	g.drawPanelFade(screen, r.x0, r.y0, r.x1, r.y1, th.Panel, 4)
	g.drawTextF(screen, g.faceT, "Manage dedicated host", r.x0+pad, r.y0+lh, th.Bright)
	g.drawText(screen, "Close", r.x1-60, r.y0+lh, th.Dim)
	g.drawText(screen, "VPS public key: "+g.dmInput.String(), r.x0+pad, r.y0+2*lh, th.FG)
	if g.dm.Fingerprint != "" {
		g.drawText(screen, "fingerprint: "+g.dm.Fingerprint, r.x0+pad, r.y0+3*lh, th.Bright)
	}
	g.drawText(screen, "Authorize", r.x0+pad, r.y0+4*lh, th.Bright)
	g.drawText(screen, "Check Ready", r.x0+pad, r.y0+6*lh, th.Bright)
	if n := g.dm.Nonce(); n != "" {
		g.drawTextF(screen, g.faceS, "exact transition nonce: "+n, r.x0+pad, r.y0+7*lh, th.FG)
	}
	g.drawText(screen, "Activate", r.x0+pad, r.y0+8*lh, th.Bright)
	g.drawTextF(screen, g.faceS, g.dm.Message+"  Esc closes without saving.", r.x0+pad, r.y0+9*lh, th.Dim)
}
