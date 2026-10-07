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
		return exec.CommandContext(ctx, bin, "status", "--json").Output()
	}
)

// tailscalePeers returns the IPs of online tailnet peers, or nil when the
// tailscale CLI is absent or fails.
func tailscalePeers() []string {
	bin, err := lookPath("tailscale")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
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
		pick := ""
		for _, s := range p.TailscaleIPs {
			ip, err := netip.ParseAddr(s)
			if err != nil {
				continue
			}
			if ip.Is4() && cgnat.Contains(ip) {
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
