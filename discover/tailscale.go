package discover

import (
	"context"
	"encoding/json"
	"net/netip"
	"os/exec"
	"sort"
	"time"
)

// statusTimeout bounds the `tailscale status --json` invocation.
const statusTimeout = 2 * time.Second

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// lookPath and runStatus are seams for tests.
var (
	lookPath  = exec.LookPath
	runStatus = func(ctx context.Context, bin string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, "status", "--json")
		cmd.WaitDelay = 500 * time.Millisecond
		return cmd.Output()
	}
)

// tailscalePeers returns the IPs of online tailnet peers, or nil when the
// tailscale CLI is absent or fails.
func tailscalePeers(parent context.Context) []string {
	bin, err := lookPath("tailscale")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, statusTimeout) // min(2s, probe budget left)
	defer cancel()
	out, err := runStatus(ctx, bin)
	if err != nil {
		return nil
	}
	return parseTailscaleStatus(out)
}

type tsStatus struct {
	Peer map[string]struct {
		Online       bool     `json:"Online"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Peer"`
}

// parseTailscaleStatus extracts one IP per online peer from
// `tailscale status --json`, preferring the 100.64.0.0/10 IPv4 address.
func parseTailscaleStatus(b []byte) []string {
	var st tsStatus
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	var out []string
	for _, p := range st.Peer {
		if !p.Online {
			continue
		}
		// CGNAT IPv4 preferred, else first IPv4, else skip the peer.
		pick := ""
		for _, s := range p.TailscaleIPs {
			ip, err := netip.ParseAddr(s)
			if err != nil || !ip.Is4() {
				continue
			}
			if cgnat.Contains(ip) {
				pick = s
				break
			}
			if pick == "" {
				pick = s
			}
		}
		if pick != "" {
			out = append(out, pick)
		}
	}
	sort.Strings(out)
	return out
}
