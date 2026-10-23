package ui

import (
	"fmt"
	"image"
	"testing"

	"github.com/medeirosvictor/hermec/core/proto"
	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/video"
)

func frameRGBA(frame *video.Frame) *image.RGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, frame.W, frame.H))
	frame.RGBA(rgba)
	return rgba
}

func overlap(a, b rectF) bool {
	return a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1
}

func TestCallViewShowsAllParticipantsAndSelectedScreenTogether(t *testing.T) {
	g, s := shareGame(t)
	g.mediaViews = make(map[mediaViewKey]*videoView)
	s.st.Voice["voice"] = nil
	for i := 0; i < 10; i++ {
		fp := fmt.Sprintf("fp-%02d", i)
		s.st.Voice["voice"] = append(s.st.Voice["voice"], proto.VoiceMember{Fingerprint: fp, Name: fp, Media: []proto.MediaKind{proto.MediaCamera}})
		g.mediaViews[mediaViewKey{Kind: proto.MediaCamera, Fingerprint: fp}] = &videoView{rgba: frameRGBA(video.NewFrame(4, 4))}
	}
	g.watch = watchState{fp: "presenter"}
	layout := g.callLayout(g.geom())
	if layout.presentation == nil || len(layout.tiles) != 10 {
		t.Fatalf("presentation=%v tiles=%d", layout.presentation, len(layout.tiles))
	}
	for i := range layout.tiles {
		if overlap(layout.tiles[i].rect, *layout.presentation) {
			t.Fatalf("tile %d overlaps presentation", i)
		}
		for j := 0; j < i; j++ {
			if overlap(layout.tiles[i].rect, layout.tiles[j].rect) {
				t.Fatalf("tiles %d and %d overlap", i, j)
			}
		}
	}
	if len(g.mediaViews) != 10 {
		t.Fatal("laying out a selected screen cleared camera views")
	}
}

func TestCallTileUsesAvatarMuteAndSpeakingStates(t *testing.T) {
	g, _ := shareGame(t)
	g.mediaViews = map[mediaViewKey]*videoView{
		{Kind: proto.MediaCamera, Fingerprint: "cam"}: &videoView{rgba: frameRGBA(video.NewFrame(4, 4))},
	}
	env := &state.Envelope{Attack: 0.01, Release: 1}
	env.Step(1, 1)
	g.speakEnv = map[string]*state.Envelope{"cam": env}
	camera := g.callTileState(state.CallParticipant{Fingerprint: "cam", Name: "Camera", Muted: true, Camera: true})
	avatar := g.callTileState(state.CallParticipant{Fingerprint: "avatar", Name: "Avatar"})
	if !camera.camera || camera.avatar || !camera.muted || !camera.speaking {
		t.Fatalf("camera tile = %+v", camera)
	}
	if avatar.camera || !avatar.avatar {
		t.Fatalf("avatar tile = %+v", avatar)
	}
}

func TestCallViewControlsAreIndependent(t *testing.T) {
	g, s := shareGame(t)
	s.st.OpenCallView()
	g.share.on = true
	g.camera.on = true
	g.vc.muted = true
	g.handleCallHit(state.Hit{Kind: state.HitCallBack})
	if s.st.CallViewOpen || s.st.InCall != "voice" || !g.share.on || !g.camera.on || !g.vc.muted {
		t.Fatalf("back changed media: open=%v call=%q share=%v camera=%v muted=%v", s.st.CallViewOpen, s.st.InCall, g.share.on, g.camera.on, g.vc.muted)
	}
}
