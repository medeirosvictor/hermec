package discover

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"sync"
	"time"
)

// DefaultBudget is how long Probe waits for replies when no budget is given.
const DefaultBudget = 1500 * time.Millisecond

// maxUnicast caps concurrent probe sends.
const maxUnicast = 64

// Found is a discovered server.
type Found struct {
	Announce
	// Addr is host:port for the websocket dial: the reply's source IP plus
	// Announce.Port.
	Addr string
	// Source is "lan", "tailnet" or "saved".
	Source string
}

// ProbeOpts configures Probe.
type ProbeOpts struct {
	Port       int      // UDP discovery port (the server's TCP port)
	ExtraHosts []string // saved servers: host or host:port
	// TailscalePeers returns peer IPs to probe; nil uses the tailscale CLI.
	TailscalePeers func() []string
	Budget         time.Duration // 0 = DefaultBudget
	// ListenAddr is the local address of the probe sockets. "" (production)
	// binds the wildcard ":0", which LAN broadcast needs; tests set
	// "127.0.0.1:0" so they never open a wildcard socket (which triggers OS
	// firewall prompts).
	ListenAddr string

	// lanDests overrides the computed LAN broadcast destinations (tests).
	lanDests []string
}

var sourcePriority = map[string]int{"lan": 0, "tailnet": 1, "saved": 2}

// Probe looks for servers over LAN broadcast, the tailnet and saved hosts
// concurrently. It never fails: an empty slice means nothing was found.
func Probe(ctx context.Context, opts ProbeOpts) []Found {
	budget := opts.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	var (
		mu   sync.Mutex
		best = map[string]Found{}
		wg   sync.WaitGroup
	)
	add := func(f Found) {
		mu.Lock()
		defer mu.Unlock()
		if old, ok := best[f.Addr]; ok && sourcePriority[old.Source] <= sourcePriority[f.Source] {
			return
		}
		best[f.Addr] = f
	}

	run := func(source string, bcast bool, dests func(context.Context) []string) {
		defer wg.Done()
		sweep(ctx, source, bcast, opts.ListenAddr, dests, opts.Port, add)
	}
	wg.Add(3)
	go run("lan", true, func(context.Context) []string {
		if opts.lanDests != nil {
			return opts.lanDests
		}
		return broadcastDests(opts.Port)
	})
	go run("tailnet", false, func(ctx context.Context) []string {
		if opts.TailscalePeers != nil {
			return opts.TailscalePeers()
		}
		return tailscalePeers(ctx)
	})
	go run("saved", false, func(context.Context) []string { return opts.ExtraHosts })
	wg.Wait()

	out := make([]Found, 0, len(best))
	for _, f := range best {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out
}

// sweep sends the probe to every destination and collects replies until ctx
// ends. Destinations without a port use defPort.
func sweep(ctx context.Context, source string, bcast bool, listenAddr string, dests func(context.Context) []string, defPort int, add func(Found)) {
	network := "udp"
	if bcast {
		network = "udp4"
	}
	if listenAddr == "" {
		listenAddr = ":0"
	}
	conn, err := net.ListenPacket(network, listenAddr)
	if err != nil {
		return
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() { // unblock the reader on cancellation
		select {
		case <-ctx.Done():
			_ = conn.SetReadDeadline(time.Now())
		case <-stop:
		}
	}()

	// Peers/destinations are resolved concurrently with the reply window
	// (not before it): the window is bounded by the overall deadline either
	// way, so resolving first would only shrink it. Senders are not joined:
	// once the reader exits the conn is closed, so pending WriteTo calls
	// fail fast and DNS lookups are ctx-bound; the only goroutine that can
	// outlive Probe is a caller-supplied TailscalePeers seam that ignores
	// the deadline, which exits when that seam returns.
	go func() {
		sem := make(chan struct{}, maxUnicast)
		var swg sync.WaitGroup
		for _, d := range dests(ctx) {
			if ctx.Err() != nil {
				break
			}
			sem <- struct{}{}
			swg.Add(1)
			go func(d string) {
				defer swg.Done()
				defer func() { <-sem }()
				sendProbe(ctx, conn, d, defPort)
			}(d)
		}
		swg.Wait()
	}()

	buf := make([]byte, MaxPacket+1)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			continue // e.g. ICMP-induced errors on Windows; keep reading
		}
		a, err := ParseAnnounce(buf[:n])
		if err != nil {
			continue
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		add(Found{
			Announce: a,
			Addr:     net.JoinHostPort(ua.IP.String(), strconv.Itoa(a.Port)),
			Source:   source,
		})
	}
}

func sendProbe(ctx context.Context, conn net.PacketConn, dest string, defPort int) {
	host, port, err := net.SplitHostPort(dest)
	if err != nil {
		host, port = dest, strconv.Itoa(defPort)
	}
	pn, err := strconv.Atoi(port)
	if err != nil || pn < 1 || pn > 65535 {
		return
	}
	var ip net.IP
	if a, err := netip.ParseAddr(host); err == nil {
		ip = net.IP(a.AsSlice())
	} else {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host) // ctx-bound DNS
		if err != nil || len(ips) == 0 {
			return
		}
		ip = ips[0].IP
		for _, c := range ips {
			if c.IP.To4() != nil {
				ip = c.IP
				break
			}
		}
	}
	ua := &net.UDPAddr{IP: ip, Port: pn}
	_, _ = conn.WriteTo([]byte(Magic), ua)
}

// broadcastDests lists the limited broadcast address plus the directed
// broadcast address of every up, broadcast-capable IPv4 interface.
func broadcastDests(port int) []string {
	p := strconv.Itoa(port)
	out := []string{net.JoinHostPort("255.255.255.255", p)}
	ifs, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if b := directedBroadcast(ipn); b != nil {
					out = append(out, net.JoinHostPort(b.String(), p))
				}
			}
		}
	}
	return out
}

func directedBroadcast(n *net.IPNet) net.IP {
	ip := n.IP.To4()
	if ip == nil || len(n.Mask) != net.IPv4len {
		return nil
	}
	b := make(net.IP, 4)
	for i := range b {
		b[i] = ip[i] | ^n.Mask[i]
	}
	return b
}
