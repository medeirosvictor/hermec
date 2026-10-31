package state

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeFS map[string]time.Time

func (f fakeFS) stat(p string) (time.Time, error) {
	t, ok := f[p]
	if !ok {
		return time.Time{}, errors.New("missing")
	}
	return t, nil
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestWatcherBaselineNoChange(t *testing.T) {
	fs := fakeFS{"a": t0}
	w := &Watcher{Stat: fs.stat}
	w.Add("a")
	if got := w.Changed(t0); got != nil {
		t.Fatalf("baseline call = %v, want nil", got)
	}
	if got := w.Changed(t0.Add(2 * time.Second)); got != nil {
		t.Fatalf("unchanged = %v, want nil", got)
	}
}

func TestWatcherDetectsMtimeChange(t *testing.T) {
	fs := fakeFS{"a": t0, "b": t0}
	w := &Watcher{Stat: fs.stat}
	w.Add("a")
	w.Add("b")
	w.Changed(t0)
	fs["b"] = t0.Add(time.Minute)
	got := w.Changed(t0.Add(2 * time.Second))
	if !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("got %v, want [b]", got)
	}
	if got := w.Changed(t0.Add(4 * time.Second)); got != nil {
		t.Fatalf("reported twice: %v", got)
	}
}

func TestWatcherThrottlesToOncePerSecond(t *testing.T) {
	fs := fakeFS{"a": t0}
	w := &Watcher{Stat: fs.stat}
	w.Add("a")
	w.Changed(t0)
	fs["a"] = t0.Add(time.Minute)
	if got := w.Changed(t0.Add(500 * time.Millisecond)); got != nil {
		t.Fatalf("within 1s = %v, want nil", got)
	}
	if got := w.Changed(t0.Add(time.Second)); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("after 1s = %v, want [a]", got)
	}
}

func TestWatcherMissingFileNotChange(t *testing.T) {
	fs := fakeFS{}
	w := &Watcher{Stat: fs.stat}
	w.Add("a")
	w.Changed(t0)
	if got := w.Changed(t0.Add(2 * time.Second)); got != nil {
		t.Fatalf("missing = %v, want nil", got)
	}
	// Appearing later establishes a baseline silently; a later edit fires.
	fs["a"] = t0
	if got := w.Changed(t0.Add(4 * time.Second)); got != nil {
		t.Fatalf("appeared = %v, want nil", got)
	}
	fs["a"] = t0.Add(time.Minute)
	if got := w.Changed(t0.Add(6 * time.Second)); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("edit = %v, want [a]", got)
	}
}
