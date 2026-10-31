package host

import (
	"slices"
	"sync"
)

// MaxKnown caps the other hosts a Host learns, and a Known set keeps.
const MaxKnown = 16

// MergeURLs returns a copy of have with the entries of add appended that are
// non-empty and not already there, up to max entries in all (no cap when max
// is 0).
func MergeURLs(have, add []string, max int) []string {
	out := slices.Clone(have)
	for _, u := range add {
		if u != "" && !slices.Contains(out, u) && (max == 0 || len(out) < max) {
			out = append(out, u)
		}
	}
	return out
}

// Known is the set of other hosts' URLs that a Host's owner remembers. Its
// methods fit Config.Known (List) and Config.SaveKnown (Add).
type Known struct {
	mu      sync.Mutex
	urls    []string
	persist func([]string)
}

// NewKnown returns a Known holding urls. persist is given the whole set,
// under the set's lock, whenever Add grows it.
func NewKnown(urls []string, persist func([]string)) *Known {
	return &Known{urls: slices.Clone(urls), persist: persist}
}

// List returns a copy of the set.
func (k *Known) List() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.urls)
}

// Add merges urls into the set, up to MaxKnown, and persists it if it grew.
func (k *Known) Add(urls []string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	merged := MergeURLs(k.urls, urls, MaxKnown)
	if len(merged) > len(k.urls) {
		k.urls = merged
		k.persist(slices.Clone(merged))
	}
}
