package state

import "sort"

// MaxDiscovered caps the rows shown in the connect scene's DISCOVERED list.
const MaxDiscovered = 6

// DiscoveredIn is one probe result, decoupled from the discover package.
type DiscoveredIn struct {
	Name, Addr, Source string
}

// DiscoveredRow is one displayable discovered server.
type DiscoveredRow struct {
	Name   string // saved label when the server is already saved
	Source string
	Addr   string // host:port
	URL    string // dial URL
	Saved  bool
}

var discSourceOrder = map[string]int{"lan": 0, "tailnet": 1, "saved": 2}

// MergeDiscovered turns probe results into rows: sorted by source priority
// (lan, tailnet, saved) then Addr, capped at MaxDiscovered, with the saved
// label replacing the announced name for servers already in saved.
func MergeDiscovered(found []DiscoveredIn, saved []ServerEntry) []DiscoveredRow {
	if len(found) == 0 {
		return nil
	}
	labels := make(map[string]string, len(saved))
	for _, e := range saved {
		labels[HostPort(e.URL)] = e.Label
	}
	rows := make([]DiscoveredRow, 0, len(found))
	for _, f := range found {
		r := DiscoveredRow{Name: f.Name, Source: f.Source, Addr: f.Addr, URL: "ws://" + f.Addr + "/"}
		if l, ok := labels[f.Addr]; ok {
			r.Saved = true
			if l != "" {
				r.Name = l
			}
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := discSourceOrder[rows[i].Source], discSourceOrder[rows[j].Source]
		if a != b {
			return a < b
		}
		return rows[i].Addr < rows[j].Addr
	})
	if len(rows) > MaxDiscovered {
		rows = rows[:MaxDiscovered]
	}
	return rows
}

// MoveSel moves the list selection by d within n rows. -1 means no selection
// (focus on the fields): Down from -1 selects the first row, Up from the
// first row returns to -1. The result is always in [-1, n-1].
func MoveSel(sel, d, n int) int {
	if n <= 0 {
		return -1
	}
	sel += d
	if sel < -1 {
		sel = -1
	}
	if sel > n-1 {
		sel = n - 1
	}
	return sel
}

// Reselect returns the index of addr in rows, or -1 (also for empty addr).
func Reselect(rows []DiscoveredRow, addr string) int {
	if addr == "" {
		return -1
	}
	for i, r := range rows {
		if r.Addr == addr {
			return i
		}
	}
	return -1
}

// SavedHosts lists the distinct hosts to probe directly: the host[:port] of
// each saved server URL (skipping the embedded-local placeholder) followed by
// extra.
func SavedHosts(saved []ServerEntry, extra ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, e := range saved {
		if e.URL == "hermec://local" {
			continue
		}
		add(HostPort(e.URL))
	}
	for _, h := range extra {
		add(h)
	}
	return out
}
