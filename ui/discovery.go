package ui

import (
	"context"
	"time"

	"github.com/medeirosvictor/hermec/discover"
	"github.com/medeirosvictor/hermec/ui/state"
)

// probeEvery is the connect scene's re-probe period.
const probeEvery = 5 * time.Second

// discoverPort is the default server port, probed over LAN broadcast.
const discoverPort = 7697

// savedForDiscovery is the saved list with the local placeholder resolved to
// this run's real URL, so labels match discovered addresses.
func (g *game) savedForDiscovery() []state.ServerEntry {
	out := make([]state.ServerEntry, len(g.servers))
	for i, e := range g.servers {
		e.URL = g.realURL(e.URL)
		out[i] = e
	}
	return out
}

// startProbe runs one discovery probe in a goroutine unless one is in flight.
func (g *game) startProbe() {
	if g.probing {
		return
	}
	g.probing = true
	var extra []string
	if g.localURL != "" {
		extra = append(extra, state.HostPort(g.localURL))
	}
	// ListenAddr stays empty (wildcard) on purpose, even with -local: real
	// LAN broadcast discovery needs it. The cost is a possible firewall
	// prompt per rebuilt exe; keep a stable exe path (see dev docs).
	opts := discover.ProbeOpts{
		Port:       discoverPort,
		ExtraHosts: state.SavedHosts(g.servers, extra...),
	}
	g.logf("discovery probe (extra hosts %v)", opts.ExtraHosts)
	ch := g.probeCh
	go func() {
		found := discover.Probe(context.Background(), opts)
		in := make([]state.DiscoveredIn, len(found))
		for i, f := range found {
			in[i] = state.DiscoveredIn{Name: f.Name, Addr: f.Addr, Source: f.Source}
		}
		ch <- in
	}()
}

// updateDiscovery drains probe results and schedules probes: on scene entry,
// every probeEvery while the connect scene is active, and on a refresh
// request. The schedule is idle whenever the scene is not active.
func (g *game) updateDiscovery() {
	select {
	case in := <-g.probeCh:
		g.probing = false
		prev := ""
		if f := &g.connect; f.sel >= 0 && f.sel < len(f.disc) {
			prev = f.disc[f.sel].Addr
		}
		g.discRows = state.MergeDiscovered(in, g.savedForDiscovery())
		g.connect.disc = g.discRows
		g.connect.sel = state.Reselect(g.discRows, prev)
		g.logf("discovery: %d server(s)", len(g.discRows))
	default:
	}
	g.connect.disc = g.discRows // the form is rebuilt on rail switches
	active := g.st.Phase == state.PhaseConnect && !g.settingsOpen
	if !active {
		g.discActive = false
		return
	}
	now := time.Now()
	if !g.discActive || g.connect.refresh || now.After(g.nextProbe) {
		g.discActive = true
		g.nextProbe = now.Add(probeEvery)
		g.startProbe()
	}
}
