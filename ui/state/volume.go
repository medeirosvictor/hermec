package state

// VolumeSteps are the per-person playback volumes offered in the right-click
// menu, in percent.
var VolumeSteps = []int{0, 25, 50, 75, 100, 125, 150, 200}

// VolumePct is the saved playback volume for fp in percent; unset, or a
// value that is not a step in VolumeSteps, means 100.
func (s Settings) VolumePct(fp string) int {
	v, ok := s.Volumes[fp]
	if ok && validStep(VolumeSteps, v) {
		return v
	}
	return 100
}

// SetVolume saves fp's volume. 100 (the default) is dropped from the map so
// settings.toml only lists people who were changed.
func (s *Settings) SetVolume(fp string, pct int) {
	if pct == 100 {
		delete(s.Volumes, fp)
		if len(s.Volumes) == 0 {
			s.Volumes = nil
		}
		return
	}
	if s.Volumes == nil {
		s.Volumes = map[string]int{}
	}
	s.Volumes[fp] = pct
}

func validStep(steps []int, v int) bool {
	for _, st := range steps {
		if st == v {
			return true
		}
	}
	return false
}
