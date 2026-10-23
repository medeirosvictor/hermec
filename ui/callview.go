package ui

import (
	"hash/fnv"
	"image/color"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
)

type callTile struct {
	participant state.CallParticipant
	rect        rectF
}

type callControl struct {
	kind state.HitKind
	rect rectF
}

type callViewLayout struct {
	back         rectF
	presentation *rectF
	tiles        []callTile
	controls     []callControl
}

func (g *game) callViewActive() bool {
	s := g.cur()
	return g.callKey != "" && g.callKey == g.curKey && s.st.Phase == state.PhaseMain && s.st.CallViewOpen && !g.settingsOpen
}

func (g *game) callLayout(gm geometry) callViewLayout {
	r := rectF{gm.chatR.x0 + pad, gm.chatR.y0 + pad, gm.chatR.x1 - pad, gm.chatR.y1 - pad}
	layout := callViewLayout{back: rectF{r.x0, r.y0, min(r.x0+80, r.x1), min(r.y0+gm.lh, r.y1)}}
	controlH := min(gm.lh+pad, max(0, r.y1-layout.back.y1))
	controlsY := r.y1 - controlH
	kinds := []state.HitKind{state.HitBarMute, state.HitVoiceCamera, state.HitVoiceShare, state.HitVoiceLeave}
	if w := max(0, r.x1-r.x0) / float64(len(kinds)); w > 0 {
		for i, kind := range kinds {
			layout.controls = append(layout.controls, callControl{kind: kind, rect: rectF{r.x0 + float64(i)*w, controlsY, r.x0 + float64(i+1)*w, r.y1}})
		}
	}
	content := rectF{r.x0, layout.back.y1 + pad/2, r.x1, controlsY - pad/2}
	participants := g.cur().st.CallParticipants()
	if g.watch.fp != "" && content.y1 > content.y0 {
		ph := (content.y1 - content.y0) * 0.68
		presentation := rectF{content.x0, content.y0, content.x1, content.y0 + ph}
		layout.presentation = &presentation
		strip := rectF{content.x0, presentation.y1 + pad/2, content.x1, content.y1}
		if len(participants) > 0 {
			w := max(1, (strip.x1-strip.x0)/float64(len(participants)))
			for i, participant := range participants {
				layout.tiles = append(layout.tiles, callTile{participant, rectF{strip.x0 + float64(i)*w, strip.y0, min(strip.x0+float64(i+1)*w-pad/2, strip.x1), strip.y1}})
			}
		}
		return layout
	}
	if len(participants) == 0 || content.x1 <= content.x0 || content.y1 <= content.y0 {
		return layout
	}
	cols := int(math.Ceil(math.Sqrt(float64(len(participants)))))
	rows := (len(participants) + cols - 1) / cols
	w, h := (content.x1-content.x0)/float64(cols), (content.y1-content.y0)/float64(rows)
	for i, participant := range participants {
		col, row := i%cols, i/cols
		layout.tiles = append(layout.tiles, callTile{participant, rectF{
			content.x0 + float64(col)*w, content.y0 + float64(row)*h,
			content.x0 + float64(col+1)*w - pad/2, content.y0 + float64(row+1)*h - pad/2,
		}})
	}
	return layout
}

type callTileFacts struct{ camera, avatar, muted, speaking, sharing bool }

func (g *game) callTileState(participant state.CallParticipant) callTileFacts {
	view := g.mediaViews[mediaViewKey{Kind: proto.MediaCamera, Fingerprint: participant.Fingerprint}]
	camera := participant.Camera && view != nil && view.rgba != nil
	speaking := false
	if env := g.speakEnv[participant.Fingerprint]; env != nil {
		speaking = env.Value() > 0
	}
	return callTileFacts{camera: camera, avatar: !camera, muted: participant.Muted, speaking: speaking, sharing: participant.Sharing}
}

func (g *game) addCallViewHits(gm geometry) {
	if !g.callViewActive() {
		return
	}
	layout := g.callLayout(gm)
	g.hits.Add(state.Hit{X0: layout.back.x0, Y0: layout.back.y0, X1: layout.back.x1, Y1: layout.back.y1, Kind: state.HitCallBack})
	if layout.presentation != nil {
		r := *layout.presentation
		g.hits.Add(state.Hit{X0: r.x0, Y0: r.y0, X1: r.x1, Y1: r.y1, Kind: state.HitCallScreen, Key: g.watch.fp})
	}
	for _, tile := range layout.tiles {
		r := tile.rect
		g.hits.Add(state.Hit{X0: r.x0, Y0: r.y0, X1: r.x1, Y1: r.y1, Kind: state.HitCallParticipant, Key: tile.participant.Fingerprint})
	}
	for _, control := range layout.controls {
		r := control.rect
		g.hits.Add(state.Hit{X0: r.x0, Y0: r.y0, X1: r.x1, Y1: r.y1, Kind: control.kind})
	}
}

func (g *game) handleCallHit(hit state.Hit) {
	switch hit.Kind {
	case state.HitCallBack:
		g.cur().st.CloseCallView()
	case state.HitCallParticipant:
		for _, participant := range g.cur().st.CallParticipants() {
			if participant.Fingerprint == hit.Key && participant.Sharing {
				g.selectScreen(hit.Key)
				return
			}
		}
	case state.HitBarMute, state.HitVoiceCamera, state.HitVoiceShare, state.HitVoiceLeave:
		g.clickBar(hit)
	}
}

func personInitials(name string) string {
	runes := []rune(name)
	if len(runes) > 2 {
		runes = runes[:2]
	}
	return string(runes)
}

func avatarColor(fp string) color.RGBA {
	h := fnv.New32a()
	_, _ = h.Write([]byte(fp))
	v := h.Sum32()
	return color.RGBA{80 + byte(v&63), 80 + byte(v>>8&63), 80 + byte(v>>16&63), 255}
}

func (g *game) drawCallView(screen *ebiten.Image, gm geometry) {
	layout := g.callLayout(gm)
	g.drawText(screen, "‹ chat", layout.back.x0, layout.back.y0, g.th.Bright)
	if layout.presentation != nil {
		r := *layout.presentation
		vector.FillRect(screen, float32(r.x0), float32(r.y0), float32(max(0, r.x1-r.x0)), float32(max(0, r.y1-r.y0)), color.Black, false)
	}
	for _, tile := range layout.tiles {
		r, participant := tile.rect, tile.participant
		facts := g.callTileState(participant)
		bg := avatarColor(participant.Fingerprint)
		if facts.speaking {
			vector.FillRect(screen, float32(r.x0-2), float32(r.y0-2), float32(r.x1-r.x0+4), float32(r.y1-r.y0+4), g.th.Bright, false)
		}
		vector.FillRect(screen, float32(r.x0), float32(r.y0), float32(max(0, r.x1-r.x0)), float32(max(0, r.y1-r.y0)), bg, false)
		if facts.camera {
			g.drawViewIn(screen, g.mediaViews[mediaViewKey{Kind: proto.MediaCamera, Fingerprint: participant.Fingerprint}], r)
		} else {
			initials := personInitials(participant.Name)
			x := (r.x0 + r.x1 - text.Advance(initials, g.face)) / 2
			g.drawText(screen, initials, x, (r.y0+r.y1-g.lineH())/2, g.th.Bright)
		}
		label := participant.Name
		if participant.Self {
			label += " (you)"
		}
		if facts.muted {
			label += " · muted"
		}
		if facts.sharing {
			label += " · LIVE"
		}
		cols := max(1, int((r.x1-r.x0)/max(1, text.Advance("M", g.face))))
		g.drawText(screen, state.Ellipsize(label, cols), r.x0+2, max(r.y0, r.y1-g.lineH()), g.th.FG)
	}
	labels := map[state.HitKind]string{state.HitBarMute: "mute", state.HitVoiceCamera: g.cameraLabel(), state.HitVoiceShare: g.shareLabel(), state.HitVoiceLeave: "leave"}
	for _, control := range layout.controls {
		vector.StrokeRect(screen, float32(control.rect.x0), float32(control.rect.y0), float32(control.rect.x1-control.rect.x0), float32(control.rect.y1-control.rect.y0), 1, g.th.Dim, false)
		g.drawText(screen, labels[control.kind], control.rect.x0+pad/2, control.rect.y0, g.th.FG)
	}
}

func (g *game) drawViewIn(dst *ebiten.Image, view *videoView, r rectF) {
	if view == nil || view.rgba == nil || view.w <= 0 || view.h <= 0 {
		return
	}
	if view.pending {
		if view.img == nil || view.img.Bounds().Dx() != view.w || view.img.Bounds().Dy() != view.h {
			if view.img != nil {
				view.img.Deallocate()
			}
			view.img = ebiten.NewImage(view.w, view.h)
		}
		view.img.WritePixels(view.rgba.Pix)
		view.pending = false
	}
	if view.img == nil {
		return
	}
	x, y, w, h := state.Letterbox(view.w, view.h, r.x0, r.y0, r.x1, r.y1)
	op := &ebiten.DrawImageOptions{Filter: ebiten.FilterLinear}
	op.GeoM.Scale(w/float64(view.w), h/float64(view.h))
	op.GeoM.Translate(x, y)
	dst.DrawImage(view.img, op)
}
