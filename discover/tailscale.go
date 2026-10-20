package discover

import (
	"context"
	"encoding/json"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// statusTimeout bounds the `tailscale status --json` invocation.
const statusTimeout = 2 * time.Second

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// macAppTailscale is where the Mac app bundles its CLI; the app does not put
// it on PATH.
const macAppTailscale = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"

var (
	noTailscale     atomic.Bool
	noTailscaleOnce sync.Once
)

// TailscaleMissing reports that a lookup found no tailscale CLI, so the Join
// page can say tailnet discovery is off. False until a lookup has run.
func TailscaleMissing() bool { return noTailscale.Load() }

// tailscaleBin finds the tailscale CLI: PATH first, then (macOS only) the app
// bundle. A miss is logged once and recorded for TailscaleMissing.
func tailscaleBin() (string, bool) {
	if bin, err := lookPath("tailscale"); err == nil {
		noTailscale.Store(false)
		return bin, true
	}
	if goos == "darwin" {
		if fi, err := statFn(macAppTailscale); err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0 {
			noTailscale.Store(false)
			return macAppTailscale, true
		}
	}
	noTailscale.Store(true)
	noTailscaleOnce.Do(func() { log.Print("tailscale CLI not found; tailnet discovery off") })
	return "", false
}

// lookPath, goos, statFn and runStatus are seams for tests.
var (
	goos      = runtime.GOOS
	statFn    = os.Stat
	lookPath  = exec.LookPath
	runStatus = func(ctx context.Context, bin string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, "status", "--json")
		hideConsole(cmd)
		cmd.WaitDelay = 500 * time.Millisecond
		return cmd.Output()
	}
)

// tailscalePeers returns the IPs of online tailnet peers, or nil when the
// tailscale CLI is absent or fails.
func tailscalePeers(parent context.Context) []string {
	bin, ok := tailscaleBin()
	if !ok {
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
