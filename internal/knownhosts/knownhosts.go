package knownhosts

import (
	"slices"
	"sync"
)

// Max caps the other hosts a Host learns, and a Set keeps.
const Max = 16

// Merge returns a copy of have with the entries of add appended that are
// non-empty and not already there, up to max entries in all (no cap when max
// is 0).
func Merge(have, add []string, max int) []string {
	out := slices.Clone(have)
	for _, u := range add {
		if u != "" && !slices.Contains(out, u) && (max == 0 || len(out) < max) {
			out = append(out, u)
		}
	}
	return out
}

// Set is the set of other hosts' URLs that a Host's owner remembers. Its
// methods fit host.Config.Known (List) and host.Config.SaveKnown (Add).
type Set struct {
	mu      sync.Mutex
	urls    []string
	persist func([]string)
}

// New returns a Set holding urls. persist is given the whole set,
// under the set's lock, whenever Add grows it.
func New(urls []string, persist func([]string)) *Set {
	return &Set{urls: slices.Clone(urls), persist: persist}
}

// List returns a copy of the set.
func (k *Set) List() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.urls)
}

// Add merges urls into the set, up to Max, and persists it if it grew.
func (k *Set) Add(urls []string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	merged := Merge(k.urls, urls, Max)
	if len(merged) > len(k.urls) {
		k.urls = merged
		k.persist(slices.Clone(merged))
	}
}
