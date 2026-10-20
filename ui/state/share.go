package state

// Share quality ids, as stored in settings.toml.
const (
	Quality720  = "720p"
	Quality1080 = "1080p"
)

// ShareQualityID is the screen-share quality in effect: 1080p, or 720p for
// unset and anything unknown.
func (s Settings) ShareQualityID() string {
	if s.ShareQuality == Quality1080 {
		return Quality1080
	}
	return Quality720
}

// NextShareQuality is the other quality; unset or unknown counts as 720p.
func NextShareQuality(cur string) string {
	if cur == Quality1080 {
		return Quality720
	}
	return Quality1080
}

// ShareProfile is what a quality means for the encoder: the largest picture
// the screen is fitted into, the frame rate and the bitrate.
type ShareProfile struct {
	MaxW, MaxH, FPS, KBPS int
}

// ShareProfileFor maps a quality id to its profile (unknown ids are 720p).
func ShareProfileFor(q string) ShareProfile {
	if q == Quality1080 {
		return ShareProfile{MaxW: 1920, MaxH: 1080, FPS: 15, KBPS: 3000}
	}
	return ShareProfile{MaxW: 1280, MaxH: 720, FPS: 15, KBPS: 1500}
}

// Letterbox fits a fw x fh picture into the pane [x0,x1) x [y0,y1), keeping
// its aspect ratio and centring it. A degenerate picture or pane gives a
// zero-size result.
func Letterbox(fw, fh int, x0, y0, x1, y1 float64) (x, y, w, h float64) {
	pw, ph := x1-x0, y1-y0
	if fw <= 0 || fh <= 0 || pw <= 0 || ph <= 0 {
		return x0, y0, 0, 0
	}
	if pw*float64(fh) > ph*float64(fw) { // pane is wider than the picture: bars at the sides
		h = ph
		w = ph * float64(fw) / float64(fh)
	} else {
		w = pw
		h = pw * float64(fh) / float64(fw)
	}
	return x0 + (pw-w)/2, y0 + (ph-h)/2, w, h
}

// SharerIn reports the name of the member fp when they are in voice channel
// ch and sharing their screen.
func (s *State) SharerIn(ch, fp string) (name string, ok bool) {
	for _, m := range s.Voice[ch] {
		if m.Fingerprint == fp && m.Sharing {
			return m.Name, true
		}
	}
	return "", false
}
