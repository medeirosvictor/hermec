package ui

import (
	"context"
	"net"
	"strconv"
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

// selfHostPort is the port of the server this app is hosting right now, or 0.
func (g *game) selfHostPort() int {
	h := g.hosted
	if h == nil || h.srv == nil {
		return 0
	}
	_, p, err := net.SplitHostPort(h.srv.Server().Addr())
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

// dropSelfHosted removes the rows that are the server this app hosts on port:
// this machine's loopback, tailnet or LAN address with that port. Joining one
// would open a second session to yourself. port 0 (not hosting) keeps all rows
// and skips the address lookup.
func dropSelfHosted(ctx context.Context, found []state.DiscoveredIn, self func(context.Context) (string, string), port int) []state.DiscoveredIn {
	if port == 0 || len(found) == 0 {
		return found
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ts, lan := self(ctx)
	mine := map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true, ts: ts != "", lan: lan != ""}
	out := found[:0:0]
	for _, f := range found {
		host, p, err := net.SplitHostPort(f.Addr)
		if err == nil && mine[host] && p == strconv.Itoa(port) {
			continue
		}
		out = append(out, f)
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
	hostPort, self := g.selfHostPort(), g.selfAddrs
	if self == nil {
		self = discover.SelfAddrs
	}
	go func() {
		found := discover.Probe(context.Background(), opts)
		in := make([]state.DiscoveredIn, len(found))
		for i, f := range found {
			in[i] = state.DiscoveredIn{Name: f.Name, Addr: f.Addr, Source: f.Source}
		}
		ch <- dropSelfHosted(context.Background(), in, self, hostPort)
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
	active := g.cur().st.Phase == state.PhaseConnect && !g.settingsOpen
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
