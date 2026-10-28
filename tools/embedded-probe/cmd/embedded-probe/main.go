package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/medeirosvictor/hermec/netpath"
	probe "hermec-embedded-probe"
)

const markerName = ".hermec-embedded-probe"

func main() {
	state := flag.String("state", "", "disposable probe state directory (required)")
	controlURL := flag.String("control-url", "", "Tailscale or Headscale coordination URL")
	hostname := flag.String("hostname", "hermec-probe", "embedded node hostname prefix")
	role := flag.String("role", "all", "all, host, client, or standby")
	peers := flag.String("peers", "", "comma-separated peer overlay IPs expected after enrollment")
	port := flag.Int("port", 17697, "primary HERMEC TCP/UDP port")
	reportPath := flag.String("report", "", "write credential-free JSON report")
	timeout := flag.Duration("timeout", 2*time.Minute, "enrollment and scenario timeout")
	flag.Parse()
	if err := run(*state, *controlURL, *hostname, *role, *peers, *port, *reportPath, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, "embedded probe:", err)
		os.Exit(1)
	}
}

func run(state, controlURL, hostname, role, peers string, port int, reportPath string, timeout time.Duration) error {
	if state == "" {
		return errors.New("-state is required")
	}
	if port < 1 || port > 65534 {
		return errors.New("-port must be between 1 and 65534")
	}
	if role != "all" && role != "host" && role != "client" && role != "standby" {
		return errors.New("-role must be all, host, client, or standby")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if role != "all" {
		dir, err := prepareStateDir(state)
		if err != nil {
			return err
		}
		n, err := openNode(ctx, dir, hostname, controlURL)
		if err != nil {
			return err
		}
		defer n.Close()
		if err := requirePeers(n, peers); err != nil {
			return err
		}
		report := probe.ScenarioReport{StartedAt: time.Now(), Addresses: addrStrings(n.Addresses()), Checks: map[string]probe.CheckResult{"profile_ready": {Passed: true}}}
		return emitReport(report, reportPath)
	}

	if _, err := prepareStateDir(state); err != nil {
		return err
	}
	roles := []string{"host", "client", "standby"}
	var nodes [3]*probe.Node
	for i, name := range roles {
		dir, err := prepareStateDir(filepath.Join(state, name))
		if err != nil {
			closeNodes(nodes[:])
			return err
		}
		nodes[i], err = openNode(ctx, dir, hostname+"-"+name, controlURL)
		if err != nil {
			closeNodes(nodes[:])
			return err
		}
	}
	defer closeNodes(nodes[:])
	for _, n := range nodes {
		if err := n.Refresh(ctx); err != nil {
			return err
		}
	}
	if err := requirePeers(nodes[0], peers); err != nil {
		return err
	}
	hostIP, err := firstIP(nodes[0])
	if err != nil {
		return err
	}
	standbyIP, err := firstIP(nodes[2])
	if err != nil {
		return err
	}
	report, err := probe.RunScenario(ctx, probe.ScenarioConfig{
		Networks:    [3]netpath.Network{nodes[0], nodes[1], nodes[2]},
		PrimaryAddr: hostIP.String(), StandbyAddr: standbyIP.String(),
		Port: port, StandbyPort: port + 1, Timeout: timeout,
	})
	if writeErr := emitReport(report, reportPath); writeErr != nil {
		return writeErr
	}
	return err
}

func openNode(ctx context.Context, dir, hostname, controlURL string) (*probe.Node, error) {
	return probe.Open(ctx, probe.ProfileConfig{StateDir: dir, Hostname: hostname, ControlURL: controlURL, OnLoginURL: func(url string) {
		fmt.Println("Approve this disposable probe node in your browser:")
		fmt.Println(url)
	}})
}

func prepareStateDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", err
	}
	marker := filepath.Join(abs, markerName)
	if len(entries) > 0 {
		if _, err := os.Stat(marker); err != nil {
			return "", fmt.Errorf("state directory is non-empty and was not created by this probe: %s", abs)
		}
	} else if err := os.WriteFile(marker, []byte("HERMEC disposable embedded-network probe\n"), 0o600); err != nil {
		return "", err
	}
	return abs, nil
}

func requirePeers(n *probe.Node, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	have := map[netip.Addr]bool{}
	for _, ip := range n.PeerAddresses() {
		have[ip] = true
	}
	for _, part := range strings.Split(raw, ",") {
		ip, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil {
			return fmt.Errorf("invalid -peers IP %q", part)
		}
		if !have[ip.Unmap()] {
			return fmt.Errorf("expected peer %s is not reachable in the profile snapshot", ip)
		}
	}
	return nil
}

func firstIP(n *probe.Node) (netip.Addr, error) {
	for _, ip := range n.Addresses() {
		if ip.Is4() {
			return ip, nil
		}
	}
	for _, ip := range n.Addresses() {
		if ip.Is6() {
			return ip, nil
		}
	}
	return netip.Addr{}, errors.New("profile has no assigned IP")
}
func addrStrings(in []netip.Addr) []string {
	out := make([]string, len(in))
	for i, ip := range in {
		out[i] = ip.String()
	}
	return out
}
func closeNodes(nodes []*probe.Node) {
	for _, n := range nodes {
		if n != nil {
			_ = n.Close()
		}
	}
}
func emitReport(report probe.ScenarioReport, path string) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if path == "" {
		fmt.Println(string(raw))
		return nil
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
