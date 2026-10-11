package state

import "time"

// watchInterval is the minimum gap between two polls.
const watchInterval = time.Second

// Watcher polls file modification times. It is not safe for concurrent use;
// call it from one goroutine (the game's Update).
type Watcher struct {
	// Stat returns a file's modification time (os.Stat in production).
	Stat func(path string) (time.Time, error)

	paths []string
	mtime map[string]time.Time // last seen mtime; absent until the file first exists
	last  time.Time            // time of the last poll
	began bool
}

// Add registers path. A path that does not exist (yet) is harmless: a missing
// file is never reported as a change, and its baseline is taken when it
// first appears.
func (w *Watcher) Add(path string) {
	w.paths = append(w.paths, path)
}

// Changed polls the watched files at most once per second and returns those
// whose mtime differs from the last one seen. The first poll only records
// baselines and returns nil.
func (w *Watcher) Changed(now time.Time) []string {
	if w.began && now.Sub(w.last) < watchInterval {
		return nil
	}
	first := !w.began
	w.began, w.last = true, now
	if w.mtime == nil {
		w.mtime = map[string]time.Time{}
	}
	var out []string
	for _, p := range w.paths {
		mt, err := w.Stat(p)
		if err != nil {
			continue
		}
		old, seen := w.mtime[p]
		w.mtime[p] = mt
		if seen && !first && !mt.Equal(old) {
			out = append(out, p)
		}
	}
	return out
}
