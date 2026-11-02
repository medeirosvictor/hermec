package ui

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/medeirosvictor/hermec/ui/state"
)

type fakeVolumeSink struct {
	io.Closer
	got map[string]int
}

func (f *fakeVolumeSink) SetVolume(fp string, pct int) { f.got[fp] = pct }

func TestVolumeItemsMarkCurrent(t *testing.T) {
	g := newTestGame(t)
	g.set.SetVolume("fp1", 150)
	items := g.volumeItems("fp1")
	if len(items) != len(state.VolumeSteps) {
		t.Fatalf("%d items", len(items))
	}
	for i, it := range items {
		marked := strings.HasPrefix(it.label, "> ")
		if marked != (state.VolumeSteps[i] == 150) {
			t.Errorf("item %q marked=%v", it.label, marked)
		}
	}
}

func TestSetVolumeAppliesLiveAndPersists(t *testing.T) {
	g := newTestGame(t)
	g.settingsPath = filepath.Join(t.TempDir(), "settings.toml")
	sink := &fakeVolumeSink{got: map[string]int{}}
	g.vc.sink = sink
	g.volumeItems("fp1")[0].act() // 0%
	if sink.got["fp1"] != 0 {
		t.Fatalf("live volume = %v", sink.got)
	}
	g.volumeItems("fp1")[4].act() // 100%
	if sink.got["fp1"] != 100 {
		t.Fatalf("live volume = %v", sink.got)
	}
	got, err := state.LoadSettings(g.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Volumes) != 0 {
		t.Fatalf("100%% should not be saved: %v", got.Volumes)
	}
	g.volumeItems("fp2")[7].act() // 200%
	got, _ = state.LoadSettings(g.settingsPath)
	if got.VolumePct("fp2") != 200 {
		t.Fatalf("saved = %v", got.Volumes)
	}
}

func TestApplyVolumesOnOpenedSink(t *testing.T) {
	sink := &fakeVolumeSink{got: map[string]int{}}
	applyVolumes(sink, map[string]int{"a": 50, "b": 200})
	if sink.got["a"] != 50 || sink.got["b"] != 200 {
		t.Fatalf("got %v", sink.got)
	}
	applyVolumes(struct{}{}, map[string]int{"a": 1}) // a sink without volume control is left alone
}
