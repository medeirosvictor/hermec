package ui

import (
	"fmt"

	"github.com/medeirosvictor/hermec/ui/state"
)

// volumeSetter is the playback sink's per-speaker volume control.
type volumeSetter interface{ SetVolume(fp string, pct int) }

// volumeItems are the volume rows of a person's context menu: one per step,
// the current one marked.
func (g *game) volumeItems(fp string) []menuItem {
	cur := g.set.VolumePct(fp)
	items := make([]menuItem, len(state.VolumeSteps))
	for i, pct := range state.VolumeSteps {
		marker := "  "
		if pct == cur {
			marker = "> "
		}
		items[i] = menuItem{label: fmt.Sprintf("%s%3d%% volume", marker, pct), act: func() { g.setVolume(fp, pct) }}
	}
	return items
}

// setVolume saves fp's playback volume and applies it to the running call.
func (g *game) setVolume(fp string, pct int) {
	g.set.SetVolume(fp, pct)
	g.logf("volume for %s: %d%%", fp, pct)
	g.persistSettings()
	if v, ok := g.vc.sink.(volumeSetter); ok {
		v.SetVolume(fp, pct)
	}
}

// applyVolumes sets every saved volume on a freshly opened sink.
func applyVolumes(sink any, volumes map[string]int) {
	if v, ok := sink.(volumeSetter); ok {
		for fp, pct := range volumes {
			v.SetVolume(fp, pct)
		}
	}
}

// micGainSetter is the capture source's gain control.
type micGainSetter interface{ SetGain(pct int) }

// applyMicGain sets the mic gain on an open source; sources without gain
// control are left alone.
func applyMicGain(src any, pct int) {
	if m, ok := src.(micGainSetter); ok {
		m.SetGain(pct)
	}
}
